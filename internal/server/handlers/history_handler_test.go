package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

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

func buildHistoryHandler(t *testing.T, history domain.HistoryRepository) *Handler {
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
		Status: jobstatus.Status{
			JobID:    "20260810-213000-a1b2c3d4",
			State:    state,
			Title:    "認証処理のレビュー",
			QueuedAt: time.Date(2026, 8, 10, 12, 30, 0, 0, time.UTC),
		},
		Outcome:       outcome,
		RepoURL:       "git@github.com:org/repo.git",
		BaseBranch:    "main",
		FeatureBranch: "develop",
		Mode:          "code",
		ModelName:     "gemini-2.5-pro",
		Decision:      review.DecisionMinor,
	}
}

func TestHandleHistory_RendersRows(t *testing.T) {
	history := &recordingHistory{
		page: domain.HistoryPage{
			Items: []domain.JobStatus{sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)},
			Meta:  domain.PageMeta{Page: 1, PerPage: 20, Total: 1, TotalPages: 1, From: 1, To: 1},
		},
	}

	w := httptest.NewRecorder()
	buildHistoryHandler(t, history).HandleHistory(w, httptest.NewRequest(http.MethodGet, "/history", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	for _, want := range []string{
		"org/repo",
		"main",
		"develop",
		"認証処理のレビュー",
		"gemini-2.5-pro",
		"/history/20260810-213000-a1b2c3d4",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("一覧に %q が含まれていません", want)
		}
	}
}

// 状態はジョブの state と結末の両方で決まります。スキップは succeeded なので、
// state だけを見ると完了と区別できません。
func TestHandleHistory_DistinguishesSkipped(t *testing.T) {
	tests := []struct {
		name    string
		state   jobstatus.State
		outcome review.Status
		want    string
	}{
		{"完了", jobstatus.StateSucceeded, review.StatusSucceeded, "完了"},
		{"スキップ", jobstatus.StateSucceeded, review.StatusSkipped, "スキップ"},
		{"実行中", jobstatus.StateRunning, "", "実行中"},
		{"受付済み", jobstatus.StateQueued, "", "受付済み"},
		{"失敗", jobstatus.StateFailed, review.StatusFailed, "失敗"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &recordingHistory{
				page: domain.HistoryPage{Items: []domain.JobStatus{sampleStatus(tt.state, tt.outcome)}},
			}

			w := httptest.NewRecorder()
			buildHistoryHandler(t, history).HandleHistory(w, httptest.NewRequest(http.MethodGet, "/history", nil))

			if !strings.Contains(w.Body.String(), tt.want) {
				t.Errorf("一覧に %q が含まれていません", tt.want)
			}
		})
	}
}

func TestHandleHistory_PageParams(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantPage    int
		wantPerPage int
	}{
		{"既定", "", 1, defaultPerPage},
		{"指定", "?page=3&per_page=5", 3, 5},
		{"不正値は既定へ倒す", "?page=abc&per_page=-1", 1, defaultPerPage},
		{"上限で頭打ち", "?per_page=1000", 1, maxPerPage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &recordingHistory{}

			w := httptest.NewRecorder()
			buildHistoryHandler(t, history).HandleHistory(w, httptest.NewRequest(http.MethodGet, "/history"+tt.query, nil))

			if history.gotPage != tt.wantPage {
				t.Errorf("page = %d, want %d", history.gotPage, tt.wantPage)
			}
			if history.gotPerPage != tt.wantPerPage {
				t.Errorf("perPage = %d, want %d", history.gotPerPage, tt.wantPerPage)
			}
		})
	}
}

func TestHandleHistory_EmptyState(t *testing.T) {
	w := httptest.NewRecorder()
	buildHistoryHandler(t, &recordingHistory{}).HandleHistory(w, httptest.NewRequest(http.MethodGet, "/history", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "まだレビュー履歴がありません") {
		t.Error("空のときの案内が出ていません")
	}
}

func TestHandleHistory_ListError(t *testing.T) {
	history := &recordingHistory{listErr: errors.New("gcs down")}

	w := httptest.NewRecorder()
	buildHistoryHandler(t, history).HandleHistory(w, httptest.NewRequest(http.MethodGet, "/history", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "gcs down") {
		t.Error("内部のエラー内容が画面へ出ています")
	}
}

// detailRequest は chi のルートパラメータを埋めたリクエストを返します。
//
// パスは固定にし、検証したい値はルートパラメータにだけ入れます。空白などを含む値を
// パスへ埋めると httptest.NewRequest 側が panic し、ハンドラーへ届く前に落ちるためです。
func detailRequest(jobID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/history/x", nil)

	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func TestHandleReviewDetail_RendersReport(t *testing.T) {
	report := review.Report{
		Title:   "認証処理のレビュー",
		Summary: "概ね良好です。",
		Verdict: review.Verdict{Decision: review.DecisionMinor, Reason: "軽微な指摘が1件"},
		Findings: []review.Finding{
			{Severity: review.SeverityMinor, File: "auth.go", Line: 42, Excerpt: "x := 1", Message: "未使用です。", Suggestion: "削除してください。"},
		},
	}
	history := &recordingHistory{
		detail: domain.ReviewDetail{
			Status: sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded),
			Report: &report,
		},
	}

	w := httptest.NewRecorder()
	buildHistoryHandler(t, history).HandleReviewDetail(w, detailRequest("20260810-213000-a1b2c3d4"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	for _, want := range []string{"概ね良好です。", "軽微な指摘が1件", "auth.go:42", "未使用です。", "削除してください。"} {
		if !strings.Contains(body, want) {
			t.Errorf("詳細に %q が含まれていません", want)
		}
	}
}

// 成果物がまだ無い（実行中・失敗・スキップ）場合も、進行状況までは見せます。
func TestHandleReviewDetail_WithoutReport(t *testing.T) {
	history := &recordingHistory{
		detail: domain.ReviewDetail{Status: sampleStatus(jobstatus.StateRunning, "")},
	}

	w := httptest.NewRecorder()
	buildHistoryHandler(t, history).HandleReviewDetail(w, detailRequest("20260810-213000-a1b2c3d4"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "結果がまだありません") {
		t.Error("結果が無いときの案内が出ていません")
	}
}

// ジョブIDはストレージのパス要素になるため、受け取った時点で正規化します。
// jobid.Sanitize は末尾要素だけを取り出すので、パス要素は下流へ渡りません。
func TestHandleReviewDetail_StripsPathTraversal(t *testing.T) {
	history := &recordingHistory{}

	w := httptest.NewRecorder()
	buildHistoryHandler(t, history).HandleReviewDetail(w, detailRequest("../../etc/passwd"))

	if strings.ContainsAny(history.gotJobID, "/.") {
		t.Fatalf("パス要素が下流へ渡っています: %q", history.gotJobID)
	}
	if history.gotJobID != "passwd" {
		t.Errorf("正規化後のID = %q, want %q", history.gotJobID, "passwd")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// 正規化しても形式を満たさない値は弾きます。
func TestHandleReviewDetail_RejectsInvalidJobID(t *testing.T) {
	tests := []struct {
		name  string
		jobID string
	}{
		{"空", " "},
		{"記号始まり", "-bad-id"},
		{"使えない文字", "job$id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &recordingHistory{}

			w := httptest.NewRecorder()
			buildHistoryHandler(t, history).HandleReviewDetail(w, detailRequest(tt.jobID))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if history.gotJobID != "" {
				t.Errorf("不正なIDでストレージを読みにいっています: %q", history.gotJobID)
			}
		})
	}
}

func TestHandleReviewDetail_NotFound(t *testing.T) {
	history := &recordingHistory{getErr: jobstatus.ErrNotFound}

	w := httptest.NewRecorder()
	buildHistoryHandler(t, history).HandleReviewDetail(w, detailRequest("20260810-213000-a1b2c3d4"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleReviewDetail_GetError(t *testing.T) {
	history := &recordingHistory{getErr: errors.New("gcs down")}

	w := httptest.NewRecorder()
	buildHistoryHandler(t, history).HandleReviewDetail(w, detailRequest("20260810-213000-a1b2c3d4"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "gcs down") {
		t.Error("内部のエラー内容が画面へ出ています")
	}
}

func deleteRequest(jobID string) *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/history/x", nil)

	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func TestHandleReviewDelete(t *testing.T) {
	history := &recordingHistory{
		detail: domain.ReviewDetail{Status: sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)},
	}

	w := httptest.NewRecorder()
	buildHistoryHandler(t, history).HandleReviewDelete(w, deleteRequest("20260810-213000-a1b2c3d4"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if history.deleteCalls != 1 {
		t.Errorf("削除回数 = %d, want 1", history.deleteCalls)
	}
	if history.deletedJobID != "20260810-213000-a1b2c3d4" {
		t.Errorf("削除対象 = %q", history.deletedJobID)
	}
}

// 実行中のものを消すと、ワーカーがあとから status.json を書き戻して復活します。
// 画面にボタンが出ていなくても、直接呼ばれた場合に弾けること。
func TestHandleReviewDeleteRejectsRunning(t *testing.T) {
	tests := []struct {
		name  string
		state jobstatus.State
	}{
		{"受付済み", jobstatus.StateQueued},
		{"実行中", jobstatus.StateRunning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &recordingHistory{
				detail: domain.ReviewDetail{Status: sampleStatus(tt.state, "")},
			}

			w := httptest.NewRecorder()
			buildHistoryHandler(t, history).HandleReviewDelete(w, deleteRequest("20260810-213000-a1b2c3d4"))

			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409", w.Code)
			}
			if history.deleteCalls != 0 {
				t.Errorf("削除が実行されています: %d 回", history.deleteCalls)
			}
		})
	}
}

func TestHandleReviewDeleteErrors(t *testing.T) {
	tests := []struct {
		name     string
		jobID    string
		history  *recordingHistory
		wantCode int
	}{
		{
			name:     "不正なジョブID",
			jobID:    "-bad-id",
			history:  &recordingHistory{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "見つからない",
			jobID:    "20260810-213000-a1b2c3d4",
			history:  &recordingHistory{getErr: jobstatus.ErrNotFound},
			wantCode: http.StatusNotFound,
		},
		{
			name:  "削除に失敗",
			jobID: "20260810-213000-a1b2c3d4",
			history: &recordingHistory{
				detail:    domain.ReviewDetail{Status: sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)},
				deleteErr: errors.New("gcs down"),
			},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			buildHistoryHandler(t, tt.history).HandleReviewDelete(w, deleteRequest(tt.jobID))

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantCode)
			}
			if strings.Contains(w.Body.String(), "gcs down") {
				t.Error("内部のエラー内容が応答に出ています")
			}
		})
	}
}

// 削除できる状態のときだけボタンを描くこと。実行中に出すと押せてしまいます。
func TestReviewDetailShowsDeleteButtonOnlyWhenDeletable(t *testing.T) {
	tests := []struct {
		name  string
		state jobstatus.State
		want  bool
	}{
		{"完了", jobstatus.StateSucceeded, true},
		{"失敗", jobstatus.StateFailed, true},
		{"実行中", jobstatus.StateRunning, false},
		{"受付済み", jobstatus.StateQueued, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &recordingHistory{
				detail: domain.ReviewDetail{Status: sampleStatus(tt.state, "")},
			}

			w := httptest.NewRecorder()
			buildHistoryHandler(t, history).HandleReviewDetail(w, detailRequest("20260810-213000-a1b2c3d4"))

			if got := strings.Contains(w.Body.String(), "delete-review-btn"); got != tt.want {
				t.Errorf("削除ボタンの表示 = %v, want %v", got, tt.want)
			}
		})
	}
}

// 削除ボタンと一緒に、送信に使う CSRF トークンが実際に埋まること。
//
// ボタンの有無だけを見ていたため、トークンが空のまま描画される不具合を見逃していました。
// 空だと X-CSRF-Token に空文字が載り、削除が 403 で必ず失敗します。ミドルウェアを
// 通したうえで値の中身まで確かめます。
func TestReviewDetailRendersCSRFTokenForDelete(t *testing.T) {
	authHandler, err := auth.NewHandler(auth.Config{
		ClientID:          "client-id",
		ClientSecret:      "client-secret",
		RedirectURL:       "https://service.example.com/auth/callback",
		SessionAuthKey:    "1234567890abcdef",
		SessionEncryptKey: "1234567890123456",
		SessionName:       "test-session",
		AllowedEmails:     []string{"tester@example.com"},
	})
	if err != nil {
		t.Fatalf("auth.NewHandler() error = %v", err)
	}

	history := &recordingHistory{
		detail: domain.ReviewDetail{Status: sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)},
	}
	handler := authHandler.CSRFContextMiddleware(
		http.HandlerFunc(buildHistoryHandler(t, history).HandleReviewDetail),
	)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, detailRequest("20260810-213000-a1b2c3d4"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	matched := regexp.MustCompile(`id="csrf_token" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if matched == nil {
		t.Fatalf("CSRFトークンが空のまま描画されています: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "delete-review-btn") {
		t.Error("削除ボタンが描画されていません")
	}
}
