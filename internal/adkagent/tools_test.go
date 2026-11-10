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

// newTestWorkspace creates a working directory laid out with test files.
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
			t.Fatalf("failed to create the directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("failed to create the file: %v", err)
		}
	}
	return dir
}

func newTestToolbox(t *testing.T, budget int64) *toolbox {
	t.Helper()

	tb, err := newToolbox(newTestWorkspace(t), budget)
	if err != nil {
		t.Fatalf("failed to create the toolbox: %v", err)
	}
	return tb
}

func TestReadFile(t *testing.T) {
	tb := newTestToolbox(t, 10)

	got, err := tb.readFile(t.Context(), readFileArgs{Path: "chapter1.md"})
	if err != nil {
		t.Fatalf("readFile returned an error: %v", err)
	}
	if got.Error != "" {
		t.Fatalf("Error is set: %s", got.Error)
	}
	if !strings.Contains(got.Content, "アキラ") {
		t.Errorf("content not read: %q", got.Content)
	}
}

func TestReadFileRejectsEscape(t *testing.T) {
	tb := newTestToolbox(t, 10)

	for _, path := range []string{"../secret.txt", "/etc/passwd", "notes/../../secret.txt", ""} {
		got, err := tb.readFile(t.Context(), readFileArgs{Path: path})
		if err != nil {
			t.Fatalf("readFile returned an error: %v", err)
		}
		if got.Error == "" {
			t.Errorf("path %q is not rejected", path)
		}
	}
}

func TestReadFileRejectsSymlinkEscape(t *testing.T) {
	tb := newTestToolbox(t, 10)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("failed to create the outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(tb.root, "link.txt")); err != nil {
		t.Skipf("skipping: symlinks cannot be created here: %v", err)
	}

	got, err := tb.readFile(t.Context(), readFileArgs{Path: "link.txt"})
	if err != nil {
		t.Fatalf("readFile returned an error: %v", err)
	}
	if got.Error == "" {
		t.Error("an escape through a symlink is not rejected")
	}
}

func TestBudgetExhaustion(t *testing.T) {
	tb := newTestToolbox(t, 2)

	if got, _ := tb.readFile(t.Context(), readFileArgs{Path: "chapter1.md"}); got.Error != "" {
		t.Fatalf("the first call failed: %s", got.Error)
	}
	if got, _ := tb.listFiles(t.Context(), listFilesArgs{}); got.Error != "" {
		t.Fatalf("the second call failed: %s", got.Error)
	}

	// From the third call on, every tool returns the cut-off message.
	got, _ := tb.searchText(t.Context(), searchTextArgs{Query: "アキラ"})
	if !strings.Contains(got.Error, "上限") {
		t.Errorf("the limit message was not returned: %q", got.Error)
	}
}

func TestListFiles(t *testing.T) {
	tb := newTestToolbox(t, 10)

	got, err := tb.listFiles(t.Context(), listFilesArgs{})
	if err != nil {
		t.Fatalf("listFiles returned an error: %v", err)
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
			t.Errorf("%s is not listed: %v", name, got.Files)
		}
	}
}

func TestSearchText(t *testing.T) {
	tb := newTestToolbox(t, 10)

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "魔法"})
	if err != nil {
		t.Fatalf("searchText returned an error: %v", err)
	}
	if len(got.Hits) != 2 {
		t.Fatalf("Hits = %v, want 2", got.Hits)
	}
	for _, hit := range got.Hits {
		if !strings.Contains(hit, ":") {
			t.Errorf("not in path:line form: %q", hit)
		}
	}
}

// A Japanese file over the limit is read, not rejected as binary.
//
// If the order went back to truncating before utf8.Valid, a 3-byte character split at the
// boundary would give "テキストファイルではありません" (not a text file) with a 2/3 chance. Long
// Japanese manuscripts are the main subject of the novel / article modes, so it would break
// only on the most used path.
func TestReadFileTruncatesAtRuneBoundary(t *testing.T) {
	t.Parallel()

	for pad := range 3 {
		t.Run(fmt.Sprintf("shift the boundary by %d bytes", pad), func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			content := strings.Repeat("x", pad) + strings.Repeat("あ", maxFileBytes)
			if err := os.WriteFile(filepath.Join(dir, "long.md"), []byte(content), 0o600); err != nil {
				t.Fatalf("failed to create the file: %v", err)
			}
			tb, err := newToolbox(dir, 10)
			if err != nil {
				t.Fatalf("failed to create the toolbox: %v", err)
			}

			got, err := tb.readFile(t.Context(), readFileArgs{Path: "long.md"})
			if err != nil {
				t.Fatalf("readFile returned an error: %v", err)
			}
			if got.Error != "" {
				t.Fatalf("a valid Japanese file was rejected: %s", got.Error)
			}
			if !got.Truncated {
				t.Error("Truncated is false although over the limit")
			}
			if !utf8.ValidString(got.Content) {
				t.Error("the truncated content is broken UTF-8")
			}
			if len(got.Content) > maxFileBytes {
				t.Errorf("returns more than the limit: %d > %d", len(got.Content), maxFileBytes)
			}
		})
	}
}

// A file within the limit is not truncated.
func TestReadFileKeepsShortFileIntact(t *testing.T) {
	t.Parallel()

	tb := newTestToolbox(t, 10)
	got, err := tb.readFile(t.Context(), readFileArgs{Path: "chapter1.md"})
	if err != nil {
		t.Fatalf("readFile returned an error: %v", err)
	}
	if got.Truncated {
		t.Error("Truncated is true for a short file")
	}
	if !strings.Contains(got.Content, "アキラ") {
		t.Errorf("content not read: %q", got.Content)
	}
}

// ★ Search must not follow a symlink and read outside the working directory.
//
// read_file is protected by resolve, but search_text opens the paths WalkDir returns directly,
// so it is a separate entrance. WalkDir does not recurse into symlinked directories, but a
// symlink that appears as a file arrives looking just like a regular file. DirEntry.Info()
// describes the link itself (a few dozen bytes), so the size cut-off does not apply either.
// With a planted symlink to a private key on the host or /proc/self/environ, lines matching the
// search term would go straight into the model's input.
func TestSearchTextRejectsSymlinkEscape(t *testing.T) {
	tb := newTestToolbox(t, 10)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("アキラ-この行は作業ディレクトリの外にある\n"), 0o600); err != nil {
		t.Fatalf("failed to create the outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(tb.root, "link.txt")); err != nil {
		t.Skipf("skipping: symlinks cannot be created here: %v", err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "アキラ"})
	if err != nil {
		t.Fatalf("searchText returned an error: %v", err)
	}
	for _, hit := range got.Hits {
		if strings.Contains(hit, "作業ディレクトリの外") {
			t.Errorf("an outside file's content is returned through a symlink: %q", hit)
		}
		if strings.HasPrefix(hit, "link.txt:") {
			t.Errorf("a symlink is searched: %q", hit)
		}
	}
}

// Symlinks are not listed.
//
// Listing one leaks no content, but it shows the model a path read_file rejects as a "readable
// file" and invites wasted tool calls.
func TestListFilesSkipsSymlink(t *testing.T) {
	tb := newTestToolbox(t, 10)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("failed to create the outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(tb.root, "link.txt")); err != nil {
		t.Skipf("skipping: symlinks cannot be created here: %v", err)
	}

	got, err := tb.listFiles(t.Context(), listFilesArgs{})
	if err != nil {
		t.Fatalf("listFiles returned an error: %v", err)
	}
	for _, name := range got.Files {
		if name == "link.txt" {
			t.Errorf("a symlink is listed: %v", got.Files)
		}
	}
}

// A long line is cut at the per-hit limit without breaking a character boundary.
//
// The hit count limit alone does not bound the total (a minified 1 MiB line makes a 1 MiB hit).
func TestSearchTextTruncatesLongHit(t *testing.T) {
	dir := t.TempDir()
	// One long line of 3-byte characters only. The limit falls inside a character.
	line := strings.Repeat("あ", maxHitBytes) + "魔法\n"
	if err := os.WriteFile(filepath.Join(dir, "long.md"), []byte(line), 0o600); err != nil {
		t.Fatalf("failed to create the file: %v", err)
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("failed to create the toolbox: %v", err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "魔法"})
	if err != nil {
		t.Fatalf("searchText returned an error: %v", err)
	}
	if len(got.Hits) != 1 {
		t.Fatalf("Hits = %d, want 1", len(got.Hits))
	}
	if !got.Truncated {
		t.Error("Truncated is false although it was cut")
	}
	if !utf8.ValidString(got.Hits[0]) {
		t.Error("the truncated hit is broken UTF-8")
	}
	// "path:line: " and the ellipsis go over the limit. The line itself only has to be within it.
	if len(got.Hits[0]) > maxHitBytes+64 {
		t.Errorf("a hit is too long: %d bytes", len(got.Hits[0]))
	}
}

// The total returned bytes are limited.
func TestSearchTextCapsTotalBytes(t *testing.T) {
	dir := t.TempDir()
	// One hit per file, each cut to the per-hit limit.
	// Enough files are placed to hit the total limit.
	files := maxSearchResultBytes/maxHitBytes + 10
	line := strings.Repeat("x", maxHitBytes*2) + "魔法\n"
	for i := range files {
		name := filepath.Join(dir, fmt.Sprintf("file%03d.md", i))
		if err := os.WriteFile(name, []byte(line), 0o600); err != nil {
			t.Fatalf("failed to create the file: %v", err)
		}
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("failed to create the toolbox: %v", err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "魔法"})
	if err != nil {
		t.Fatalf("searchText returned an error: %v", err)
	}
	if !got.Truncated {
		t.Error("Truncated is false although the total limit was hit")
	}
	total := 0
	for _, hit := range got.Hits {
		total += len(hit)
	}
	if total > maxSearchResultBytes {
		t.Errorf("the total exceeds the limit: %d > %d", total, maxSearchResultBytes)
	}
}

// A hit line keeps its indentation.
//
// With TrimSpace, nesting depth and bullet levels become invisible to the model.
func TestSearchTextKeepsIndent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("    - アキラの設定\n"), 0o600); err != nil {
		t.Fatalf("failed to create the file: %v", err)
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("failed to create the toolbox: %v", err)
	}

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: "アキラ"})
	if err != nil {
		t.Fatalf("searchText returned an error: %v", err)
	}
	if len(got.Hits) != 1 || !strings.HasSuffix(got.Hits[0], "    - アキラの設定") {
		t.Errorf("the indentation was dropped: %q", got.Hits)
	}
}

// An overlong query is rejected without walking.
func TestSearchTextRejectsLongQuery(t *testing.T) {
	tb := newTestToolbox(t, 10)

	got, err := tb.searchText(t.Context(), searchTextArgs{Query: strings.Repeat("a", maxQueryBytes+1)})
	if err != nil {
		t.Fatalf("searchText returned an error: %v", err)
	}
	if got.Error == "" {
		t.Error("an overlong query is not rejected")
	}
}

// With a cancelled context, nothing is walked and a Go error is returned rather than the Error field.
//
// In the Error field, a cut-off cannot be told from "searched and found nothing", and the model
// writes its final review believing it investigated.
func TestToolsStopOnCanceledContext(t *testing.T) {
	tb := newTestToolbox(t, 10)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := tb.listFiles(ctx, listFilesArgs{}); err == nil {
		t.Error("list_files ignores cancellation")
	}
	if _, err := tb.searchText(ctx, searchTextArgs{Query: "アキラ"}); err == nil {
		t.Error("search_text ignores cancellation")
	}
}

// ★ A range can be read.
//
// This exists to avoid reading whole files. What is read stays in every later exchange and is
// re-read each time, so one waste keeps costing until the run ends.
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
		{"no range reads everything", 0, 0, "l1\nl2\nl3\nl4\nl5", 1, 5},
		{"a few lines from the middle", 2, 2, "l2\nl3", 2, 3},
		{"omitted lines read to the end", 4, 0, "l4\nl5", 4, 5},
		{"lines beyond the end are clamped", 4, 99, "l4\nl5", 4, 5},
		{"from at or below 0 is the top", -3, 1, "l1", 1, 1},
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
				t.Errorf("range = %d-%d, want %d-%d", got.From, got.To, tt.wantFrom, tt.wantTo)
			}
			// Unable to tell whether more follows, a model reading a range assumes it read everything.
			if got.TotalLines != 5 {
				t.Errorf("TotalLines = %d, want 5", got.TotalLines)
			}
		})
	}
}

// A range past the end of the file is not an error; only the total line count is returned.
// Knowing the total, the model can correct the range itself.
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
		t.Errorf("out of range is an error: %q", got.Error)
	}
	if got.Content != "" || got.TotalLines != 2 {
		t.Errorf("Content = %q, TotalLines = %d, want \"\" / 2", got.Content, got.TotalLines)
	}
}

// Lines around a hit can be added. This saves the round trip of reopening with read_file.
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

// Even when close hits' contexts overlap, no line appears twice.
// Without deduplication, duplicates eat the total limit.
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

// Without context, only the hit lines are returned, as before.
// Adding context to a broad search costs hits themselves at the total limit.
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

// from and lines come from JSON the model writes, so values near the int limit can arrive.
// When they were added before comparing, the overflow put end below from and the slice panicked.
func TestReadFileHandlesOutOfRangeArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args readFileArgs
	}{
		{"huge lines", readFileArgs{Path: "chapter1.md", From: 2, Lines: math.MaxInt}},
		{"huge from and lines", readFileArgs{Path: "chapter1.md", From: math.MaxInt, Lines: math.MaxInt}},
		{"negative lines", readFileArgs{Path: "chapter1.md", From: 1, Lines: math.MinInt}},
		{"negative from", readFileArgs{Path: "chapter1.md", From: math.MinInt, Lines: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := newTestToolbox(t, 10).readFile(t.Context(), tt.args)
			if err != nil {
				t.Fatalf("readFile returned an error: %v", err)
			}
			if got.To < got.From {
				t.Errorf("the range is inverted: from=%d to=%d", got.From, got.To)
			}
		})
	}
}

// When cut off, To is "the last line returned in full".
//
// Claiming even one line too many makes the model read on from beyond it, silently skipping a
// line it was never given. Both a cut inside a line and a cut right after a newline are checked
// (in the latter, not one byte of the claimed line is included).
func TestReadFileTruncationReportsOnlyCompleteLines(t *testing.T) {
	t.Parallel()

	for _, width := range []int{99, 98} {
		t.Run(fmt.Sprintf("line length %d bytes", width), func(t *testing.T) {
			t.Parallel()

			lines := make([]string, 3000)
			for i := range lines {
				lines[i] = strings.Repeat("x", width)
			}

			dir := t.TempDir()
			body := strings.Join(lines, "\n") + "\n"
			if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(body), 0o600); err != nil {
				t.Fatalf("failed to create the file: %v", err)
			}
			tb, err := newToolbox(dir, 10)
			if err != nil {
				t.Fatalf("failed to create the toolbox: %v", err)
			}

			got, err := tb.readFile(t.Context(), readFileArgs{Path: "long.txt"})
			if err != nil {
				t.Fatalf("readFile returned an error: %v", err)
			}
			if !got.Truncated {
				t.Fatalf("Truncated is false although over the limit")
			}
			if got.From != 1 || got.To < 1 || got.To > len(lines) {
				t.Fatalf("invalid range: from=%d to=%d total=%d", got.From, got.To, got.TotalLines)
			}

			// The claimed From..To must all be in the returned content, complete.
			want := strings.Join(lines[got.From-1:got.To], "\n")
			if !strings.HasPrefix(got.Content, want) {
				t.Errorf("the lines claimed up to to=%d were not fully returned (content %d bytes, claimed %d bytes)",
					got.To, len(got.Content), len(want))
			}
		})
	}
}

// When one line exceeds the limit, count it as returned and move on.
// An empty range would repeat the exchange of re-reading the same line for every remaining call.
func TestReadFileSingleLineLongerThanLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "minified.js"), []byte(strings.Repeat("x", maxFileBytes+1024)), 0o600); err != nil {
		t.Fatalf("failed to create the file: %v", err)
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("failed to create the toolbox: %v", err)
	}

	got, err := tb.readFile(t.Context(), readFileArgs{Path: "minified.js"})
	if err != nil {
		t.Fatalf("readFile returned an error: %v", err)
	}
	if !got.Truncated {
		t.Fatal("Truncated is false although over the limit")
	}
	if got.From != 1 || got.To != 1 {
		t.Errorf("from=%d to=%d, want both 1 (otherwise it cannot move on)", got.From, got.To)
	}
}

// The listing is cut by total size as well as count. A deeply nested path can be several KiB,
// and the count limit alone can make a tool result of several MB.
func TestListFilesCapsTotalBytes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	deep := filepath.Join(strings.Repeat("d", 100), strings.Repeat("e", 100))
	if err := os.MkdirAll(filepath.Join(dir, deep), 0o750); err != nil {
		t.Fatalf("failed to create the directory: %v", err)
	}
	// About 210 bytes each. Enough files are placed to hit the total limit before the count limit (500).
	for i := range maxListEntries {
		name := filepath.Join(dir, deep, fmt.Sprintf("f%03d.md", i))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatalf("failed to create the file: %v", err)
		}
	}
	tb, err := newToolbox(dir, 10)
	if err != nil {
		t.Fatalf("failed to create the toolbox: %v", err)
	}

	got, err := tb.listFiles(t.Context(), listFilesArgs{})
	if err != nil {
		t.Fatalf("listFiles returned an error: %v", err)
	}
	if !got.Truncated {
		t.Fatal("Truncated is false although over the limit")
	}
	if len(got.Files) >= maxListEntries {
		t.Errorf("stopped at the count limit (%d); the total limit did not apply", len(got.Files))
	}

	total := 0
	for _, f := range got.Files {
		total += len(f)
	}
	if total > maxListResultBytes {
		t.Errorf("total = %d bytes, limit %d bytes", total, maxListResultBytes)
	}
}
