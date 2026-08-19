// Package adkagent は、ADK for Go のエージェントループによる review.WorkspaceReviewer 実装です。
//
// エコシステムの「genai SDK は go-gemini-client の外に出さない」規約の例外は、この
// パッケージ（と schema.go / tools.go）に閉じています。ADK のモデル層が genai を直接
// 要求するためで、go-review-kit 本体はこのパッケージを知りません。
package adkagent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/genai"

	"github.com/shouni/go-review-kit/review"
)

// DefaultMaxToolCalls は、1 回のレビューで許すツール呼び出し回数の既定値です。
//
// レビュー 1 件は呼び出し元の締切（Web 版では Cloud Tasks の dispatch deadline）内に
// 収める必要があります。時間で打ち切らず回数で打ち切るのは、締切超過ではなく
// 「調査を切り上げて最終回答をまとめる」方向へモデルを誘導するためです。
const DefaultMaxToolCalls = 32

const agentName = "workspace_reviewer"

// instruction は、エージェントの行動指針です。レビュー観点そのもの（何をどう指摘するか）は
// プロンプト側（review.PromptGenerator）の責務なので、ここには調査の進め方だけを書きます。
const instruction = `あなたは Git リポジトリの差分をレビューするエージェントです。
ユーザーメッセージとしてレビュー指示と差分が渡されます。作業ディレクトリには差分の
比較対象（head）がチェックアウトされており、ツールで中身を調べられます。

進め方:
1. まず list_files でリポジトリの構成を把握する
2. 差分だけでは判断できない点（前後の文脈、他ファイルとの整合性、用語や登場人物の
   一貫性）を read_file / search_text で確認する
3. 確認した根拠ファイルは、指摘の evidence にリポジトリ相対パスで挙げる

差分の外を確認したうえでの指摘こそがあなたの価値です。ただしツール呼び出し回数には
上限があるため、闇雲に読まず、差分から立てた仮説の検証に絞ってください。`

// Config は、Reviewer の初期化パラメータです。
type Config struct {
	// ClientConfig は、ADK のモデル層へ渡す genai クライアント設定です。
	// APIKey か、Vertex AI（Backend / Project / Location）のどちらかを設定します。
	ClientConfig genai.ClientConfig
	// MaxToolCalls は、1 回のレビューで許すツール呼び出し回数です。
	// 0 の場合は DefaultMaxToolCalls を使います。
	MaxToolCalls int
}

// Reviewer は、ADK のエージェントループで作業ディレクトリを調べる
// review.WorkspaceReviewer 実装です。
type Reviewer struct {
	clientConfig genai.ClientConfig
	maxToolCalls int64

	// models は、モデル名 → 構築エントリのキャッシュです。モデルの構築は genai.Client の
	// 生成を伴うため、同じモデル名のレビューをまたいで再利用します。
	mu     sync.Mutex
	models map[string]*modelEntry
}

// modelEntry は、1 モデル名ぶんの構築結果です。once で構築を 1 回に絞ることで、
// 同じモデルを同時に要求されても genai クライアントの生成は 1 回で済みます。
type modelEntry struct {
	once sync.Once
	llm  model.LLM
	err  error
}

// 実装がポートを満たすことをコンパイル時に確認します。
var _ review.WorkspaceReviewer = (*Reviewer)(nil)

// New は Reviewer を生成します。
func New(cfg Config) *Reviewer {
	maxCalls := int64(cfg.MaxToolCalls)
	if maxCalls <= 0 {
		maxCalls = DefaultMaxToolCalls
	}
	return &Reviewer{
		clientConfig: cfg.ClientConfig,
		maxToolCalls: maxCalls,
		models:       make(map[string]*modelEntry),
	}
}

// ReviewWorkspace は、エージェントループでレビューを実行し review.Report を返します。
//
// エージェント・ランナー・セッションはレビュー 1 件ごとに使い捨てます。レビューは
// 単発のジョブであり、実行をまたいで残したい状態が無いためです（in-memory セッションは
// そのための選択です）。
func (r *Reviewer) ReviewWorkspace(ctx context.Context, modelName, prompt string, ws review.Workspace) (review.Report, error) {
	if strings.TrimSpace(modelName) == "" {
		return review.Report{}, fmt.Errorf("adkagent: モデル名が空です")
	}
	if strings.TrimSpace(prompt) == "" {
		return review.Report{}, fmt.Errorf("adkagent: プロンプトが空です")
	}
	if strings.TrimSpace(ws.Dir) == "" {
		return review.Report{}, fmt.Errorf("adkagent: 作業ディレクトリが空です")
	}

	llm, err := r.model(ctx, modelName)
	if err != nil {
		return review.Report{}, err
	}

	tools, err := newTools(ws.Dir, r.maxToolCalls)
	if err != nil {
		return review.Report{}, err
	}

	// OutputSchema とツールは併用できます。フレームワークが set_model_response ツールを
	// 注入し、最終イベントの Text にはそのスキーマに従う JSON が入ります。
	ag, err := llmagent.New(llmagent.Config{
		Name:         agentName,
		Description:  "作業ディレクトリを調査しながら Git 差分をレビューするエージェント",
		Model:        llm,
		Instruction:  instruction,
		Tools:        tools,
		OutputSchema: reportSchema(),
	})
	if err != nil {
		return review.Report{}, fmt.Errorf("adkagent: エージェントの構築に失敗しました: %w", err)
	}

	run, err := runner.NewInMemory("adk-review", ag)
	if err != nil {
		return review.Report{}, fmt.Errorf("adkagent: ランナーの構築に失敗しました: %w", err)
	}

	final, err := r.collectFinalText(ctx, run, prompt)
	if err != nil {
		return review.Report{}, err
	}

	report, err := review.ParseReport([]byte(final))
	if err != nil {
		// ★ 生の応答を残します。**これが無いと解析失敗は原因不明のまま終わります。**
		// エージェントレビューは十数分かかるので、失敗のたびに同じ時間を払って
		// 再現を試みることになります（実際、モデルがエスケープしていない
		// バックスラッシュを excerpt に入れて落ちた例があります）。
		slog.ErrorContext(ctx, "レビュー結果を解釈できませんでした",
			"error", err,
			"response_bytes", len(final),
			"response_head", truncateForLog(final, maxLoggedResponse))
		return review.Report{}, fmt.Errorf("adkagent: %w", err)
	}
	return report, nil
}

// maxLoggedResponse は、解析に失敗した応答をログへ残す上限です。
// 全文を載せるとレビュー 1 件で数十 KB になり、原因の特定には冒頭で足ります。
const maxLoggedResponse = 2000

// truncateForLog は、ログへ載せる応答を文字境界で切り詰めます。
func truncateForLog(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit] + "…(以下略)"
}

// collectFinalText は、エージェントを実行して最終応答のテキストを取り出します。
//
// 複数の最終イベントが出る構成（サブエージェント併用時）に備えて最後の 1 件を採りますが、
// 本実装はエージェント 1 体なので実質は唯一の最終応答です。
func (r *Reviewer) collectFinalText(ctx context.Context, run *runner.Runner, prompt string) (string, error) {
	msg := genai.NewContentFromText(prompt, genai.RoleUser)

	var final strings.Builder
	for event, err := range run.Run(ctx, "reviewer", "review", msg, agent.RunConfig{}) {
		if err != nil {
			return "", fmt.Errorf("adkagent: エージェントの実行に失敗しました: %w", err)
		}
		if event == nil || !event.IsFinalResponse() || event.Content == nil {
			continue
		}
		final.Reset()
		for _, part := range event.Content.Parts {
			// Parts は []*Part なので、要素の nil も event 自体と同じく防ぎます。
			// ここで panic すると worker のリクエストごと落ち、review-queue は
			// max_attempts = 1 なのでタスクが黙って失われます。
			if part == nil {
				continue
			}
			final.WriteString(part.Text)
		}
	}

	if strings.TrimSpace(final.String()) == "" {
		return "", review.ErrEmptyResponse
	}
	return final.String(), nil
}

// model は、モデル名に対応する model.LLM を返します（キャッシュあり）。
//
// 構築はモデル名ごとの sync.Once に閉じ込め、mutex はキャッシュの出し入れだけに使います。
// gemini.NewModel は Vertex AI では認証情報の検出とメタデータサーバーへの往復を伴うため、
// ロックを握ったまま呼ぶと、初回構築中に同居する他のレビューが全部そこで直列に待ちます。
func (r *Reviewer) model(ctx context.Context, name string) (model.LLM, error) {
	r.mu.Lock()
	entry, ok := r.models[name]
	if !ok {
		entry = &modelEntry{}
		r.models[name] = entry
	}
	r.mu.Unlock()

	entry.once.Do(func() {
		entry.llm, entry.err = gemini.NewModel(ctx, name, &r.clientConfig)
	})
	if entry.err != nil {
		// 失敗を握り続けないよう、次回は作り直せるようにします。
		r.mu.Lock()
		if r.models[name] == entry {
			delete(r.models, name)
		}
		r.mu.Unlock()
		return nil, fmt.Errorf("adkagent: モデルの初期化に失敗しました (model: %s): %w", name, entry.err)
	}
	return entry.llm, nil
}
