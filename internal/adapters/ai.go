// Package adapters provides Git / Slack / storage / prompt / pipeline ACL as
// implementations of go-review-kit's ports.
package adapters

import (
	"github.com/shouni/go-review-kit/review"
	"google.golang.org/genai"

	"github.com/shouni/adk-review/internal/adkagent"
	"github.com/shouni/adk-review/internal/config"
)

// geminiLocationID is the location used for Gemini calls.
//
// It is kept apart from GCP_LOCATION_ID (the Cloud Tasks region) because the region a model
// is offered in and the queue's region are decided for different reasons.
const geminiLocationID = "global"

// Retry settings for transient Vertex AI failures.
//
// ★ Without these, nothing is ever retried. With nil RetryOptions genai sends the request
// once and passes the result through (retryHTTPRequest in common.go). ADK does not put
// them into the client config either, so by default a single 5xx loses the review along
// with all the work done so far. review-queue has max_attempts = 1, so no task retry comes
// either (in practice one review was lost outright to a Vertex 504).
//
// The attempts and waits are tighter than the default (5 attempts, up to 60 seconds) because
// the wait eats into PIPELINE_TIMEOUT. With these settings the total wait is at most 7 seconds,
// small enough against a measured review (median 39 seconds). genai's retry loop checks
// ctx.Err(), so it never runs past the deadline.
//
// The retried statuses are left to genai's default (408, 429, 5xx). A copy here would keep
// the old list when genai adds to it.
const (
	retryAttempts     int32   = 4
	retryInitialDelay float64 = 1.0
	retryMaxDelay     float64 = 8.0
)

// NewAgentReviewer builds the ADK agent's review.WorkspaceReviewer.
func NewAgentReviewer(cfg *config.Config) review.WorkspaceReviewer {
	return adkagent.New(adkagent.Config{
		ClientConfig: geminiClientConfig(cfg),
		MaxToolCalls: cfg.AI.AgentMaxToolCalls,
	})
}

// geminiClientConfig builds the genai client config handed to ADK's model layer.
//
// Authentication is Vertex AI (the runtime SA's roles/aiplatform.user). The API key path is not wired.
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
