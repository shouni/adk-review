// Package adapters は、Git / Slack / 保存 / プロンプト / パイプライン ACL を
// go-review-kit のポート実装として提供します。
package adapters

import (
	"github.com/shouni/go-review-kit/review"
	"google.golang.org/genai"

	"github.com/shouni/adk-review/internal/adkagent"
	"github.com/shouni/adk-review/internal/config"
)

// geminiLocationID は、Gemini 呼び出しに使うロケーションです。
//
// GCP_LOCATION_ID（Cloud Tasks のリージョン）と分けているのは、モデルの提供リージョンと
// キューのリージョンが別の都合で決まるためです。
const geminiLocationID = "global"

// Vertex AI の一時障害に対する再試行の設定です。
//
// ★ 明示しないと再試行は一度も行われません。genai は RetryOptions が nil のとき
// 素通しで 1 回だけ投げます（common.go の retryHTTPRequest）。ADK もクライアント設定へ
// これを入れないため、既定では 5xx が 1 回返っただけでレビューがそこまでの作業ごと
// 失われます。review-queue は max_attempts = 1 なのでタスクの再試行も来ません
// （実測で、Vertex の 504 で 1 件そのまま失われています）。
//
// 回数と待ちを既定（5 回・最大 60 秒）より絞るのは、待ち時間が PIPELINE_TIMEOUT を
// 食うためです。この設定なら待ちの合計は最大 7 秒で、レビュー 1 件の実測（中央値 39 秒）に
// 対して十分小さく済みます。genai の再試行ループは ctx.Err() を見るので、締切の外へは
// 出ません。
//
// 対象のステータスは genai の既定（408・429・5xx）に任せます。写すと、向こうが増やしたときに
// こちらだけ古い一覧を持ち続けます。
const (
	retryAttempts     int32   = 4
	retryInitialDelay float64 = 1.0
	retryMaxDelay     float64 = 8.0
)

// NewAgentReviewer は、ADK エージェントの review.WorkspaceReviewer を構築します。
func NewAgentReviewer(cfg *config.Config) review.WorkspaceReviewer {
	return adkagent.New(adkagent.Config{
		ClientConfig: geminiClientConfig(cfg),
		MaxToolCalls: cfg.AI.AgentMaxToolCalls,
	})
}

// geminiClientConfig は、ADK のモデル層へ渡す genai クライアント設定を組み立てます。
//
// 認証は Vertex AI（実行 SA の roles/aiplatform.user）です。API キー経路は配線していません。
func geminiClientConfig(cfg *config.Config) genai.ClientConfig {
	return genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  cfg.GCP.ProjectID,
		Location: geminiLocationID,
		HTTPOptions: genai.HTTPOptions{
			RetryOptions: &genai.HTTPRetryOptions{
				Attempts:     new(retryAttempts),
				InitialDelay: new(retryInitialDelay),
				MaxDelay:     new(retryMaxDelay),
			},
		},
	}
}
