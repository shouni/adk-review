package handlers

import (
	"strings"
	"testing"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

func TestValidateBranchName(t *testing.T) {
	tests := []struct {
		name    string
		branch  string
		wantErr bool
	}{
		{name: "simple valid", branch: "main", wantErr: false},
		{name: "path valid", branch: "feature/new-ui", wantErr: false},
		{name: "contains space", branch: "feature/new ui", wantErr: true},
		{name: "contains double dot", branch: "feature..bad", wantErr: true},
		{name: "contains double slash", branch: "feature//bad", wantErr: true},
		{name: "ends with slash", branch: "feature/", wantErr: true},
		{name: "ends with dot", branch: "feature.", wantErr: true},
		{name: "empty branch", branch: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBranchName(tt.branch)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBranchName(%q) error = %v, wantErr %v", tt.branch, err, tt.wantErr)
			}
		})
	}
}

func TestValidateReviewRequest(t *testing.T) {
	// Model names are validated against the configured list, so cfg is needed.
	h := &Handler{cfg: &config.Config{AI: config.AIConfig{GeminiModels: []string{"gemini-3.6-flash"}}}}

	validRequest := domain.ReviewRequest{
		RepoURL:       "git@github.com:org/repo.git",
		BaseBranch:    "main",
		FeatureBranch: "feature/new-ui",
		Mode:          "code",
		ModelName:     "gemini-3.6-flash",
	}

	tests := []struct {
		name    string
		mutate  func(*domain.ReviewRequest)
		wantErr string // text the error message must contain
	}{
		{
			name:   "valid request",
			mutate: func(_ *domain.ReviewRequest) {},
		},
		{
			name:    "missing repo url",
			mutate:  func(r *domain.ReviewRequest) { r.RepoURL = "" },
			wantErr: "すべてのフィールド",
		},
		{
			name:    "invalid mode",
			mutate:  func(r *domain.ReviewRequest) { r.Mode = "invalid-mode" },
			wantErr: "不正なレビューモード",
		},
		{
			name:    "invalid gemini model",
			mutate:  func(r *domain.ReviewRequest) { r.ModelName = "invalid-model" },
			wantErr: "不正なGeminiモデル",
		},
		{
			name:    "invalid repo url format",
			mutate:  func(r *domain.ReviewRequest) { r.RepoURL = "invalid-url" },
			wantErr: "URLの形式",
		},
		{
			name:    "reject https github url",
			mutate:  func(r *domain.ReviewRequest) { r.RepoURL = "https://github.com/org/repo.git" },
			wantErr: "git@github.com:owner/repo.git",
		},
		{
			name:    "reject non github ssh host",
			mutate:  func(r *domain.ReviewRequest) { r.RepoURL = "git@example.com:org/repo.git" },
			wantErr: "git@github.com:owner/repo.git",
		},
		{
			name:    "reject ssh scheme",
			mutate:  func(r *domain.ReviewRequest) { r.RepoURL = "ssh://git@github.com/org/repo.git" },
			wantErr: "git@github.com:owner/repo.git",
		},
		{
			name:    "invalid base branch name",
			mutate:  func(r *domain.ReviewRequest) { r.BaseBranch = "main..error" },
			wantErr: "ベースブランチ名",
		},
		{
			name:    "invalid feature branch name",
			mutate:  func(r *domain.ReviewRequest) { r.FeatureBranch = "feature/" },
			wantErr: "フィーチャーブランチ名",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Copy the valid base request
			req := validRequest
			tt.mutate(&req)

			err := h.validateReviewRequest(req)

			// When no error is expected
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error for %s: %v", tt.name, err)
				}
				return
			}

			// When an error is expected
			if err == nil {
				t.Errorf("expected error containing %q, but got nil for %s", tt.wantErr, tt.name)
				return
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain expected substring %q for %s", err.Error(), tt.wantErr, tt.name)
			}
		})
	}
}
