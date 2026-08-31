package handlers

import (
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/go-job-kit/jobstatus"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// pastReview は、再実行の元になる過去のレビューの記録です。
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
	h.HandleReviewForm(w, httptest.NewRequest(http.MethodGet, "/"+query, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}
	return html.UnescapeString(w.Body.String())
}

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

func TestHandleReviewForm_RendersModelsWithFirstDefault(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{
		AI: config.AIConfig{GeminiModels: []string{"gemini-3.5-flash", "gemini-3.1-pro-preview"}},
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
		`<select id="model_name" name="model_name"`,
		`<option value="gemini-3.5-flash" selected>gemini-3.5-flash</option>`,
		`<option value="gemini-3.1-pro-preview" >gemini-3.1-pro-preview</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("model option not rendered: want %q body=%s", want, body)
		}
	}
}

func TestHandleReviewForm_RendersCSRFTokenFromContext(t *testing.T) {
	h, err := NewHandler(Deps{Config: &config.Config{}, TaskEnqueuer: &fakeEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(withCSRFToken(req.Context(), "test-csrf-token"))
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

// 失敗したレビューは review-queue が max_attempts = 1 なので再試行されません。
// 依頼内容を打ち直さずにやり直せることが、この画面の存在理由です。
func TestHandleReviewForm_PrefillsFromPastReview(t *testing.T) {
	past := pastReview()
	body := renderFormWithQuery(t, &fakeStatusStore{getStatus: past}, "?from="+past.JobID)

	for _, want := range []string{
		`value="` + past.RepoURL + `"`,
		`value="` + past.BaseBranch + `"`,
		`value="` + past.FeatureBranch + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s が引き継がれていません: %s", want, body)
		}
	}
	// モードとモデルは select なので、選択済みとして描かれているかで見ます。
	if !strings.Contains(body, `value="`+past.Mode+`" selected`) {
		t.Errorf("モードが選択されていません: %s", body)
	}
	if !strings.Contains(body, `value="`+past.ModelName+`" selected`) {
		t.Errorf("モデルが選択されていません: %s", body)
	}
	if !strings.Contains(body, "過去の依頼内容を引き継いでいます") {
		t.Errorf("引き継いだことが画面に出ていません: %s", body)
	}
}

// 元のレビューを読めなくてもエラー画面にはしません。再実行は入力を省くための
// 便宜で、それが効かないことは新しい依頼を妨げないためです。
func TestHandleReviewForm_FallsBackWhenSourceIsUnreadable(t *testing.T) {
	body := renderFormWithQuery(t, &fakeStatusStore{getErr: errors.New("boom")}, "?from=20260810-213000-a1b2c3d4")

	if !strings.Contains(body, "既定値で表示しています") {
		t.Errorf("読み込めなかったことが画面に出ていません: %s", body)
	}
	if !strings.Contains(body, `value="main"`) || !strings.Contains(body, `value="develop"`) {
		t.Errorf("既定値のフォームになっていません: %s", body)
	}
}

// 依頼内容を記録する前の形式で保存されたジョブを開いても、既定値まで空へ
// 倒さないこと。倒すと、再実行のほうが白紙より入力が増えます。
func TestHandleReviewForm_KeepsDefaultsForFieldsTheRecordLacks(t *testing.T) {
	sparse := domain.JobStatus{
		Status:  jobstatus.Status{JobID: "20260810-213000-a1b2c3d4", State: jobstatus.StateFailed},
		RepoURL: "git@github.com:org/other.git",
	}
	body := renderFormWithQuery(t, &fakeStatusStore{getStatus: sparse}, "?from="+sparse.JobID)

	if !strings.Contains(body, `value="`+sparse.RepoURL+`"`) {
		t.Errorf("記録にある項目が引き継がれていません: %s", body)
	}
	if !strings.Contains(body, `value="main"`) || !strings.Contains(body, `value="develop"`) {
		t.Errorf("記録に無い項目の既定値が消えています: %s", body)
	}
}

// from が無いときは、これまでどおり白紙のフォームを出すこと。
func TestHandleReviewForm_WithoutFromStaysBlank(t *testing.T) {
	body := renderFormWithQuery(t, &fakeStatusStore{getStatus: pastReview()}, "")

	if strings.Contains(body, "過去の依頼内容") || strings.Contains(body, "既定値で表示しています") {
		t.Errorf("断り書きが出ています: %s", body)
	}
	if strings.Contains(body, `value="git@github.com:org/other.git"`) {
		t.Errorf("読み込むはずのないレビューが引き継がれています: %s", body)
	}
}
