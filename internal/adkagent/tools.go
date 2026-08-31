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

	// 件数だけで足りないのは list_files も同じです。深く入れ子になったパスは 1 本で
	// 数 KiB あり、500 件で数 MB のツール結果になり得ます。上限を search_text と
	// 揃えるのは、抑えたい理由（以降の全ターンで読み直される）が同じだからです。
	maxListResultBytes = 64 * 1024

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
// ★ エージェントループは数分かかることがあり、その間このアプリは何も出力しません。
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
// の項を参照）、キャンセルだけは例外です。実行そのものが終わっているのでリカバリーの
// 余地が無く、途中まで走った結果を正常な戻り値として返すと「探したが見つからなかった」
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
