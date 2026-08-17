// Package adapters は、Gemini / Git / Slack / ストレージのクライアントを
// go-review-kit のポート実装として提供します。
package adapters

import (
	"context"
	"fmt"

	geminiclient "github.com/shouni/go-gemini-client/gemini"
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

// NewGeminiClient は、モデル呼び出しの共有クライアントを構築します。
//
// 単発レビュアーはこのクライアント 1 個に相乗りし、リトライ・認証のポリシーをここへ
// 集約します（AI は Vertex AI 経由で呼びます。設計は README の技術スタックを参照）。
func NewGeminiClient(ctx context.Context, cfg *config.Config) (geminiclient.Generator, error) {
	client, err := geminiclient.NewClient(ctx, geminiclient.Config{
		ProjectID:  cfg.ProjectID,
		LocationID: geminiLocationID,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini クライアントの構築に失敗しました: %w", err)
	}
	return client, nil
}

// NewAgentReviewer は、ADK エージェントの review.WorkspaceReviewer を構築します。
//
// ADK のモデル層は genai SDK を直接要求するため、go-gemini-client には相乗りできません。
// 認証は単発側と同じ Vertex AI（ProjectID）に揃えます。
func NewAgentReviewer(cfg *config.Config) review.WorkspaceReviewer {
	return adkagent.New(adkagent.Config{
		ClientConfig: genai.ClientConfig{
			Backend:  genai.BackendVertexAI,
			Project:  cfg.ProjectID,
			Location: geminiLocationID,
		},
		MaxToolCalls: cfg.AgentMaxToolCalls,
	})
}
