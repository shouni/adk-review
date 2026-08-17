package handlers

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/adk-review/internal/config"
)

func TestHandleReviewForm_RendersValidationPatterns(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{}, TaskEnqueuer: &fakeEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.HandleReviewForm(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}
	body := html.UnescapeString(w.Body.String())
	if !strings.Contains(body, `name="repo_url"`) ||
		!strings.Contains(body, `pattern="`+repoURLPattern+`"`) {
		t.Fatalf("repo url pattern not rendered: %s", body)
	}
	if !strings.Contains(body, `name="base_branch"`) ||
		!strings.Contains(body, `name="feature_branch"`) ||
		!strings.Contains(body, `pattern="`+branchPattern+`"`) {
		t.Fatalf("branch pattern not rendered: %s", body)
	}
	if !strings.Contains(body, `id="base_branch" name="base_branch" class="form-control"
                       value="main"`) {
		t.Fatalf("default base branch not rendered: %s", body)
	}
	if !strings.Contains(body, `id="feature_branch" name="feature_branch" class="form-control"
                       value="develop"`) {
		t.Fatalf("default feature branch not rendered: %s", body)
	}
	if strings.Contains(body, `gorilla.csrf.Token`) {
		t.Fatalf("csrf hidden token should not be rendered: %s", body)
	}
}

func TestHandleReviewForm_RendersPromptModesWithCodeDefault(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{}, TaskEnqueuer: &fakeEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.HandleReviewForm(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}
	body := html.UnescapeString(w.Body.String())
	for _, want := range []string{
		`<option value="article"`,
		`article (技術記事・ドキュメント品質レビュー)`,
		`<option value="code" selected>`,
		`code (詳細なコード品質レビュー)`,
		`<option value="novel"`,
		`novel (小説原稿の詳細レビュー)`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("review mode option not rendered: want %q body=%s", want, body)
		}
	}
}

func TestHandleReviewForm_RendersGeminiModelsWithFirstDefault(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{
		GeminiModels: []string{"gemini-3.5-flash", "gemini-3.1-pro-preview"},
	}, TaskEnqueuer: &fakeEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.HandleReviewForm(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}
	body := html.UnescapeString(w.Body.String())
	for _, want := range []string{
		`<select id="gemini_model" name="gemini_model"`,
		`<option value="gemini-3.5-flash" selected>gemini-3.5-flash</option>`,
		`<option value="gemini-3.1-pro-preview" >gemini-3.1-pro-preview</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("gemini model option not rendered: want %q body=%s", want, body)
		}
	}
}

func TestHandleReviewForm_RendersCSRFTokenFromContext(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{}, TaskEnqueuer: &fakeEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithCSRFToken(req.Context(), "test-csrf-token"))
	w := httptest.NewRecorder()
	h.HandleReviewForm(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, `name="csrf_token"`) {
		t.Fatalf("csrf field name not rendered: %s", body)
	}
	if !strings.Contains(body, `value="test-csrf-token"`) {
		t.Fatalf("csrf token value not rendered: %s", body)
	}
}
