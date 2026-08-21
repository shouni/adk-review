package adkagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	for pad := 0; pad < 3; pad++ {
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
