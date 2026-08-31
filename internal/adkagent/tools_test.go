package adkagent

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// newTestWorkspace は、検証用のファイルを敷いた作業ディレクトリを作ります。
func newTestWorkspace(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	files := map[string]string{
		"chapter1.md":        "# 第一章\n主人公のアキラは剣士である。\n",
		"chapter2.md":        "# 第二章\nアキラは魔法を使った。\n",
		"notes/settings.txt": "アキラ: 剣士。魔法は使えない。\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("ディレクトリの作成に失敗: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("ファイルの作成に失敗: %v", err)
		}
	}
	return dir
}

func newTestToolbox(t *testing.T, budget int64) *toolbox {
	t.Helper()

	tb, err := newToolbox(newTestWorkspace(t), budget)
	if err != nil {
		t.Fatalf("toolbox の生成に失敗: %v", err)
	}
	return tb
}

func TestReadFile(t *testing.T) {
	tb := newTestToolbox(t, 10)

	got, err := tb.readFile(t.Context(), readFileArgs{Path: "chapter1.md"})
	if err != nil {
		t.Fatalf("readFile がエラーを返しました: %v", err)
	}
	if got.Error != "" {
		t.Fatalf("Error が入っています: %s", got.Error)
	}
	if !strings.Contains(got.Content, "アキラ") {
		t.Errorf("内容が読めていません: %q", got.Content)
	}
}

func TestReadFileRejectsEscape(t *testing.T) {
	tb := newTestToolbox(t, 10)

	for _, path := range []string{"../secret.txt", "/etc/passwd", "notes/../../secret.txt", ""} {
		got, err := tb.readFile(t.Context(), readFileArgs{Path: path})
		if err != nil {
			t.Fatalf("readFile がエラーを返しました: %v", err)
		}
		if got.Error == "" {
			t.Errorf("パス %q が拒否されていません", path)
		}
	}
}

func TestReadFileRejectsSymlinkEscape(t *testing.T) {
	tb := newTestToolbox(t, 10)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("外部ファイルの作成に失敗: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(tb.root, "link.txt")); err != nil {
		t.Skipf("symlink を作成できない環境のためスキップします: %v", err)
	}

	got, err := tb.readFile(t.Context(), readFileArgs{Path: "link.txt"})
	if err != nil {
		t.Fatalf("readFile がエラーを返しました: %v", err)
	}
	if got.Error == "" {
		t.Error("symlink 経由の脱出が拒否されていません")
	}
}

func TestBudgetExhaustion(t *testing.T) {
	tb := newTestToolbox(t, 2)

	if got, _ := tb.readFile(t.Context(), readFileArgs{Path: "chapter1.md"}); got.Error != "" {
		t.Fatalf("1 回目が失敗しました: %s", got.Error)
	}
	if got, _ := tb.listFiles(t.Context(), listFilesArgs{}); got.Error != "" {
		t.Fatalf("2 回目が失敗しました: %s", got.Error)
	}

	// 3 回目以降は、どのツールでも打ち切りのメッセージが返ります。
	got, _ := tb.searchText(t.Context(), searchTextArgs{Query: "アキラ"})
	if !strings.Contains(got.Error, "上限") {
		t.Errorf("上限メッセージが返っていません: %q", got.Error)
	}
}

func TestListFiles(t *testing.T) {
	tb := newTestToolbox(t, 10)

	got, err := tb.listFiles(t.Context(), listFilesArgs{})
	if err != nil {
		t.Fatalf("listFiles がエラーを返しました: %v", err)
	}
	want := []string{"chapter1.md", "chapter2.md", "notes/settings.txt"}
	if len(got.Files) != len(want) {
		t.Fatalf("Files = %v, want %v", got.Files, want)
	}
	for _, name := range want {
		found := false
		for _, f := range got.Files {
			if f == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s が一覧にありません: %v", name, got.Files)
		}
	}
}

func TestSearchText(t *testing.T) {
	tb := newTestToolbox(t, 10)

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "魔法"})
	if err != nil {
		t.Fatalf("searchText がエラーを返しました: %v", err)
	}
	if len(got.Hits) != 2 {
		t.Fatalf("Hits = %v, want 2 件", got.Hits)
	}
	for _, hit := range got.Hits {
		if !strings.Contains(hit, ":") {
			t.Errorf("path:line 形式ではありません: %q", hit)
		}
	}
}

// 上限を超える日本語ファイルが、バイナリ扱いで拒否されずに読めること。
//
// 切り詰めてから utf8.Valid を見る順序に戻すと、3 バイト文字が境界で割れる 2/3 の
// 確率で「テキストファイルではありません」になります。長い日本語原稿は novel /
// article モードの主対象なので、いちばん使う経路でだけ壊れます。
func TestReadFileTruncatesAtRuneBoundary(t *testing.T) {
	t.Parallel()

	for pad := range 3 {
		t.Run(fmt.Sprintf("境界を%dバイトずらす", pad), func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			content := strings.Repeat("x", pad) + strings.Repeat("あ", maxFileBytes)
			if err := os.WriteFile(filepath.Join(dir, "long.md"), []byte(content), 0o600); err != nil {
				t.Fatalf("ファイルの作成に失敗: %v", err)
			}
			tb, err := newToolbox(dir, 10)
			if err != nil {
				t.Fatalf("toolbox の生成に失敗: %v", err)
			}

			got, err := tb.readFile(t.Context(), readFileArgs{Path: "long.md"})
			if err != nil {
				t.Fatalf("readFile が error を返した: %v", err)
			}
			if got.Error != "" {
				t.Fatalf("正常な日本語ファイルが拒否された: %s", got.Error)
			}
			if !got.Truncated {
				t.Error("上限超えなのに Truncated が false")
			}
			if !utf8.ValidString(got.Content) {
				t.Error("切り詰めた内容が壊れた UTF-8 になっている")
			}
			if len(got.Content) > maxFileBytes {
				t.Errorf("上限を超えて返している: %d > %d", len(got.Content), maxFileBytes)
			}
		})
	}
}

// 上限以下のファイルは切り詰めないこと。
func TestReadFileKeepsShortFileIntact(t *testing.T) {
	t.Parallel()

	tb := newTestToolbox(t, 10)
	got, err := tb.readFile(t.Context(), readFileArgs{Path: "chapter1.md"})
	if err != nil {
		t.Fatalf("readFile が error を返した: %v", err)
	}
	if got.Truncated {
		t.Error("短いファイルで Truncated が true")
	}
	if !strings.Contains(got.Content, "アキラ") {
		t.Errorf("内容が読めていない: %q", got.Content)
	}
}

// ★ 検索が symlink を辿って作業ディレクトリの外を読まないこと。
//
// read_file は resolve で塞いでいますが、search_text は WalkDir が返したパスを直接開くため、
// 別の入口になります。WalkDir は symlink のディレクトリを再帰しない一方、ファイルとして
// 現れた symlink は通常ファイルと同じ顔をして渡ってきます。DirEntry.Info() が返すのは
// リンク自身の情報（サイズは数十バイト）なので、サイズによる足切りも効きません。
// ホスト上の秘密鍵や /proc/self/environ を指す symlink を仕込まれると、検索語に一致した
// 行がそのままモデルの入力に入ります。
func TestSearchTextRejectsSymlinkEscape(t *testing.T) {
	tb := newTestToolbox(t, 10)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("アキラ-この行は作業ディレクトリの外にある\n"), 0o600); err != nil {
		t.Fatalf("外部ファイルの作成に失敗: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(tb.root, "link.txt")); err != nil {
		t.Skipf("symlink を作成できない環境のためスキップします: %v", err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "アキラ"})
	if err != nil {
		t.Fatalf("searchText がエラーを返しました: %v", err)
	}
	for _, hit := range got.Hits {
		if strings.Contains(hit, "作業ディレクトリの外") {
			t.Errorf("symlink 経由で外部ファイルの内容が返っています: %q", hit)
		}
		if strings.HasPrefix(hit, "link.txt:") {
			t.Errorf("symlink が検索対象になっています: %q", hit)
		}
	}
}

// 一覧に symlink を出さないこと。
//
// 一覧に出しても中身は漏れませんが、read_file が拒否するパスを「読めるファイル」として
// モデルに見せることになり、無駄なツール呼び出しを誘発します。
func TestListFilesSkipsSymlink(t *testing.T) {
	tb := newTestToolbox(t, 10)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("外部ファイルの作成に失敗: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(tb.root, "link.txt")); err != nil {
		t.Skipf("symlink を作成できない環境のためスキップします: %v", err)
	}

	got, err := tb.listFiles(t.Context(), listFilesArgs{})
	if err != nil {
		t.Fatalf("listFiles がエラーを返しました: %v", err)
	}
	for _, name := range got.Files {
		if name == "link.txt" {
			t.Errorf("symlink が一覧に出ています: %v", got.Files)
		}
	}
}

// 長い行は 1 ヒットぶんの上限で切り、文字境界を壊さないこと。
//
// ヒット件数の上限だけでは総量を抑えられません（minify 済みの 1 行が 1 MiB あれば
// 1 ヒットで 1 MiB です）。
func TestSearchTextTruncatesLongHit(t *testing.T) {
	dir := t.TempDir()
	// 3 バイト文字だけの長い 1 行。上限の位置が文字の途中に来ます。
	line := strings.Repeat("あ", maxHitBytes) + "魔法\n"
	if err := os.WriteFile(filepath.Join(dir, "long.md"), []byte(line), 0o600); err != nil {
		t.Fatalf("ファイルの作成に失敗: %v", err)
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("toolbox の生成に失敗: %v", err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "魔法"})
	if err != nil {
		t.Fatalf("searchText がエラーを返しました: %v", err)
	}
	if len(got.Hits) != 1 {
		t.Fatalf("Hits = %d 件, want 1 件", len(got.Hits))
	}
	if !got.Truncated {
		t.Error("切り詰めたのに Truncated が false")
	}
	if !utf8.ValidString(got.Hits[0]) {
		t.Error("切り詰めたヒットが壊れた UTF-8 になっています")
	}
	// "path:line: " と省略記号のぶんだけ上限を超えます。行そのものが上限内であればよいです。
	if len(got.Hits[0]) > maxHitBytes+64 {
		t.Errorf("1 ヒットが長すぎます: %d バイト", len(got.Hits[0]))
	}
}

// 返却の総バイト数に上限があること。
func TestSearchTextCapsTotalBytes(t *testing.T) {
	dir := t.TempDir()
	// 1 ファイル 1 ヒット、各ヒットは 1 ヒットぶんの上限まで切り詰められます。
	// 総量の上限に当たるだけの本数を置きます。
	files := maxSearchResultBytes/maxHitBytes + 10
	line := strings.Repeat("x", maxHitBytes*2) + "魔法\n"
	for i := range files {
		name := filepath.Join(dir, fmt.Sprintf("file%03d.md", i))
		if err := os.WriteFile(name, []byte(line), 0o600); err != nil {
			t.Fatalf("ファイルの作成に失敗: %v", err)
		}
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("toolbox の生成に失敗: %v", err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "魔法"})
	if err != nil {
		t.Fatalf("searchText がエラーを返しました: %v", err)
	}
	if !got.Truncated {
		t.Error("総量の上限に当たったのに Truncated が false")
	}
	total := 0
	for _, hit := range got.Hits {
		total += len(hit)
	}
	if total > maxSearchResultBytes {
		t.Errorf("総量が上限を超えています: %d > %d", total, maxSearchResultBytes)
	}
}

// ヒット行のインデントを保つこと。
//
// TrimSpace まで掛けると、ネストの深さや箇条書きの階層がモデルから見えなくなります。
func TestSearchTextKeepsIndent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("    - アキラの設定\n"), 0o600); err != nil {
		t.Fatalf("ファイルの作成に失敗: %v", err)
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("toolbox の生成に失敗: %v", err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "アキラ"})
	if err != nil {
		t.Fatalf("searchText がエラーを返しました: %v", err)
	}
	if len(got.Hits) != 1 || !strings.HasSuffix(got.Hits[0], "    - アキラの設定") {
		t.Errorf("インデントが落ちています: %q", got.Hits)
	}
}

// 長すぎる query は走査せずに拒否すること。
func TestSearchTextRejectsLongQuery(t *testing.T) {
	tb := newTestToolbox(t, 10)

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: strings.Repeat("a", maxQueryBytes+1)})
	if err != nil {
		t.Fatalf("searchText がエラーを返しました: %v", err)
	}
	if got.Error == "" {
		t.Error("長すぎる query が拒否されていません")
	}
}

// キャンセル済みの context では走査せず、Error フィールドではなく Go の error を返すこと。
//
// 打ち切りを Error フィールドに載せると「探したが見つからなかった」と区別が付かず、
// モデルは調査済みのつもりで最終レビューをまとめてしまいます。
func TestToolsStopOnCanceledContext(t *testing.T) {
	tb := newTestToolbox(t, 10)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := tb.listFiles(ctx, listFilesArgs{}); err == nil {
		t.Error("list_files がキャンセルを無視しています")
	}
	if _, err := tb.searchText(ctx, searchTextArgs{Query: "アキラ"}); err == nil {
		t.Error("search_text がキャンセルを無視しています")
	}
}

// ★ 範囲を指定して読めること。
//
// 全文を読ませないための機能です。読んだ内容は以降のやり取りすべてに残り、
// そのたびに読み直されるので、1 回の無駄が実行の終わりまで効き続けます。
func TestReadFileRange(t *testing.T) {
	dir := t.TempDir()
	body := "l1\nl2\nl3\nl4\nl5\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tb, err := newToolbox(dir, 50)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		from, lines int
		want        string
		wantFrom    int
		wantTo      int
	}{
		{"範囲指定なしは全文", 0, 0, "l1\nl2\nl3\nl4\nl5", 1, 5},
		{"途中から数行", 2, 2, "l2\nl3", 2, 3},
		{"行数を省くと末尾まで", 4, 0, "l4\nl5", 4, 5},
		{"末尾を超える行数は詰める", 4, 99, "l4\nl5", 4, 5},
		{"0 以下の from は先頭", -3, 1, "l1", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tb.readFile(t.Context(), readFileArgs{Path: "f.txt", From: tt.from, Lines: tt.lines})
			if err != nil {
				t.Fatalf("readFile: %v", err)
			}
			if got.Content != tt.want {
				t.Errorf("Content = %q, want %q", got.Content, tt.want)
			}
			if got.From != tt.wantFrom || got.To != tt.wantTo {
				t.Errorf("範囲 = %d-%d, want %d-%d", got.From, got.To, tt.wantFrom, tt.wantTo)
			}
			// 続きがあるかを判断できないと、範囲で読んだモデルは全部読んだつもりになります。
			if got.TotalLines != 5 {
				t.Errorf("TotalLines = %d, want 5", got.TotalLines)
			}
		})
	}
}

// ファイルの末尾より後ろを指定しても、エラーにせず総行数だけ返すこと。
// 総行数が分かれば、モデルは自分で指定し直せます。
func TestReadFileRangePastEndReportsTotal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tb, err := newToolbox(dir, 50)
	if err != nil {
		t.Fatal(err)
	}

	got, err := tb.readFile(t.Context(), readFileArgs{Path: "f.txt", From: 99, Lines: 5})
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if got.Error != "" {
		t.Errorf("範囲外がエラーになっています: %q", got.Error)
	}
	if got.Content != "" || got.TotalLines != 2 {
		t.Errorf("Content = %q, TotalLines = %d, want \"\" / 2", got.Content, got.TotalLines)
	}
}

// ヒットの前後を添えられること。read_file で開き直す往復を減らすためのものです。
func TestSearchTextContext(t *testing.T) {
	dir := t.TempDir()
	body := "one\ntwo\nTARGET\nfour\nfive\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tb, err := newToolbox(dir, 50)
	if err != nil {
		t.Fatal(err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "TARGET", Context: 1})
	if err != nil {
		t.Fatalf("searchText: %v", err)
	}
	want := []string{"f.txt:2: two", "f.txt:3: TARGET", "f.txt:4: four"}
	if !slices.Equal(got.Hits, want) {
		t.Errorf("Hits = %v, want %v", got.Hits, want)
	}
}

// 近いヒットの文脈が重なっても、同じ行を二度並べないこと。
// 潰さないと、総量の上限を重複で食い潰します。
func TestSearchTextContextDoesNotRepeatLines(t *testing.T) {
	dir := t.TempDir()
	body := "a\nTARGET\nb\nTARGET\nc\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tb, err := newToolbox(dir, 50)
	if err != nil {
		t.Fatal(err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "TARGET", Context: 2})
	if err != nil {
		t.Fatalf("searchText: %v", err)
	}
	want := []string{"f.txt:1: a", "f.txt:2: TARGET", "f.txt:3: b", "f.txt:4: TARGET", "f.txt:5: c"}
	if !slices.Equal(got.Hits, want) {
		t.Errorf("Hits = %v, want %v", got.Hits, want)
	}
}

// context を指定しなければ、これまでどおりヒット行だけを返すこと。
// 広い検索で文脈を足すと、そのぶんヒットそのものが総量の上限で落ちます。
func TestSearchTextWithoutContextReturnsOnlyMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nTARGET\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tb, err := newToolbox(dir, 50)
	if err != nil {
		t.Fatal(err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "TARGET"})
	if err != nil {
		t.Fatalf("searchText: %v", err)
	}
	if !slices.Equal(got.Hits, []string{"f.txt:2: TARGET"}) {
		t.Errorf("Hits = %v", got.Hits)
	}
}

// from と lines はモデルが書く JSON なので、int の上限に近い値が届き得ます。
// 足してから比べていた頃は桁溢れで end が from を下回り、切り出しで panic しました。
func TestReadFileHandlesOutOfRangeArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args readFileArgs
	}{
		{"lines が極端に大きい", readFileArgs{Path: "chapter1.md", From: 2, Lines: math.MaxInt}},
		{"from も lines も極端に大きい", readFileArgs{Path: "chapter1.md", From: math.MaxInt, Lines: math.MaxInt}},
		{"lines が負", readFileArgs{Path: "chapter1.md", From: 1, Lines: math.MinInt}},
		{"from が負", readFileArgs{Path: "chapter1.md", From: math.MinInt, Lines: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := newTestToolbox(t, 10).readFile(t.Context(), tt.args)
			if err != nil {
				t.Fatalf("readFile が error を返した: %v", err)
			}
			if got.To < got.From {
				t.Errorf("範囲が逆転しています: from=%d to=%d", got.From, got.To)
			}
		})
	}
}

// 打ち切ったとき、To は「全部返せた最後の行」であること。
//
// 1 行でも多く申告すると、モデルは次にその先から読むので、渡していない行が黙って
// 飛ばされます。切り口が行の途中に来る場合と、改行のちょうど直後に来る場合の両方を見ます
// （後者では、申告した行が 1 バイトも入っていません）。
func TestReadFileTruncationReportsOnlyCompleteLines(t *testing.T) {
	t.Parallel()

	for _, width := range []int{99, 98} {
		t.Run(fmt.Sprintf("行長%dバイト", width), func(t *testing.T) {
			t.Parallel()

			lines := make([]string, 3000)
			for i := range lines {
				lines[i] = strings.Repeat("x", width)
			}

			dir := t.TempDir()
			body := strings.Join(lines, "\n") + "\n"
			if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(body), 0o600); err != nil {
				t.Fatalf("ファイルの作成に失敗: %v", err)
			}
			tb, err := newToolbox(dir, 10)
			if err != nil {
				t.Fatalf("toolbox の生成に失敗: %v", err)
			}

			got, err := tb.readFile(t.Context(), readFileArgs{Path: "long.txt"})
			if err != nil {
				t.Fatalf("readFile が error を返した: %v", err)
			}
			if !got.Truncated {
				t.Fatalf("上限超えなのに Truncated が false")
			}
			if got.From != 1 || got.To < 1 || got.To > len(lines) {
				t.Fatalf("範囲が不正です: from=%d to=%d total=%d", got.From, got.To, got.TotalLines)
			}

			// 申告した From..To が、返した内容にすべて完全な形で入っていること。
			want := strings.Join(lines[got.From-1:got.To], "\n")
			if !strings.HasPrefix(got.Content, want) {
				t.Errorf("to=%d と申告した行が返り切っていません（内容 %d バイト、申告分 %d バイト）",
					got.To, len(got.Content), len(want))
			}
		})
	}
}

// 1 行が上限を超える場合は、その行を返したことにして先へ進ませること。
// 範囲を空にすると、同じ行を読み直すだけのやり取りが呼び出し回数ぶん繰り返されます。
func TestReadFileSingleLineLongerThanLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "minified.js"), []byte(strings.Repeat("x", maxFileBytes+1024)), 0o600); err != nil {
		t.Fatalf("ファイルの作成に失敗: %v", err)
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("toolbox の生成に失敗: %v", err)
	}

	got, err := tb.readFile(t.Context(), readFileArgs{Path: "minified.js"})
	if err != nil {
		t.Fatalf("readFile が error を返した: %v", err)
	}
	if !got.Truncated {
		t.Fatal("上限超えなのに Truncated が false")
	}
	if got.From != 1 || got.To != 1 {
		t.Errorf("from=%d to=%d, want どちらも 1（進めなくなります）", got.From, got.To)
	}
}

// 一覧は件数だけでなく総量でも打ち切ること。深く入れ子になったパスは 1 本で数 KiB
// あり、件数の上限だけでは数 MB のツール結果になり得ます。
func TestListFilesCapsTotalBytes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	deep := filepath.Join(strings.Repeat("d", 100), strings.Repeat("e", 100))
	if err := os.MkdirAll(filepath.Join(dir, deep), 0o750); err != nil {
		t.Fatalf("ディレクトリの作成に失敗: %v", err)
	}
	// 1 本およそ 210 バイト。件数の上限（500）より先に総量の上限へ当たる本数を置きます。
	for i := range maxListEntries {
		name := filepath.Join(dir, deep, fmt.Sprintf("f%03d.md", i))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatalf("ファイルの作成に失敗: %v", err)
		}
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("toolbox の生成に失敗: %v", err)
	}

	got, err := tb.listFiles(t.Context(), listFilesArgs{})
	if err != nil {
		t.Fatalf("listFiles が error を返した: %v", err)
	}
	if !got.Truncated {
		t.Fatal("上限超えなのに Truncated が false")
	}
	if len(got.Files) >= maxListEntries {
		t.Errorf("件数の上限で止まっています（%d 件）。総量の上限が効いていません", len(got.Files))
	}

	total := 0
	for _, f := range got.Files {
		total += len(f)
	}
	if total > maxListResultBytes {
		t.Errorf("総量 = %d バイト, 上限 %d バイト", total, maxListResultBytes)
	}
}
