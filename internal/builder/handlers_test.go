package builder

import (
	"strings"
	"testing"

	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/adk-review/internal/server/handlers"
)

// Validate stops startup when a role's handlers are incomplete.
//
// Incomplete, the router just skips registering those routes, so the deploy succeeds, /health
// passes, and only the submission path is broken. Cloud Tasks gets a 404 and, with
// max_attempts = 1, drops the task, so the review request is silently lost.
func TestAppHandlersValidate(t *testing.T) {
	t.Parallel()

	// The contents are never looked at; only being non-nil matters.
	authHandler := &session.Handler{}
	webHandler := &handlers.Handler{}
	workerHandler := &worker.Handler[domain.ReviewRequest]{}
	taskAuth := &oidc.Verifier{}

	tests := []struct {
		name    string
		h       AppHandlers
		wantErr string
	}{
		{
			name: "web only passes",
			h:    AppHandlers{Auth: authHandler, Web: webHandler},
		},
		{
			name: "worker only passes",
			h:    AppHandlers{Worker: workerHandler, TaskAuth: taskAuth},
		},
		{
			name: "both passes",
			h: AppHandlers{
				Auth: authHandler, Web: webHandler,
				Worker: workerHandler, TaskAuth: taskAuth,
			},
		},
		{
			// If this passed, every screen would be a 302 while startup succeeds.
			name:    "web handler without auth",
			h:       AppHandlers{Web: webHandler},
			wantErr: "web handlers",
		},
		{
			name:    "auth without web handler",
			h:       AppHandlers{Auth: authHandler},
			wantErr: "web handlers",
		},
		{
			// ★ If this passed, /tasks/execute-review would either be open without
			// authentication or not registered, dropping tasks with a 404.
			name:    "worker handler without OIDC verification",
			h:       AppHandlers{Worker: workerHandler},
			wantErr: "worker handlers",
		},
		{
			name:    "OIDC verification without worker handler",
			h:       AppHandlers{TaskAuth: taskAuth},
			wantErr: "worker handlers",
		},
		{
			name:    "no role served",
			h:       AppHandlers{},
			wantErr: "no handler was built",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.h.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("a valid setup failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the inconsistency passed (want an error containing %q)", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}
