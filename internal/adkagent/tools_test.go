package adkagent

import (
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

	got, err := tb.readFile(nil, readFileArgs{Path: "chapter1.md"})
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
		got, err := tb.readFile(nil, readFileArgs{Path: path})
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

	got, err := tb.readFile(nil, readFileArgs{Path: "link.txt"})
	if err != nil {
		t.Fatalf("readFile がエラーを返しました: %v", err)
	}
	if got.Error == "" {
		t.Error("symlink 経由の脱出が拒否されていません")
	}
}

func TestBudgetExhaustion(t *testing.T) {
	tb := newTestToolbox(t, 2)

	if got, _ := tb.readFile(nil, readFileArgs{Path: "chapter1.md"}); got.Error != "" {
		t.Fatalf("1 回目が失敗しました: %s", got.Error)
	}
	if got, _ := tb.listFiles(nil, listFilesArgs{}); got.Error != "" {
		t.Fatalf("2 回目が失敗しました: %s", got.Error)
	}

	// 3 回目以降は、どのツールでも打ち切りのメッセージが返ります。
	got, _ := tb.searchText(nil, searchTextArgs{Query: "アキラ"})
	if !strings.Contains(got.Error, "上限") {
		t.Errorf("上限メッセージが返っていません: %q", got.Error)
	}
}

func TestListFiles(t *testing.T) {
	tb := newTestToolbox(t, 10)

	got, err := tb.listFiles(nil, listFilesArgs{})
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

	got, err := tb.searchText(nil, searchTextArgs{Query: "魔法"})
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

			got, err := tb.readFile(nil, readFileArgs{Path: "long.md"})
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
	got, err := tb.readFile(nil, readFileArgs{Path: "chapter1.md"})
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
