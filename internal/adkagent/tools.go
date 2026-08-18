package adkagent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// ツールが返す量の上限です。モデルのコンテキストを一度の呼び出しで食い潰さないための
// 値で、超えた分は打ち切ってその旨を結果に含めます。
const (
	maxFileBytes     = 200 * 1024
	maxListEntries   = 500
	maxSearchHits    = 100
	maxSearchByteLen = 1 * 1024 * 1024 // これより大きいファイルは検索対象から外します
	maxSearchFiles   = 2000            // 1 回の検索で開くファイル数の上限
)

// toolbox は、1 回のレビュー実行に紐付くワークスペース参照ツール一式です。
//
// remaining は全ツール共通の残り呼び出し回数です。時間ではなく回数で打ち切るのは、
// レビュー全体の締切（Cloud Tasks の dispatch deadline）に対して、モデルの思考時間は
// 読めないがツール呼び出し回数は設計時に見積もれるためです。
type toolbox struct {
	root      string
	remaining atomic.Int64
}

// budgetExhaustedMsg は、回数を使い切ったことをモデルへ伝える文言です。
//
// ツールの失敗を Go の error として返すとフレームワークがエラーイベントとして扱い、
// モデルがリカバリー（別のパスを試す、諦めて最終回答へ進む）を選べません。失敗も
// 各ツールの Error フィールドに載せた正常な戻り値として渡し、判断はモデルに委ねます。
const budgetExhaustedMsg = "ツールの呼び出し回数上限に達しました。これ以上の調査はできません。ここまでに得た情報で最終レビューをまとめてください。"

// newTools は、root 配下だけを参照できるツール一式を組み立てます。
func newTools(root string, budget int64) ([]tool.Tool, error) {
	tb, err := newToolbox(root, budget)
	if err != nil {
		return nil, err
	}

	readFile, err := functiontool.New(functiontool.Config{
		Name: "read_file",
		Description: "作業ディレクトリ内のファイルを読みます。パスは作業ディレクトリからの相対パスで指定します。" +
			"差分に含まれないファイル（前後の章、関連コード）の確認に使ってください。",
	}, tb.readFile)
	if err != nil {
		return nil, fmt.Errorf("read_file の構築に失敗しました: %w", err)
	}

	listFiles, err := functiontool.New(functiontool.Config{
		Name: "list_files",
		Description: "作業ディレクトリ内のファイル一覧を返します。dir を省略するとルートから列挙します。" +
			"リポジトリの構成を把握するために最初に呼ぶことを推奨します。",
	}, tb.listFiles)
	if err != nil {
		return nil, fmt.Errorf("list_files の構築に失敗しました: %w", err)
	}

	searchText, err := functiontool.New(functiontool.Config{
		Name: "search_text",
		Description: "作業ディレクトリ内の全テキストファイルから文字列を検索し、ファイル名・行番号付きで返します。" +
			"登場人物名・用語・関数名などが他のどこに現れるかの確認に使ってください（大文字小文字は区別しません）。",
	}, tb.searchText)
	if err != nil {
		return nil, fmt.Errorf("search_text の構築に失敗しました: %w", err)
	}

	return []tool.Tool{readFile, listFiles, searchText}, nil
}

// newToolbox は、root を実体パスへ解決して toolbox を生成します。
//
// 構築時に一度だけ解決しておくのは、パス検査（resolve）とレスポンスの相対パス表示が
// 同じ基準を共有するためです。macOS の /var → /private/var のように、OS 側の symlink で
// 見かけのパスと実体が食い違う環境があります。
func newToolbox(root string, budget int64) (*toolbox, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("作業ディレクトリを解決できません (%s): %w", root, err)
	}

	tb := &toolbox{root: resolved}
	tb.remaining.Store(budget)
	return tb, nil
}

// spend は、呼び出し回数の残りを 1 消費します。使い切っていたら false を返します。
func (t *toolbox) spend() bool {
	return t.remaining.Add(-1) >= 0
}

// resolve は、相対パスを root 配下の絶対パスへ解決します。
//
// filepath.Clean だけでは symlink 経由の脱出を防げないため、実体パスまで解決して
// root 配下であることを確認します。レビュー対象のリポジトリは外部入力なので、
// リポジトリ内の symlink がホストの任意ファイルを指している可能性があります。
func (t *toolbox) resolve(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("パスが空です")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("絶対パスは指定できません: %s", rel)
	}

	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("作業ディレクトリの外は参照できません: %s", rel)
	}

	// t.root は構築時に実体解決済みなので、こちら側だけ解決すれば基準が揃います。
	joined := filepath.Join(t.root, clean)
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("パスを解決できません: %s", rel)
	}
	if resolved != t.root && !strings.HasPrefix(resolved, t.root+string(filepath.Separator)) {
		return "", fmt.Errorf("作業ディレクトリの外は参照できません: %s", rel)
	}
	return resolved, nil
}

// --- read_file ---

type readFileArgs struct {
	Path string `json:"path"`
}

type readFileResult struct {
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (t *toolbox) readFile(_ agent.Context, args readFileArgs) (readFileResult, error) {
	if !t.spend() {
		return readFileResult{Error: budgetExhaustedMsg}, nil
	}

	path, err := t.resolve(args.Path)
	if err != nil {
		return readFileResult{Error: err.Error()}, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return readFileResult{Error: fmt.Sprintf("読み込みに失敗しました: %v", err)}, nil
	}

	// テキストかどうかは読み込んだ内容そのもので判定します。先に切り詰めると、
	// 日本語のようなマルチバイト文字が途中で割れて「テキストではない」と誤判定されます
	// （3 バイト文字なら切り口が文字境界に当たるのは 1/3 だけです）。
	if !utf8.Valid(content) {
		return readFileResult{Error: "テキストファイルではありません"}, nil
	}

	truncated := false
	if len(content) > maxFileBytes {
		content = content[:truncateAtRune(content, maxFileBytes)]
		truncated = true
	}
	return readFileResult{Content: string(content), Truncated: truncated}, nil
}

// truncateAtRune は、limit を超えない範囲で最も後ろの文字境界を返します。
// content は utf8.Valid 済みであることを前提とします。
func truncateAtRune(content []byte, limit int) int {
	if len(content) <= limit {
		return len(content)
	}
	// limit の位置が文字の途中なら、その文字の先頭まで戻します。
	// UTF-8 の継続バイトは 0b10xxxxxx なので、最大 3 バイト戻れば境界に着きます。
	for limit > 0 && content[limit]&0xC0 == 0x80 {
		limit--
	}
	return limit
}

// --- list_files ---

type listFilesArgs struct {
	Dir string `json:"dir,omitempty"`
}

type listFilesResult struct {
	Files     []string `json:"files,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func (t *toolbox) listFiles(_ agent.Context, args listFilesArgs) (listFilesResult, error) {
	if !t.spend() {
		return listFilesResult{Error: budgetExhaustedMsg}, nil
	}

	dir := args.Dir
	if dir == "" {
		dir = "."
	}
	base, err := t.resolve(dir)
	if err != nil {
		return listFilesResult{Error: err.Error()}, nil
	}

	var files []string
	truncated := false
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
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
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return listFilesResult{Error: fmt.Sprintf("一覧の取得に失敗しました: %v", err)}, nil
	}
	return listFilesResult{Files: files, Truncated: truncated}, nil
}

// --- search_text ---

type searchTextArgs struct {
	Query string `json:"query"`
}

type searchTextResult struct {
	// Hits の各要素は "path:line: 行の内容" 形式です。
	Hits      []string `json:"hits,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func (t *toolbox) searchText(_ agent.Context, args searchTextArgs) (searchTextResult, error) {
	if !t.spend() {
		return searchTextResult{Error: budgetExhaustedMsg}, nil
	}
	if strings.TrimSpace(args.Query) == "" {
		return searchTextResult{Error: "query が空です"}, nil
	}

	query := strings.ToLower(args.Query)
	var hits []string
	truncated := false
	scanned := 0

	err := filepath.WalkDir(t.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// 走査したファイル数でも打ち切ります。ヒットが 1 件も無いとヒット上限が効かず、
		// 1 レビューぶんのツール予算をリポジトリの全走査に何度も使ってしまうためです。
		scanned++
		if scanned > maxSearchFiles {
			truncated = true
			return filepath.SkipAll
		}

		info, err := d.Info()
		if err != nil || info.Size() > maxSearchByteLen {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil || !utf8.Valid(content) {
			return nil
		}

		rel, err := filepath.Rel(t.root, path)
		if err != nil {
			return err
		}
		// strings.Lines は行を切り出しながら回すので、大きなファイルでも
		// 全行ぶんのスライスを一度に確保しません。
		lineNo := 0
		for line := range strings.Lines(string(content)) {
			lineNo++
			line = strings.TrimRight(line, "\n")
			if !strings.Contains(strings.ToLower(line), query) {
				continue
			}
			if len(hits) >= maxSearchHits {
				truncated = true
				return filepath.SkipAll
			}
			hits = append(hits, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), lineNo, strings.TrimSpace(line)))
		}
		return nil
	})
	if err != nil {
		return searchTextResult{Error: fmt.Sprintf("検索に失敗しました: %v", err)}, nil
	}
	return searchTextResult{Hits: hits, Truncated: truncated}, nil
}
