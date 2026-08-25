package adkagent

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
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

	// ヒット数だけでは返却量を抑えられません。minify 済みの 1 行が 1 MiB あれば、
	// 100 ヒットで 100 MiB のツール結果になり、モデルのコンテキストとレイテンシと
	// API 課金を一度に食い潰します。1 ヒットぶんと全体の両方にバイト上限を置きます。
	maxHitBytes          = 2 * 1024
	maxSearchResultBytes = 64 * 1024

	// query の長さの上限です。検索語を決めるのはモデルなので、リポジトリ内の文章に
	// 引きずられて異常に長い query を作ることがあり得ます。
	maxQueryBytes = 200

	// maxSearchContext は、1 ヒットに添えられる前後の行数の上限です。
	// 5 行を超えるなら read_file で範囲を指定したほうが安く済みます。
	maxSearchContext = 5
)

// toolbox は、1 回のレビュー実行に紐付くワークスペース参照ツール一式です。
//
// remaining は全ツール共通の残り呼び出し回数です。時間ではなく回数で打ち切るのは、
// レビュー全体の締切（Cloud Tasks の dispatch deadline）に対して、モデルの思考時間は
// 読めないがツール呼び出し回数は設計時に見積もれるためです。
type toolbox struct {
	root      string
	budget    int64
	remaining atomic.Int64
}

// budgetExhaustedMsg は、回数を使い切ったことをモデルへ伝える文言です。
//
// ツールの失敗を Go の error として返すとフレームワークがエラーイベントとして扱い、
// モデルがリカバリー（別のパスを試す、諦めて最終回答へ進む）を選べません。失敗も
// 各ツールの Error フィールドに載せた正常な戻り値として渡し、判断はモデルに委ねます。
const budgetExhaustedMsg = "ツールの呼び出し回数上限に達しました。これ以上の調査はできません。ここまでに得た情報で最終レビューをまとめてください。"

// newTools は、root 配下だけを参照できるツール一式を組み立てます。
//
// toolbox も返すのは、実行後に呼び出し回数を数えるためです（used）。上限が実測に対して
// 妥当かどうかは、使い切ったかどうかではなく毎回いくつ使ったかで判断します。
func newTools(root string, budget int64) (*toolbox, []tool.Tool, error) {
	tb, err := newToolbox(root, budget)
	if err != nil {
		return nil, nil, err
	}

	readFile, err := functiontool.New(functiontool.Config{
		Name: "read_file",
		Description: "作業ディレクトリ内のファイルを読みます。パスは作業ディレクトリからの相対パスで指定します。" +
			"差分に含まれないファイル（前後の章、関連コード）の確認に使ってください。" +
			"from（開始行、1 始まり）と lines（行数）で範囲を指定できます。" +
			"search_text で行番号が分かっているなら、必ず範囲を指定してください。" +
			"読んだ内容は以降のやり取りすべてに残り、そのたび読み直されます。" +
			"全文を読むのは、何行目を見ればよいか分からないときだけにしてください。" +
			"返り値の total_lines で続きの有無が分かります。",
	}, tb.readFileTool)
	if err != nil {
		return nil, nil, fmt.Errorf("read_file の構築に失敗しました: %w", err)
	}

	listFiles, err := functiontool.New(functiontool.Config{
		Name: "list_files",
		Description: "作業ディレクトリ内のファイル一覧を返します。dir を省略するとルートから列挙します。" +
			"リポジトリの構成を把握するために最初に呼ぶことを推奨します。",
	}, tb.listFilesTool)
	if err != nil {
		return nil, nil, fmt.Errorf("list_files の構築に失敗しました: %w", err)
	}

	searchText, err := functiontool.New(functiontool.Config{
		Name: "search_text",
		Description: "作業ディレクトリ内の全テキストファイルから文字列を検索し、ファイル名・行番号付きで返します。" +
			"登場人物名・用語・関数名などが他のどこに現れるかの確認に使ってください（大文字小文字は区別しません）。" +
			"context（0〜5）を指定すると各ヒットの前後の行も返します。" +
			"1 行では判断できないと分かっているときは、read_file で開き直すより context を指定するほうが安く済みます。",
	}, tb.searchTextTool)
	if err != nil {
		return nil, nil, fmt.Errorf("search_text の構築に失敗しました: %w", err)
	}

	return tb, []tool.Tool{readFile, listFiles, searchText}, nil
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

	tb := &toolbox{root: resolved, budget: budget}
	tb.remaining.Store(budget)
	return tb, nil
}

// used は、実際に呼ばれた回数を返します。
//
// remaining は使い切ったあとも減り続けるため（spend は減らしてから判定します）、
// 上限で頭打ちにします。使い切った実行を「上限より多く呼んだ」と記録しないためです。
func (t *toolbox) used() int {
	used := t.budget - t.remaining.Load()
	if used > t.budget {
		return int(t.budget)
	}
	if used < 0 {
		return 0
	}
	return int(used)
}

// spend は、呼び出し回数の残りを 1 消費します。使い切っていたら false を返します。
func (t *toolbox) spend() bool {
	return t.remaining.Add(-1) >= 0
}

// trace は、ツールの呼び出しを 1 行記録します。
//
// ★ エージェントループは数分かかることがあり、**その間このアプリは何も出力しません。**
// 運用側からは実行中とハングの区別が付かないため、進行の唯一の手掛かりとしてここを残します。
// 残り回数も併せて出すのは、上限に達して調査を打ち切った場合（結果は正常な戻り値として
// モデルへ返るのでエラーにならない）を後から追えるようにするためです。
//
// ctx は agent.Context です。ADK の ReadonlyContext が context.Context を埋め込んでいるため、
// slogctx が載せた job_id / mode もそのまま付きます。
func (t *toolbox) trace(ctx context.Context, tool string, attrs ...any) {
	slog.InfoContext(ctx, "エージェントがツールを呼びました",
		append([]any{"tool", tool, "remaining", t.remaining.Load()}, attrs...)...)
}

// cancelErr は、走査が終わった理由が context のキャンセル・締切だった場合に、その原因を
// Go の error として返します。
//
// ツールの失敗は基本的に Error フィールドへ載せてモデルに判断させますが（budgetExhaustedMsg
// の項を参照）、キャンセルだけは例外です。**実行そのものが終わっているのでリカバリーの
// 余地が無く**、途中まで走った結果を正常な戻り値として返すと「探したが見つからなかった」
// と区別が付かなくなります。
func cancelErr(ctx context.Context, tool string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("adkagent: %s を中断しました: %w", tool, err)
	}
	return nil
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
	// From は読み始める行番号です（1 始まり）。0 や負値は先頭からとみなします。
	From int `json:"from,omitempty"`
	// Lines は読む行数です。0 以下なら From から末尾までです。
	Lines int `json:"lines,omitempty"`
}

type readFileResult struct {
	Content string `json:"content,omitempty"`
	// From / To は実際に返した行の範囲です（1 始まり、To を含む）。
	From int `json:"from,omitempty"`
	To   int `json:"to,omitempty"`
	// TotalLines はファイル全体の行数です。**続きがあるかどうかはこれで分かります。**
	// 無いと、範囲を指定して読んだモデルは「これで全部なのか」を判断できません。
	TotalLines int `json:"total_lines,omitempty"`
	// Truncated は、返す量の上限に当たって末尾を落としたことを示します。
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// readFileTool は ADK へ渡すハンドラです。functiontool が要求する引数型は agent.Context
// ですが、ツール本体が使うのはその context.Context としての側面（キャンセルとログ）だけです。
// 本体を素の context.Context で受けておくと、テストから agent.Context の実装を用意せずに
// 呼べます。list_files / search_text も同じ形です。
func (t *toolbox) readFileTool(toolCtx agent.Context, args readFileArgs) (readFileResult, error) {
	return t.readFile(toolCtx, args)
}

func (t *toolbox) readFile(toolCtx context.Context, args readFileArgs) (readFileResult, error) {
	if !t.spend() {
		return readFileResult{Error: budgetExhaustedMsg}, nil
	}
	t.trace(toolCtx, "read_file", "path", args.Path)

	path, err := t.resolve(args.Path)
	if err != nil {
		slog.WarnContext(toolCtx, "ツールがパスを解決できませんでした", "tool", "read_file", "path", args.Path, "error", err)
		return readFileResult{Error: err.Error()}, nil
	}

	// path は resolve が root 配下へ閉じ込め済みです（symlink 経由の脱出も実体解決で
	// 弾いています）。ここを可変にしないとツールとして成立しません。
	content, err := os.ReadFile(path) //nolint:gosec // resolve でワークスペース内に限定済み
	if err != nil {
		return readFileResult{Error: fmt.Sprintf("読み込みに失敗しました: %v", err)}, nil
	}

	// テキストかどうかは読み込んだ内容そのもので判定します。先に切り詰めると、
	// 日本語のようなマルチバイト文字が途中で割れて「テキストではない」と誤判定されます
	// （3 バイト文字なら切り口が文字境界に当たるのは 1/3 だけです）。
	if !utf8.Valid(content) {
		return readFileResult{Error: "テキストファイルではありません"}, nil
	}

	body, from, to, total := selectLines(string(content), args.From, args.Lines)

	truncated := false
	if len(body) > maxFileBytes {
		body = body[:truncateAtRune(body, maxFileBytes)]
		truncated = true
		// 落としたぶん、返した範囲の終わりも縮みます。ここを直さないと、モデルは
		// 読めていない行を読んだつもりで次へ進みます。
		to = from + strings.Count(body, "\n")
	}
	return readFileResult{
		Content:    body,
		From:       from,
		To:         to,
		TotalLines: total,
		Truncated:  truncated,
	}, nil
}

// selectLines は、1 始まりの行範囲を切り出し、範囲と全体の行数を返します。
//
// 範囲の指定が無ければ全体を返します（従来どおり）。範囲が外れている場合は空を返し、
// 行番号は 0 になります。**呼び出し側が総行数を見て指定し直せる**ようにするためで、
// エラーにはしません。
func selectLines(text string, from, count int) (body string, first, last, total int) {
	lines := strings.Split(text, "\n")
	// 末尾の改行で生まれる空要素は行として数えません。
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
	if count > 0 && from+count-1 < end {
		end = from + count - 1
	}
	return strings.Join(lines[from-1:end], "\n"), from, end, total
}

// truncateAtRune は、limit を超えない範囲で最も後ろの文字境界を返します。
// content は utf8.Valid 済みであることを前提とします。
//
// []byte（read_file の中身）と string（search_text のヒット行）の両方から呼ぶため型
// パラメータにしています。どちらも中身はバイト列で、判定に使うのは添字だけです。
func truncateAtRune[T ~[]byte | ~string](content T, limit int) int {
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
		files = append(files, filepath.ToSlash(rel))
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

// --- search_text ---

type searchTextArgs struct {
	Query string `json:"query"`
	// Context は、ヒット行の前後に添える行数です（0〜maxSearchContext、既定 0）。
	//
	// 既定を 0 にしているのは、広い検索で総量の上限に先に当たると、**添えた文脈のぶんだけ
	// ヒットそのものが落ちる**ためです。1 行では判断できないと分かっている検索でだけ指定します。
	Context int `json:"context,omitempty"`
}

type searchTextResult struct {
	// Hits の各要素は "path:line: 行の内容" 形式です。
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
	t.trace(toolCtx, "search_text", "query", query)

	ctxLines := min(max(args.Context, 0), maxSearchContext)

	lowered := strings.ToLower(query)
	var hits []string
	truncated := false
	scanned := 0
	resultBytes := 0
	matches := 0

	err := filepath.WalkDir(t.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// キャンセルはファイル単位で見ます。1 ファイルは maxSearchByteLen で頭打ちなので、
		// 1 回のコールバックが締切を大きく踏み越えることはありません。
		if err := toolCtx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// ★ 通常ファイル以外（symlink・FIFO・デバイス）は開きません。**os.ReadFile は
		// symlink を辿るため、ここを通すと root 外のファイルの中身がモデルの入力に入ります。**
		// d.Info() が返すのはリンク自身の情報なので、下のサイズ検査も素通りします
		// （read_file は resolve で塞いでいますが、こちらは WalkDir のパスを直接開きます）。
		if !d.Type().IsRegular() {
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
		// path は WalkDir が t.root 配下から渡す通常ファイルのパスです。
		content, err := os.ReadFile(path) //nolint:gosec // 走査元が root 配下の通常ファイルに限定済み
		if err != nil || !utf8.Valid(content) {
			return nil
		}

		rel, err := filepath.Rel(t.root, path)
		if err != nil {
			return err
		}
		relPath := filepath.ToSlash(rel)

		// emit は 1 行を結果へ載せます。載せられなければ false を返します。
		//
		// 既に載せた行は飛ばします。ヒットが近いと前後の文脈が重なるためで、
		// 潰さないと同じ行が何度も並んで総量の上限を無駄に食います。
		lastEmitted := 0
		emit := func(no int, line string) bool {
			if no <= lastEmitted {
				return true
			}
			hit, cut := formatHit(relPath, no, line)
			if cut {
				truncated = true
			}
			// 総量の上限に当たったら、その 1 件は載せずに打ち切ります。
			if resultBytes+len(hit) > maxSearchResultBytes {
				truncated = true
				return false
			}
			resultBytes += len(hit)
			hits = append(hits, hit)
			lastEmitted = no
			return true
		}

		// strings.Lines は行を切り出しながら回すので、大きなファイルでも
		// 全行ぶんのスライスを一度に確保しません。**前後の文脈も、直前の数行を
		// 持ち回るだけで足ります**（全行を配列にする必要はありません）。
		var before []bufferedLine
		remainingAfter := 0
		lineNo := 0

		for raw := range strings.Lines(string(content)) {
			lineNo++
			// 落とすのは改行だけです。TrimSpace までやるとインデントが消え、
			// モデルから見て行の位置関係（ネストの深さ、箇条書きの階層）が読めなくなります。
			line := strings.TrimRight(raw, "\r\n")

			switch {
			case strings.Contains(strings.ToLower(line), lowered):
				if matches >= maxSearchHits {
					truncated = true
					return filepath.SkipAll
				}
				matches++
				for _, b := range before {
					if !emit(b.no, b.text) {
						return filepath.SkipAll
					}
				}
				before = before[:0]
				if !emit(lineNo, line) {
					return filepath.SkipAll
				}
				remainingAfter = ctxLines

			case remainingAfter > 0:
				if !emit(lineNo, line) {
					return filepath.SkipAll
				}
				remainingAfter--

			case ctxLines > 0:
				if len(before) == ctxLines {
					before = before[1:]
				}
				before = append(before, bufferedLine{no: lineNo, text: line})
			}
		}
		return nil
	})
	if err != nil {
		if cerr := cancelErr(toolCtx, "search_text"); cerr != nil {
			return searchTextResult{}, cerr
		}
		return searchTextResult{Error: fmt.Sprintf("検索に失敗しました: %v", err)}, nil
	}
	return searchTextResult{Hits: hits, Truncated: truncated}, nil
}

// bufferedLine は、ヒットの手前で持ち回る 1 行です。
type bufferedLine struct {
	no   int
	text string
}

// formatHit は "path:line: 行の内容" 形式のヒットを組み立て、行を切り詰めたかを返します。
// 切り詰めは文字境界で行い、切ったことが分かるよう末尾に省略記号を付けます。
func formatHit(rel string, lineNo int, line string) (string, bool) {
	if len(line) <= maxHitBytes {
		return fmt.Sprintf("%s:%d: %s", rel, lineNo, line), false
	}
	return fmt.Sprintf("%s:%d: %s…", rel, lineNo, line[:truncateAtRune(line, maxHitBytes)]), true
}
