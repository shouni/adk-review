package adkagent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
)

// The search_text tool. It searches across the text files under the working directory.

type searchTextArgs struct {
	Query string `json:"query"`
	// Context is the number of lines added before and after a hit (0 to maxSearchContext, default 0).
	//
	// The default is 0 because when a broad search hits the total size limit first, the added
	// context costs hits themselves. It is meant only for searches known to need more than one line.
	Context int `json:"context,omitempty"`
}

type searchTextResult struct {
	// Each element of Hits has the form "path:line: line content".
	Hits      []string `json:"hits,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func (t *toolbox) searchTextTool(toolCtx agent.Context, args searchTextArgs) (searchTextResult, error) {
	return t.searchText(toolCtx, args)
}

func (t *toolbox) searchText(toolCtx context.Context, args searchTextArgs) (searchTextResult, error) {
	if !t.spend() {
		return searchTextResult{Error: budgetExhaustedMsg}, nil
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return searchTextResult{Error: "query が空です"}, nil
	}
	if len(query) > maxQueryBytes {
		return searchTextResult{Error: fmt.Sprintf("query が長すぎます（%d バイト、上限 %d バイト）", len(query), maxQueryBytes)}, nil
	}

	ctxLines := min(max(args.Context, 0), maxSearchContext)
	t.trace(toolCtx, "search_text", "query", query, "context", ctxLines)

	run := searchRun{root: t.root, lowered: strings.ToLower(query), ctxLines: ctxLines}
	if err := filepath.WalkDir(t.root, run.walkFunc(toolCtx)); err != nil {
		if cerr := cancelErr(toolCtx, "search_text"); cerr != nil {
			return searchTextResult{}, cerr
		}
		return searchTextResult{Error: fmt.Sprintf("検索に失敗しました: %v", err)}, nil
	}
	return searchTextResult{Hits: run.hits, Truncated: run.truncated}, nil
}

// searchRun is the state accumulated during one search_text.
//
// The hit count and the returned size each have a single limit for the whole search, so they
// must carry across files. Confined to the WalkDir callback, one function would manage the state,
// walk the directory and cut out lines at once, so the state is a type and the walk is separate.
type searchRun struct {
	root     string
	lowered  string // The lowercased search term. Matching is case-insensitive.
	ctxLines int

	hits        []string
	resultBytes int
	matches     int
	scanned     int
	truncated   bool
}

// walkFunc returns the WalkDir callback that walks the root and accumulates hits.
//
// It only applies per-file cut-offs (type, size, encoding, files walked); scanning the
// content belongs to scanFile.
func (s *searchRun) walkFunc(ctx context.Context) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Cancellation is checked per file. A file is capped at maxSearchByteLen, so one callback
		// never overshoots the deadline by much.
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// ★ Nothing but regular files (no symlinks, FIFOs or devices) is opened. os.ReadFile
		// follows symlinks, so letting them through would put the content of files outside the
		// root into the model's input. d.Info() describes the link itself, so the size check
		// below would pass it too (read_file is protected by resolve, but this opens WalkDir's
		// paths directly).
		if !d.Type().IsRegular() {
			return nil
		}
		// The number of files walked also cuts it off. With no hits at all the hit limit never
		// applies, and a review's tool budget would be spent on repeated full walks of the repository.
		s.scanned++
		if s.scanned > maxSearchFiles {
			s.truncated = true
			return filepath.SkipAll
		}

		info, err := d.Info()
		if err != nil || info.Size() > maxSearchByteLen {
			return nil
		}
		// path is a regular file's path that WalkDir passes from under s.root.
		content, err := os.ReadFile(path) //nolint:gosec // the walk is limited to regular files under the root
		if err != nil || !utf8.Valid(content) {
			return nil
		}

		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		return s.scanFile(filepath.ToSlash(rel), string(content))
	}
}

// scanFile scans one file's content and accumulates hits and (if requested) their surrounding lines.
//
// It returns filepath.SkipAll at a limit because reading further could not be returned anyway.
func (s *searchRun) scanFile(relPath, content string) error {
	// emit adds one line to the result. It returns false when it cannot.
	//
	// Lines already added are skipped. Close hits have overlapping context, and without
	// deduplication the same line repeats and wastes the total size limit.
	lastEmitted := 0
	emit := func(no int, line string) bool {
		if no <= lastEmitted {
			return true
		}
		hit, cut := formatHit(relPath, no, line)
		if cut {
			s.truncated = true
		}
		// On hitting the total size limit, stop without adding that line.
		if s.resultBytes+len(hit) > maxSearchResultBytes {
			s.truncated = true
			return false
		}
		s.resultBytes += len(hit)
		s.hits = append(s.hits, hit)
		lastEmitted = no
		return true
	}

	// strings.Lines yields lines as it goes, so even a large file never allocates a slice of
	// every line at once. The context only needs the last few lines carried along (no array
	// of all lines is needed).
	var before []bufferedLine
	remainingAfter := 0
	lineNo := 0

	for raw := range strings.Lines(content) {
		lineNo++
		// Only the newline is dropped. TrimSpace would remove the indentation, and the model could
		// no longer read how lines relate (nesting depth, bullet list levels).
		line := strings.TrimRight(raw, "\r\n")

		switch {
		case strings.Contains(strings.ToLower(line), s.lowered):
			if s.matches >= maxSearchHits {
				s.truncated = true
				return filepath.SkipAll
			}
			s.matches++
			for _, b := range before {
				if !emit(b.no, b.text) {
					return filepath.SkipAll
				}
			}
			before = before[:0]
			if !emit(lineNo, line) {
				return filepath.SkipAll
			}
			remainingAfter = s.ctxLines

		case remainingAfter > 0:
			if !emit(lineNo, line) {
				return filepath.SkipAll
			}
			remainingAfter--

		case s.ctxLines > 0:
			if len(before) == s.ctxLines {
				before = before[1:]
			}
			before = append(before, bufferedLine{no: lineNo, text: line})
		}
	}
	return nil
}

// bufferedLine is one line carried along ahead of a hit.
type bufferedLine struct {
	no   int
	text string
}

// formatHit builds a hit of the form "path:line: line content" and reports whether the line was truncated.
// Truncation happens at a character boundary, with an ellipsis at the end to show the cut.
func formatHit(rel string, lineNo int, line string) (string, bool) {
	if len(line) <= maxHitBytes {
		return fmt.Sprintf("%s:%d: %s", rel, lineNo, line), false
	}
	return fmt.Sprintf("%s:%d: %s…", rel, lineNo, line[:truncateAtRune(line, maxHitBytes)]), true
}
