package adkagent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
)

// The read_file tool. It reads one file under the working directory, optionally a line range.

type readFileArgs struct {
	Path string `json:"path"`
	// From is the line number to start at (1-based). 0 or negative means from the top.
	From int `json:"from,omitempty"`
	// Lines is the number of lines to read. 0 or less means from From to the end.
	Lines int `json:"lines,omitempty"`
}

type readFileResult struct {
	Content string `json:"content,omitempty"`
	// From / To are the range of lines actually returned (1-based, To inclusive).
	From int `json:"from,omitempty"`
	To   int `json:"to,omitempty"`
	// TotalLines is the line count of the whole file; it tells whether more follows.
	// Without it, a model reading a range cannot judge whether it has seen everything.
	TotalLines int `json:"total_lines,omitempty"`
	// Truncated means the end was dropped because the return limit was reached.
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// readFileTool is the handler handed to ADK. functiontool requires an agent.Context argument,
// but the tool body uses only its context.Context side (cancellation and logging). Taking a
// plain context.Context in the body lets tests call it without implementing agent.Context.
// list_files / search_text have the same shape.
func (t *toolbox) readFileTool(toolCtx agent.Context, args readFileArgs) (readFileResult, error) {
	return t.readFile(toolCtx, args)
}

func (t *toolbox) readFile(toolCtx context.Context, args readFileArgs) (readFileResult, error) {
	if !t.spend() {
		return readFileResult{Error: budgetExhaustedMsg}, nil
	}
	// ★ Log the range too. Without it there is no checking afterwards whether the model uses
	// ranges. When a limit or a tool description changes, the rise or fall of prompt_tokens
	// alone does not reach the "why" of whether it worked.
	t.trace(toolCtx, "read_file", "path", args.Path, "from", args.From, "lines", args.Lines)

	path, err := t.resolve(args.Path)
	if err != nil {
		slog.WarnContext(toolCtx, "tool could not resolve the path", "tool", "read_file", "path", args.Path, "error", err)
		return readFileResult{Error: err.Error()}, nil
	}

	// resolve has already confined path under the root (escapes through symlinks are rejected
	// by resolving the real path). The path has to vary here, or there is no tool.
	content, err := os.ReadFile(path) //nolint:gosec // resolve has confined it to the workspace
	if err != nil {
		return readFileResult{Error: fmt.Sprintf("読み込みに失敗しました: %v", err)}, nil
	}

	// Whether it is text is judged on the content as read. Truncating first would split a
	// multi-byte character such as Japanese and misjudge it as "not text" (with 3-byte
	// characters, only 1 in 3 cut points lands on a character boundary).
	if !utf8.Valid(content) {
		return readFileResult{Error: "テキストファイルではありません"}, nil
	}

	body, from, to, total := selectLines(string(content), args.From, args.Lines)

	truncated := false
	if len(body) > maxFileBytes {
		body = body[:truncateAtRune(body, maxFileBytes)]
		truncated = true
		// What was dropped also shrinks the end of the returned range. Without fixing it here,
		// the model moves on believing it read lines it never got.
		//
		// The number of newlines is the number of lines fully returned. If the end is cut
		// mid-line, that line does not count; if it is cut right after a newline, not one byte
		// of the next line is included. Adding either to `to` makes the model read on from
		// beyond it, silently skipping lines it was never given.
		if complete := strings.Count(body, "\n"); complete > 0 {
			to = from + complete - 1
		} else {
			// A single line exceeds maxFileBytes. It cannot be returned whole, so the line counts
			// as returned and the model moves on. An empty range would repeat the exchange of
			// re-reading the same line for every remaining call.
			to = from
		}
	}
	return readFileResult{
		Content:    body,
		From:       from,
		To:         to,
		TotalLines: total,
		Truncated:  truncated,
	}, nil
}

// selectLines cuts out a 1-based line range and returns the range and the total line count.
//
// Without a range it returns everything (as before). A range out of bounds returns empty with
// line numbers 0. It is not an error, so the caller can look at the total and try again.
func selectLines(text string, from, count int) (body string, first, last, total int) {
	lines := strings.Split(text, "\n")
	// The empty element produced by a trailing newline is not counted as a line.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total = len(lines)

	if from <= 0 {
		from = 1
	}
	if from > total {
		return "", 0, 0, total
	}

	end := total
	// from+count can overflow, so compare against the remaining line count. Both values come
	// from JSON the model writes, so values near the int limit do arrive (adding first would
	// wrap negative, put end below from and panic on the slice).
	if remaining := total - from + 1; count > 0 && count < remaining {
		end = from + count - 1
	}
	return strings.Join(lines[from-1:end], "\n"), from, end, total
}

// truncateAtRune returns the last character boundary not beyond limit.
// content is assumed to have passed utf8.Valid.
//
// It is generic because it is called with both []byte (read_file's content) and string
// (search_text's hit lines). Both are byte sequences, and only indexing is used.
func truncateAtRune[T ~[]byte | ~string](content T, limit int) int {
	if len(content) <= limit {
		return len(content)
	}
	// If limit falls inside a character, step back to its first byte.
	// UTF-8 continuation bytes are 0b10xxxxxx, so at most 3 steps back reach a boundary.
	for limit > 0 && content[limit]&0xC0 == 0x80 {
		limit--
	}
	return limit
}
