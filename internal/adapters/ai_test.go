package adapters

import (
	"testing"

	"google.golang.org/genai"

	"github.com/shouni/adk-review/internal/config"
)

// ★ 再試行の設定が落ちていないこと。
//
// **genai は RetryOptions が nil だと一度も再試行しません。** 落ちても平常時は誰も
// 気付かず、気付くのは Vertex が 5xx を返した日に、数分かけたレビューが 1 件丸ごと
// 失われたときです（review-queue は max_attempts = 1 で再試行も来ません）。
func TestGeminiClientConfigEnablesRetry(t *testing.T) {
	cfg := geminiClientConfig(&config.Config{GCP: config.GCPConfig{ProjectID: "project-1"}})

	if cfg.Backend != genai.BackendVertexAI {
		t.Errorf("Backend = %v, want Vertex AI（API キー経路は配線していません）", cfg.Backend)
	}
	if cfg.Location != geminiLocationID {
		t.Errorf("Location = %q, want %q", cfg.Location, geminiLocationID)
	}

	retry := cfg.HTTPOptions.RetryOptions
	if retry == nil {
		t.Fatal("RetryOptions が未設定です（nil のとき genai は再試行しません）")
	}
	if retry.Attempts == nil || *retry.Attempts != retryAttempts {
		t.Errorf("Attempts = %v, want %d", retry.Attempts, retryAttempts)
	}

	// 待ちの合計が PIPELINE_TIMEOUT を食わないこと。既定（最大 60 秒）のままだと、
	// レビュー本体より待ち時間のほうが長くなり得ます。
	if retry.MaxDelay == nil || *retry.MaxDelay > retryMaxDelay {
		t.Errorf("MaxDelay = %v, want <= %v", retry.MaxDelay, retryMaxDelay)
	}
}
