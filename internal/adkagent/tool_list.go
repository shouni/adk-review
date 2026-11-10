package adkagent

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"

	"google.golang.org/adk/v2/agent"
)

// The list_files tool. It returns the files under the working directory.

type listFilesArgs struct {
	Dir string `json:"dir,omitempty"`
}

type listFilesResult struct {
	Files     []string `json:"files,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func (t *toolbox) listFilesTool(toolCtx agent.Context, args listFilesArgs) (listFilesResult, error) {
	return t.listFiles(toolCtx, args)
}

func (t *toolbox) listFiles(toolCtx context.Context, args listFilesArgs) (listFilesResult, error) {
	if !t.spend() {
		return listFilesResult{Error: budgetExhaustedMsg}, nil
	}

	dir := args.Dir
	if dir == "" {
		dir = "."
	}
	t.trace(toolCtx, "list_files", "dir", dir)

	base, err := t.resolve(dir)
	if err != nil {
		slog.WarnContext(toolCtx, "tool could not resolve the path", "tool", "list_files", "dir", dir, "error", err)
		return listFilesResult{Error: err.Error()}, nil
	}

	var files []string
	resultBytes := 0
	truncated := false
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Cancellation is checked per entry. Otherwise, even after the whole review's deadline
		// passes, it would not return until the walk finishes.
		if err := toolCtx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// Only regular files are listed. read_file rejects symlinks pointing outside the root,
		// so listing them would show the model "files that should be readable but are not".
		if !d.Type().IsRegular() {
			return nil
		}
		if len(files) >= maxListEntries {
			truncated = true
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(t.root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		// The total size may cut the listing before the count limit is reached (maxListResultBytes above).
		if resultBytes+len(name) > maxListResultBytes {
			truncated = true
			return filepath.SkipAll
		}
		resultBytes += len(name)
		files = append(files, name)
		return nil
	})
	if err != nil {
		if cerr := cancelErr(toolCtx, "list_files"); cerr != nil {
			return listFilesResult{}, cerr
		}
		return listFilesResult{Error: fmt.Sprintf("一覧の取得に失敗しました: %v", err)}, nil
	}
	return listFilesResult{Files: files, Truncated: truncated}, nil
}
