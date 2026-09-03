package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

// jsonRequest は Accept: application/json を付けたリクエストを作ります。
func jsonRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Accept", "application/json")
	return req
}

// withURLParam は chi の URL パラメータを載せます（ルーターを通さずハンドラを直接呼ぶため）。
func withURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("JSON をデコードできません: %v\n--- body ---\n%s", err, rec.Body.String())
	}
	return out
}

// --- helpers ---

// mapKeys は、エラーメッセージ用にキーだけを並べます。
func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func buildHandlerWithStore(t *testing.T, store domain.StatusStore) *Handler {
	t.Helper()

	h, err := NewHandler(Deps{
		Config:       &config.Config{Server: config.ServerConfig{ServiceURL: "https://service.example.com"}},
		TaskEnqueuer: &fakeEnqueuer{},
		Layout:       domain.NewStorageLayout("bucket-a"),
		StatusStore:  store,
		History:      &recordingHistory{},
	})
	if err != nil {
		t.Fatalf("failed to build handler: %v", err)
	}
	return h
}

// errorBody は、エラー応答の本文を読むためのテスト用の型です。
//
// gcp-kit の respond.ErrorJSON が返す形（{"error": "..."}）に合わせてあります。
// キット側は本文の型を公開していないので、読む側でだけ持ちます。
type errorBody struct {
	Error string `json:"error"`
}

// recordingHistory は履歴のフェイクです。要求されたページ番号も記録します。
type recordingHistory struct {
	page    domain.HistoryPage
	detail  domain.ReviewDetail
	listErr error
	getErr  error

	deleteErr error

	gotPage      int
	gotPerPage   int
	gotJobID     string
	deletedJobID string
	deleteCalls  int
}

func (h *recordingHistory) List(_ context.Context, page, perPage int) (domain.HistoryPage, error) {
	h.gotPage, h.gotPerPage = page, perPage
	if h.listErr != nil {
		return domain.HistoryPage{}, h.listErr
	}
	return h.page, nil
}

func (h *recordingHistory) Get(_ context.Context, jobID string) (domain.ReviewDetail, error) {
	h.gotJobID = jobID
	if h.getErr != nil {
		return domain.ReviewDetail{}, h.getErr
	}
	return h.detail, nil
}

func (h *recordingHistory) Delete(_ context.Context, jobID string) error {
	h.deleteCalls++
	h.deletedJobID = jobID
	return h.deleteErr
}

func (h *recordingHistory) Invalidate() {}

func buildHandlerWithHistory(t *testing.T, history domain.HistoryRepository) *Handler {
	t.Helper()

	h, err := NewHandler(Deps{
		Config:       &config.Config{Server: config.ServerConfig{ServiceURL: "https://service.example.com"}},
		TaskEnqueuer: &fakeEnqueuer{},
		Layout:       domain.NewStorageLayout("bucket-a"),
		StatusStore:  &fakeStatusStore{},
		History:      history,
	})
	if err != nil {
		t.Fatalf("failed to build handler: %v", err)
	}
	return h
}

func sampleStatus(state jobstatus.State, outcome review.Status) domain.JobStatus {
	return domain.JobStatus{
		JobID:         "20260810-213000-a1b2c3d4",
		State:         state,
		Title:         "認証処理のレビュー",
		QueuedAt:      time.Date(2026, 8, 10, 12, 30, 0, 0, time.UTC),
		Outcome:       outcome,
		RepoURL:       "git@github.com:org/repo.git",
		BaseBranch:    "main",
		FeatureBranch: "develop",
		Mode:          "code",
		ModelName:     "gemini-2.5-pro",
		Decision:      review.DecisionMinor,
	}
}

// fakeStatusStore は進行状況の記録先のフェイクです。
type fakeStatusStore struct {
	err   error
	saved []domain.JobStatus

	// getStatus / getErr は Get の応答です。getErr が nil のときだけ getStatus を返します。
	getStatus domain.JobStatus
	getErr    error
}

func (f *fakeStatusStore) Get(_ context.Context, _ string) (domain.JobStatus, error) {
	if f.getErr != nil {
		return domain.JobStatus{}, f.getErr
	}
	if f.getStatus.JobID == "" {
		return domain.JobStatus{}, errors.New("not recorded")
	}
	return f.getStatus, nil
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

// testJobID は、テストで採番されるジョブ ID です。
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
