package adkagent

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"

	"google.golang.org/adk/v2/agent"
)

// list_files ツールです。作業ディレクトリ配下のファイル一覧を返します。

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
		slog.WarnContext(toolCtx, "ツールがパスを解決できませんでした", "tool", "list_files", "dir", dir, "error", err)
		return listFilesResult{Error: err.Error()}, nil
	}

	var files []string
	resultBytes := 0
	truncated := false
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// キャンセルはエントリ単位で見ます。見ないと、レビュー全体の締切が過ぎたあとも
		// 走査が終わるまで戻りません。
		if err := toolCtx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// 通常ファイル以外は並べません。read_file は root 外を指す symlink を拒否するので、
		// 一覧に出すと「読めるはずなのに読めないファイル」をモデルに見せることになります。
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
		// 件数の上限に当たる前に総量で打ち切ることがあります（上の maxListResultBytes）。
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
