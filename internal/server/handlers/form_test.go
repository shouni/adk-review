package handlers

import (
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-job-kit/jobstatus"
)

// pastReview is the record of a past review to rerun.
func pastReview() domain.JobStatus {
	return domain.JobStatus{
		Status:        jobstatus.Status{JobID: "20260810-213000-a1b2c3d4", State: jobstatus.StateFailed},
		RepoURL:       "git@github.com:org/other.git",
		BaseBranch:    "release",
		FeatureBranch: "topic/fix",
		Mode:          "novel",
		ModelName:     "gemini-2.5-flash",
	}
}

func renderFormWithQuery(t *testing.T, store domain.StatusStore, query string) string {
	t.Helper()

	cfg := &config.Config{AI: config.AIConfig{GeminiModels: []string{"gemini-2.5-pro", "gemini-2.5-flash"}}}
	h, err := NewHandler(Deps{Config: cfg, TaskEnqueuer: &fakeEnqueuer{}, StatusStore: store})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	w := httptest.NewRecorder()
	h.HandleForm(w, httptest.NewRequest(http.MethodGet, "/"+query, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}
	return html.UnescapeString(w.Body.String())
}

func TestHandleForm_RendersValidationPatterns(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{}, TaskEnqueuer: &fakeEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.HandleForm(w, req)

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

func TestHandleForm_RendersPromptModesWithCodeDefault(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{}, TaskEnqueuer: &fakeEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.HandleForm(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}
	body := html.UnescapeString(w.Body.String())
	for _, want := range []string{
		`<option value="article"`,
		`article (技術記事レビュー)`,
		`<option value="code" selected>`,
		`code (コード品質レビュー)`,
		`<option value="novel"`,
		`novel (小説原稿レビュー)`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("review mode option not rendered: want %q body=%s", want, body)
		}
	}
}

func TestHandleForm_RendersModelsWithFirstDefault(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{
		AI: config.AIConfig{GeminiModels: []string{"gemini-3.5-flash", "gemini-3.1-pro-preview"}},
	}, TaskEnqueuer: &fakeEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.HandleForm(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}
	body := html.UnescapeString(w.Body.String())
	for _, want := range []string{
		`<select id="model_name" name="model_name"`,
		`<option value="gemini-3.5-flash" selected>gemini-3.5-flash</option>`,
		`<option value="gemini-3.1-pro-preview" >gemini-3.1-pro-preview</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("model option not rendered: want %q body=%s", want, body)
		}
	}
}

func TestHandleForm_RendersCSRFTokenFromContext(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{}, TaskEnqueuer: &fakeEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(withCSRFToken(req.Context(), "test-csrf-token"))
	w := httptest.NewRecorder()
	h.HandleForm(w, req)

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

// A failed review is not retried, since review-queue has max_attempts = 1.
// Redoing it without retyping the request is why this screen exists.
func TestHandleForm_PrefillsFromPastReview(t *testing.T) {
	past := pastReview()
	body := renderFormWithQuery(t, &fakeStatusStore{getStatus: past}, "?from="+past.JobID)

	for _, want := range []string{
		`value="` + past.RepoURL + `"`,
		`value="` + past.BaseBranch + `"`,
		`value="` + past.FeatureBranch + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s was not carried over: %s", want, body)
		}
	}
	// Mode and model are selects, so check that they are rendered as selected.
	if !strings.Contains(body, `value="`+past.Mode+`" selected`) {
		t.Errorf("the mode is not selected: %s", body)
	}
	if !strings.Contains(body, `value="`+past.ModelName+`" selected`) {
		t.Errorf("the model is not selected: %s", body)
	}
	if !strings.Contains(body, "過去の依頼内容を引き継いでいます") {
		t.Errorf("the screen does not say it was carried over: %s", body)
	}
}

// Failing to read the original review does not show an error page. A rerun is a convenience
// that saves typing, and it not working must not block a new request.
func TestHandleForm_FallsBackWhenSourceIsUnreadable(t *testing.T) {
	body := renderFormWithQuery(t, &fakeStatusStore{getErr: errors.New("boom")}, "?from=20260810-213000-a1b2c3d4")

	if !strings.Contains(body, "既定値で表示しています") {
		t.Errorf("the screen does not say it could not be loaded: %s", body)
	}
	if !strings.Contains(body, `value="main"`) || !strings.Contains(body, `value="develop"`) {
		t.Errorf("the form does not have the defaults: %s", body)
	}
}

// Opening a job stored in a format from before requests were recorded does not blank the
// defaults too. Otherwise a rerun would need more typing than a blank form.
func TestHandleForm_KeepsDefaultsForFieldsTheRecordLacks(t *testing.T) {
	sparse := domain.JobStatus{
		Status:  jobstatus.Status{JobID: "20260810-213000-a1b2c3d4", State: jobstatus.StateFailed},
		RepoURL: "git@github.com:org/other.git",
	}
	body := renderFormWithQuery(t, &fakeStatusStore{getStatus: sparse}, "?from="+sparse.JobID)

	if !strings.Contains(body, `value="`+sparse.RepoURL+`"`) {
		t.Errorf("a field in the record was not carried over: %s", body)
	}
	if !strings.Contains(body, `value="main"`) || !strings.Contains(body, `value="develop"`) {
		t.Errorf("the default of a field missing from the record is gone: %s", body)
	}
}

// Without from, a blank form is shown as before.
func TestHandleForm_WithoutFromStaysBlank(t *testing.T) {
	body := renderFormWithQuery(t, &fakeStatusStore{getStatus: pastReview()}, "")

	if strings.Contains(body, "過去の依頼内容") || strings.Contains(body, "既定値で表示しています") {
		t.Errorf("a note is shown: %s", body)
	}
	if strings.Contains(body, `value="git@github.com:org/other.git"`) {
		t.Errorf("a review that should not be loaded was carried over: %s", body)
	}
}
