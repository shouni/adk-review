package handlers

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// fakeStatusStore は進行状況の記録先のフェイクです。
type fakeStatusStore struct {
	err   error
	saved []domain.JobStatus
}

func (f *fakeStatusStore) Get(_ context.Context, _ string) (domain.JobStatus, error) {
	return domain.JobStatus{}, errors.New("not recorded")
}

func (f *fakeStatusStore) Save(_ context.Context, jobID string, status domain.JobStatus) error {
	if f.err != nil {
		return f.err
	}
	status.JobID = jobID
	f.saved = append(f.saved, status)
	return nil
}

// fakeHistory は履歴のフェイクです。投入直後にキャッシュが捨てられたかだけを見ます。
type fakeHistory struct {
	invalidated int
}

func (f *fakeHistory) List(context.Context, int, int) (domain.HistoryPage, error) {
	return domain.HistoryPage{}, nil
}

func (f *fakeHistory) Get(context.Context, string) (domain.ReviewDetail, error) {
	return domain.ReviewDetail{}, nil
}

func (f *fakeHistory) Delete(context.Context, string) error { return nil }

func (f *fakeHistory) Invalidate() { f.invalidated++ }

type fakeEnqueuer struct {
	err      error
	called   bool
	received domain.ReviewRequest
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, payload domain.ReviewRequest) error {
	f.called = true
	f.received = payload
	return f.err
}

// testJobID は、テストで採番されるジョブIDです。
const testJobID = "20260415-102030-abcdef123456"

func buildTestHandler(t *testing.T, jobIDErr, enqueueErr error) (*Handler, *fakeEnqueuer, *fakeStatusStore, *fakeHistory) {
	t.Helper()

	enq := &fakeEnqueuer{err: enqueueErr}
	store := &fakeStatusStore{}
	history := &fakeHistory{}

	h, err := NewHandler(Deps{
		Config: &config.Config{
			Server:  config.ServerConfig{ServiceURL: "https://service.example.com"},
			AI:      config.AIConfig{GeminiModels: []string{"gemini-2.5-flash", "gemini-2.5-pro"}},
			Storage: config.StorageConfig{GCSBucket: "bucket-a"},
		},
		TaskEnqueuer: enq,
		Layout:       domain.NewStorageLayout("bucket-a"),
		StatusStore:  store,
		History:      history,
	})
	if err != nil {
		t.Fatalf("failed to build handler: %v", err)
	}

	h.now = func() time.Time {
		return time.Date(2026, 4, 15, 10, 20, 30, 0, time.UTC)
	}
	h.newJobID = func() (string, error) {
		if jobIDErr != nil {
			return "", jobIDErr
		}
		return testJobID, nil
	}
	return h, enq, store, history
}

func newSubmitRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/submit_review", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func validFormBody() string {
	v := url.Values{}
	v.Set("repo_url", "git@github.com:org/repo.git")
	v.Set("base_branch", "main")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "code")
	v.Set("model_name", "gemini-2.5-flash")
	return v.Encode()
}

func TestHandleReviewSubmit_ParseError(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()
	h.HandleReviewSubmit(w, newSubmitRequest("%zz"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on parse error")
	}
}

func TestHandleReviewSubmit_ValidationError(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v := url.Values{}
	v.Set("repo_url", "git@github.com:org/repo.git")
	v.Set("base_branch", "main")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "invalid-mode")
	v.Set("model_name", "gemini-2.5-flash")

	h.HandleReviewSubmit(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on validation error")
	}
}

func TestHandleReviewSubmit_ValidationErrorPreservesSelectedModelName(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v := url.Values{}
	v.Set("repo_url", "invalid-url")
	v.Set("base_branch", "main")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "code")
	v.Set("model_name", "gemini-2.5-pro")

	h.HandleReviewSubmit(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on validation error")
	}
	body := html.UnescapeString(w.Body.String())
	if !strings.Contains(body, `<option value="gemini-2.5-pro" selected>gemini-2.5-pro</option>`) {
		t.Fatalf("selected gemini model should be preserved, body=%s", body)
	}
}

func TestHandleReviewSubmit_ValidationErrorPreservesFormValues(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v := url.Values{}
	v.Set("repo_url", "invalid-url")
	v.Set("base_branch", "release/2026-04")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "novel")
	v.Set("model_name", "gemini-2.5-pro")

	h.HandleReviewSubmit(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on validation error")
	}
	body := html.UnescapeString(w.Body.String())
	for _, want := range []string{
		`name="repo_url" class="form-control"
                   placeholder="例: git@github.com:user/repo.git"
                   value="invalid-url"`,
		`name="base_branch" class="form-control"
                       value="release/2026-04"`,
		`name="feature_branch" class="form-control"
                       value="feature/new-ui"`,
		`<option value="novel" selected>novel (小説原稿レビュー)</option>`,
		`<option value="gemini-2.5-pro" selected>gemini-2.5-pro</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("form value should be preserved: want %q body=%s", want, body)
		}
	}
}

func TestHandleReviewSubmit_InvalidModelName(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v := url.Values{}
	v.Set("repo_url", "git@github.com:org/repo.git")
	v.Set("base_branch", "main")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "code")
	v.Set("model_name", "gemini-invalid")

	h.HandleReviewSubmit(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on invalid gemini model")
	}
}

// ジョブIDを採番できなければ保存先も閲覧先も決まらないため、投入まで進みません。
func TestHandleReviewSubmit_JobIDError(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, errors.New("entropy failure"), nil)
	w := httptest.NewRecorder()
	h.HandleReviewSubmit(w, newSubmitRequest(validFormBody()))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called when job id assignment fails")
	}
}

func TestHandleReviewSubmit_EnqueueError(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, errors.New("queue unavailable"))
	w := httptest.NewRecorder()
	h.HandleReviewSubmit(w, newSubmitRequest(validFormBody()))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
	if !enq.called {
		t.Fatal("enqueue should be called before returning 503")
	}
}

func TestHandleReviewSubmit_Success(t *testing.T) {
	h, enq, store, history := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()
	h.HandleReviewSubmit(w, newSubmitRequest(validFormBody()))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", w.Code)
	}
	if !enq.called {
		t.Fatal("enqueue should be called on success")
	}
	if enq.received.ModelName != "gemini-2.5-flash" {
		t.Fatalf("unexpected model name: %s", enq.received.ModelName)
	}
	if enq.received.JobID != testJobID {
		t.Fatalf("unexpected job id: %s", enq.received.JobID)
	}

	// 保存先も閲覧先もジョブIDから決まります。
	wantURI := "gs://bucket-a/reviews/" + testJobID + "/report.json"
	if enq.received.StorageURI != wantURI {
		t.Fatalf("storage uri = %s, want %s", enq.received.StorageURI, wantURI)
	}
	wantURL := "https://service.example.com/history/" + testJobID
	if enq.received.PublicURL != wantURL {
		t.Fatalf("public url = %s, want %s", enq.received.PublicURL, wantURL)
	}
	if body := w.Body.String(); !strings.Contains(body, wantURL) {
		t.Fatalf("response should include the detail URL, body=%q", body)
	}

	// 受付が履歴に残り、一覧のキャッシュが捨てられていること。
	if len(store.saved) != 1 {
		t.Fatalf("記録件数 = %d, want 1", len(store.saved))
	}
	if got := store.saved[0]; got.State != "queued" || got.QueuedAt.IsZero() {
		t.Fatalf("記録内容が想定と違います: %+v", got)
	}
	if history.invalidated != 1 {
		t.Fatalf("キャッシュ破棄の回数 = %d, want 1", history.invalidated)
	}
}

// 記録に失敗しても投入は成立しているため、受付は成功として返します。
func TestHandleReviewSubmit_StatusRecordFailureStillAccepts(t *testing.T) {
	h, enq, store, history := buildTestHandler(t, nil, nil)
	store.err = errors.New("gcs unavailable")

	w := httptest.NewRecorder()
	h.HandleReviewSubmit(w, newSubmitRequest(validFormBody()))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", w.Code)
	}
	if !enq.called {
		t.Fatal("enqueue should be called")
	}
	if history.invalidated != 0 {
		t.Fatal("記録に失敗したらキャッシュは捨てない想定です")
	}
}

func TestHandleReviewSubmit_SuccessPreservesFormValues(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v, err := url.ParseQuery(validFormBody())
	if err != nil {
		t.Fatalf("failed to parse valid form body: %v", err)
	}
	v.Set("repo_url", "git@github.com:org/repo.git")
	v.Set("base_branch", "release/2026-04")
	v.Set("feature_branch", "feature/completion-form")
	v.Set("mode", "article")
	v.Set("model_name", "gemini-2.5-pro")

	h.HandleReviewSubmit(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", w.Code)
	}
	if !enq.called {
		t.Fatal("enqueue should be called on success")
	}
	body := html.UnescapeString(w.Body.String())
	for _, want := range []string{
		`name="repo_url" class="form-control"
                   placeholder="例: git@github.com:user/repo.git"
                   value="git@github.com:org/repo.git"`,
		`name="base_branch" class="form-control"
                       value="release/2026-04"`,
		`name="feature_branch" class="form-control"
                       value="feature/completion-form"`,
		`<option value="article" selected>article (技術記事レビュー)</option>`,
		`<option value="gemini-2.5-pro" selected>gemini-2.5-pro</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("form value should be preserved: want %q body=%s", want, body)
		}
	}
}

func TestHandleReviewSubmit_UsesSelectedModelName(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v, err := url.ParseQuery(validFormBody())
	if err != nil {
		t.Fatalf("failed to parse valid form body: %v", err)
	}
	v.Set("model_name", "gemini-2.5-pro")

	h.HandleReviewSubmit(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", w.Code)
	}
	if enq.received.ModelName != "gemini-2.5-pro" {
		t.Fatalf("unexpected model name: %s", enq.received.ModelName)
	}
}
