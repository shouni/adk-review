package adapters

import (
	"testing"

	"google.golang.org/genai"

	"github.com/shouni/adk-review/internal/config"
)

// ★ The retry settings must not be dropped.
//
// With nil RetryOptions genai never retries. If they were dropped nobody would notice in
// normal times; it would show only on the day Vertex returns a 5xx and a review that took
// minutes is lost whole (review-queue has max_attempts = 1, so no task retry comes either).
func TestGeminiClientConfigEnablesRetry(t *testing.T) {
	cfg := geminiClientConfig(&config.Config{GCP: config.GCPConfig{ProjectID: "project-1"}})

	if cfg.Backend != genai.BackendVertexAI {
		t.Errorf("Backend = %v, want Vertex AI (the API key path is not wired)", cfg.Backend)
	}
	if cfg.Location != geminiLocationID {
		t.Errorf("Location = %q, want %q", cfg.Location, geminiLocationID)
	}

	retry := cfg.HTTPOptions.RetryOptions
	if retry == nil {
		t.Fatal("RetryOptions is unset (genai does not retry when it is nil)")
	}
	if retry.Attempts == nil || *retry.Attempts != retryAttempts {
		t.Errorf("Attempts = %v, want %d", retry.Attempts, retryAttempts)
	}

	// The total wait must not eat into PIPELINE_TIMEOUT. With the default (up to 60 seconds),
	// the waiting could outlast the review itself.
	if retry.MaxDelay == nil || *retry.MaxDelay > retryMaxDelay {
		t.Errorf("MaxDelay = %v, want <= %v", retry.MaxDelay, retryMaxDelay)
	}
}
