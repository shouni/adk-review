package adkagent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"

	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// Limits on how much the tools return. They keep one call from eating the model's context;
// anything beyond is cut off and the result says so.
const (
	maxFileBytes     = 200 * 1024
	maxListEntries   = 500
	maxSearchHits    = 100
	maxSearchByteLen = 1 * 1024 * 1024 // Files larger than this are not searched
	maxSearchFiles   = 2000            // Limit on the files opened in one search

	// The hit count alone does not bound the returned size. With a minified 1 MiB line, 100 hits
	// make a 100 MiB tool result, eating the model's context, latency and API bill at once. Both
	// each hit and the total get a byte limit.
	maxHitBytes          = 2 * 1024
	maxSearchResultBytes = 64 * 1024

	// A count alone is not enough for list_files either. A deeply nested path can be several KiB,
	// and 500 of them can make a tool result of several MB. The limit matches search_text's because
	// the reason to bound it (re-read on every later turn) is the same.
	maxListResultBytes = 64 * 1024

	// Limit on the query's length. The model picks the search term, and text in the repository
	// can lead it to build an absurdly long query.
	maxQueryBytes = 200

	// maxSearchContext is the limit on the lines added around one hit.
	// Beyond 5 lines, read_file with a range is cheaper.
	maxSearchContext = 5
)

// toolbox is the set of workspace tools bound to one review run.
//
// remaining is the call count left, shared by all tools. It cuts by count rather than time
// because, against the whole review's deadline (the Cloud Tasks dispatch deadline), the model's
// thinking time cannot be predicted but the number of tool calls can be estimated at design time.
type toolbox struct {
	root      string
	budget    int64
	remaining atomic.Int64
}

// budgetExhaustedMsg is the text telling the model the calls are used up.
//
// Returned as a Go error, a tool failure is treated by the framework as an error event and the
// model cannot choose a recovery (try another path, give up and go to the final answer). Failures
// are passed as normal return values in each tool's Error field, and the model decides.
const budgetExhaustedMsg = "ツールの呼び出し回数上限に達しました。これ以上の調査はできません。ここまでに得た情報で最終レビューをまとめてください。"

// newTools builds the set of tools that can reach only under root.
//
// Only registration and the descriptions given to the model live here. Each tool's body is in
// tool_read.go / tool_list.go / tool_search.go.
//
// The toolbox is returned too so the calls can be counted after the run (used). Whether the
// limit suits reality is judged by how many each run used, not by whether it ran out.
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
		return nil, nil, fmt.Errorf("failed to build read_file: %w", err)
	}

	listFiles, err := functiontool.New(functiontool.Config{
		Name: "list_files",
		Description: "作業ディレクトリ内のファイル一覧を返します。dir を省略するとルートから列挙します。" +
			"リポジトリの構成を把握するために最初に呼ぶことを推奨します。",
	}, tb.listFilesTool)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build list_files: %w", err)
	}

	searchText, err := functiontool.New(functiontool.Config{
		Name: "search_text",
		Description: "作業ディレクトリ内の全テキストファイルから文字列を検索し、ファイル名・行番号付きで返します。" +
			"登場人物名・用語・関数名などが他のどこに現れるかの確認に使ってください（大文字小文字は区別しません）。" +
			"context（0〜5）を指定すると各ヒットの前後の行も返します。" +
			"1 行では判断できないと分かっているときは、read_file で開き直すより context を指定するほうが安く済みます。",
	}, tb.searchTextTool)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build search_text: %w", err)
	}

	return tb, []tool.Tool{readFile, listFiles, searchText}, nil
}

// newToolbox resolves root to its real path and creates the toolbox.
//
// It is resolved once at construction so the path check (resolve) and the relative paths in
// responses share one base. In some environments an OS-level symlink makes the apparent path
// differ from the real one, like macOS's /var -> /private/var.
func newToolbox(root string, budget int64) (*toolbox, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve the working directory (%s): %w", root, err)
	}

	tb := &toolbox{root: resolved, budget: budget}
	tb.remaining.Store(budget)
	return tb, nil
}

// used returns how many calls were actually made.
//
// remaining keeps decreasing after it runs out (spend decrements before checking), so the
// result is capped at the limit, so a run that ran out is not recorded as "called more than the limit".
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

// spend consumes one remaining call. It returns false when none are left.
func (t *toolbox) spend() bool {
	return t.remaining.Add(-1) >= 0
}

// trace logs one line per tool call.
//
// ★ The agent loop can take minutes, during which the app outputs nothing else. Operations
// cannot tell running from hung, so this stays as the only sign of progress. The remaining count
// is logged too, so a run cut off at the limit (which returns to the model as a normal value and
// is no error) can be traced afterwards.
//
// ctx is an agent.Context. ADK's ReadonlyContext embeds context.Context, so the job_id / mode
// attached by slogctx come along as well.
func (t *toolbox) trace(ctx context.Context, tool string, attrs ...any) {
	slog.InfoContext(ctx, "agent called a tool",
		append([]any{"tool", tool, "remaining", t.remaining.Load()}, attrs...)...)
}

// cancelErr returns the cause as a Go error when the walk ended because the context was
// cancelled or hit its deadline.
//
// Tool failures normally go in the Error field for the model to judge (see budgetExhaustedMsg),
// but cancellation is the exception. The run itself is over, so there is no room for recovery,
// and returning a partial walk as a normal value could not be told apart from "searched and
// found nothing".
func cancelErr(ctx context.Context, tool string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("adkagent: %s was interrupted: %w", tool, err)
	}
	return nil
}

// resolve turns a relative path into an absolute path under root.
//
// filepath.Clean alone does not stop an escape through a symlink, so the real path is resolved
// and checked to be under root. The reviewed repository is external input, and a symlink in it
// may point at any file on the host.
//
// Its error text reaches the model through the tools' Error field, so it stays Japanese.
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

	// t.root was resolved at construction, so resolving this side alone aligns the base.
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
