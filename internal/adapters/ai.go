// Package adapters は、Gemini / Git / Slack / ストレージのクライアントを
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

// NewAgentReviewer は、ADK エージェントの review.WorkspaceReviewer を構築します。
//
// 認証は Vertex AI（実行 SA の roles/aiplatform.user）です。API キー経路は配線していません。
func NewAgentReviewer(cfg *config.Config) review.WorkspaceReviewer {
	return adkagent.New(adkagent.Config{
		ClientConfig: genai.ClientConfig{
			Backend:  genai.BackendVertexAI,
			Project:  cfg.GCP.ProjectID,
			Location: geminiLocationID,
		},
		MaxToolCalls: cfg.AI.AgentMaxToolCalls,
	})
}
