package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
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

// --- GET /modes ---

func TestHandleModes_ListsEveryMode(t *testing.T) {
	t.Parallel()

	h := buildHistoryHandler(t, &recordingHistory{})
	rec := httptest.NewRecorder()
	h.HandleModes(rec, jsonRequest(http.MethodGet, "/modes"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}

	got := decodeJSON[modesResponse](t, rec)
	byKey := make(map[string]ModeInfo, len(got.Modes))
	for _, m := range got.Modes {
		byKey[m.Key] = m
	}

	for _, key := range []string{"article", "code", "novel"} {
		mode, ok := byKey[key]
		if !ok {
			t.Errorf("モード %q がありません: %+v", key, got.Modes)
			continue
		}
		// 説明が空だと、呼び出し元はモードを選べません。
		if mode.Label == "" || mode.Direction == "" || mode.UseWhen == "" {
			t.Errorf("%s: 説明が欠けています: %+v", key, mode)
		}
	}

	// excerpt はプロンプト側の宣言をそのまま出します。
	if byKey["code"].Excerpt != string(assets.ExcerptCode) {
		t.Errorf("code の excerpt = %q, want %q", byKey["code"].Excerpt, assets.ExcerptCode)
	}
}

// --- GET /jobs/{jobID} ---

func TestHandleJobStatus_ReturnsStatus(t *testing.T) {
	t.Parallel()

	store := &fakeStatusStore{getStatus: sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)}
	h := buildJobStatusHandler(t, store)

	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleJobStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}

	got := decodeJSON[map[string]any](t, rec)
	// jobstatus.Status を埋め込んでいるので、JSON はフラットに並びます。
	for _, key := range []string{"job_id", "state", "mode", "model_name", "decision"} {
		if _, ok := got[key]; !ok {
			t.Errorf("%q が応答にありません: %v", key, got)
		}
	}
	// 指摘の全文はポーリング先に載せません。
	if _, ok := got["report"]; ok {
		t.Errorf("進行状況に report が含まれています: %v", got)
	}
}

// 「まだ無い」と「読めなかった」を別の状態コードへ割り当てること
// （同一視したときに何が起きるかは recordErrorStatus の doc）。
func TestHandleJobStatus_SeparatesMissingFromUnreadable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "未記録", err: jobstatus.ErrNotFound, want: http.StatusNotFound},
		{name: "読めない", err: jobstatus.ErrUnavailable, want: http.StatusBadGateway},
		{name: "その他", err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := buildJobStatusHandler(t, &fakeStatusStore{getErr: tt.err})
			rec := httptest.NewRecorder()
			req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", "20260810-213000-a1b2c3d4")
			h.HandleJobStatus(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d\n%s", rec.Code, tt.want, rec.Body.String())
			}
			if got := decodeJSON[errorBody](t, rec); got.Error == "" {
				t.Error("エラー本文が空です")
			}
		})
	}
}

func TestHandleJobStatus_RejectsInvalidJobID(t *testing.T) {
	t.Parallel()

	h := buildJobStatusHandler(t, &fakeStatusStore{})
	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", "-bad-id")
	h.HandleJobStatus(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
}

// ジョブ ID はストレージのパス要素になるため、traversal は切り詰めてから使うこと。
//
// Sanitize はエラーにせず末尾要素だけを残します。詳細画面と同じ扱いです。
func TestHandleJobStatus_StripsPathTraversal(t *testing.T) {
	t.Parallel()

	store := &fakeStatusStore{getErr: jobstatus.ErrNotFound}
	h := buildJobStatusHandler(t, store)
	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", "../../etc/passwd")
	h.HandleJobStatus(rec, req)

	// 切り詰めた ID で問い合わせた結果として 404 になります（400 でも 500 でもありません）。
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
}

// --- GET /history ---

func TestHandleHistory_ReturnsJSON(t *testing.T) {
	t.Parallel()

	history := &recordingHistory{page: domain.HistoryPage{
		Items: []domain.JobStatus{sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)},
	}}
	h := buildHistoryHandler(t, history)

	rec := httptest.NewRecorder()
	h.HandleHistory(rec, jsonRequest(http.MethodGet, "/history?page=2&per_page=5"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}

	// 項目名は snake_case です。同じ構造体で往復すると綴りの誤りに気付けないため、
	// 生のキーで確かめます（本文は 1 度しか読めないのでバイト列から 2 通りに解きます）。
	body := rec.Body.Bytes()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("JSON をデコードできません: %v\n%s", err, body)
	}
	for _, key := range []string{"items", "meta"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("%q が応答にありません: %v", key, mapKeys(raw))
		}
	}

	var got domain.HistoryPage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("HistoryPage をデコードできません: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Mode != "code" {
		t.Errorf("items が期待と違います: %+v", got.Items)
	}
	// ページングの指定はそのまま届きます。
	if history.gotPage != 2 || history.gotPerPage != 5 {
		t.Errorf("page/per_page = %d/%d, want 2/5", history.gotPage, history.gotPerPage)
	}
}

// 一覧の読み取り障害を 500 に潰さないこと。
func TestHandleHistory_UnreadableIsBadGateway(t *testing.T) {
	t.Parallel()

	h := buildHistoryHandler(t, &recordingHistory{listErr: jobstatus.ErrUnavailable})
	rec := httptest.NewRecorder()
	h.HandleHistory(rec, jsonRequest(http.MethodGet, "/history"))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502\n%s", rec.Code, rec.Body.String())
	}
}

// --- GET /history/{jobID} ---

func TestHandleReviewDetail_ReturnsStatusAndReport(t *testing.T) {
	t.Parallel()

	history := &recordingHistory{detail: domain.ReviewDetail{
		Status: sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded),
		Report: &review.Report{
			Title:   "認証処理のレビュー",
			Verdict: review.Verdict{Decision: review.DecisionMinor, Reason: "軽微な指摘のみ"},
			Findings: []review.Finding{{
				Severity: review.SeverityMinor,
				File:     "auth.go",
				Excerpt:  "if err != nil {",
				Message:  "エラーを握り潰しています",
			}},
		},
	}}
	h := buildHistoryHandler(t, history)

	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/history/x"), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleReviewDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}

	got := decodeJSON[reviewDetailResponse](t, rec)
	if got.Status.Mode != "code" {
		t.Errorf("status.mode = %q", got.Status.Mode)
	}
	if got.Report == nil {
		t.Fatal("report が nil です")
	}
}

// 成果物が無いジョブでは report が null で返ること。
//
// キーごと落とすと、呼び出し元は「まだ書かれていない」のか「項目名が変わった」のかを
// 区別できません。
func TestHandleReviewDetail_NullReportWhenAbsent(t *testing.T) {
	t.Parallel()

	history := &recordingHistory{detail: domain.ReviewDetail{
		Status: sampleStatus(jobstatus.StateRunning, ""),
	}}
	h := buildHistoryHandler(t, history)

	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/history/x"), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleReviewDetail(rec, req)

	raw := decodeJSON[map[string]json.RawMessage](t, rec)
	report, ok := raw["report"]
	if !ok {
		t.Fatalf("report キーがありません: %v", raw)
	}
	if string(report) != "null" {
		t.Errorf("report = %s, want null", report)
	}
}

// --- DELETE /history/{jobID} ---

func TestHandleReviewDelete_ConflictAsJSON(t *testing.T) {
	t.Parallel()

	history := &recordingHistory{detail: domain.ReviewDetail{
		Status: sampleStatus(jobstatus.StateRunning, ""),
	}}
	h := buildHistoryHandler(t, history)

	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodDelete, "/history/x"), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleReviewDelete(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409\n%s", rec.Code, rec.Body.String())
	}
	if got := decodeJSON[errorBody](t, rec); got.Error == "" {
		t.Error("エラー本文が空です")
	}
	if history.deleteCalls != 0 {
		t.Error("実行中なのに削除が呼ばれました")
	}
}

// --- POST /submit_review (JSON) ---

func newJSONSubmitRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/submit_review", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req
}

func TestHandleReviewSubmit_AcceptsJSON(t *testing.T) {
	t.Parallel()

	h, enq, _, _ := buildTestHandler(t, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleReviewSubmit(rec, newJSONSubmitRequest(`{
		"repo_url": "git@github.com:org/repo.git",
		"base_branch": "main",
		"feature_branch": "develop",
		"mode": "code",
		"model_name": "gemini-2.5-pro"
	}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202\n%s", rec.Code, rec.Body.String())
	}

	got := decodeJSON[submitResponse](t, rec)
	if got.JobID != testJobID {
		t.Errorf("job_id = %q, want %q", got.JobID, testJobID)
	}
	if !strings.HasSuffix(got.DetailURL, testJobID) {
		t.Errorf("detail_url = %q", got.DetailURL)
	}
	if !enq.called || enq.received.Mode != "code" {
		t.Errorf("キューへ渡った内容が違います: %+v", enq.received)
	}
}

// 呼び出し元に保存先を決めさせないこと。
//
// storage_uri を受け付けると、成果物をバケット内の任意のパスへ書かせられます。
// 黙って捨てるのではなくエラーにするのは、送った側が効いたと思い込まないためです。
func TestHandleReviewSubmit_RejectsCallerSuppliedStorageURI(t *testing.T) {
	t.Parallel()

	h, enq, _, _ := buildTestHandler(t, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleReviewSubmit(rec, newJSONSubmitRequest(`{
		"repo_url": "git@github.com:org/repo.git",
		"base_branch": "main",
		"feature_branch": "develop",
		"mode": "code",
		"model_name": "gemini-2.5-pro",
		"storage_uri": "gs://other-bucket/anywhere.json"
	}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
	if enq.called {
		t.Error("不正な入力なのにキューへ投入されました")
	}
}

func TestHandleReviewSubmit_JSONValidationError(t *testing.T) {
	t.Parallel()

	h, enq, _, _ := buildTestHandler(t, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleReviewSubmit(rec, newJSONSubmitRequest(`{
		"repo_url": "https://github.com/org/repo.git",
		"base_branch": "main",
		"feature_branch": "develop",
		"mode": "code",
		"model_name": "gemini-2.5-pro"
	}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
	// 画面ではなく理由だけを返します（HTML が混ざっていないこと）。
	if got := decodeJSON[errorBody](t, rec); got.Error == "" {
		t.Error("エラー本文が空です")
	}
	if enq.called {
		t.Error("不正な入力なのにキューへ投入されました")
	}
}

// フォーム投入は JSON を求めない限り従来どおり HTML を返すこと。
func TestHandleReviewSubmit_FormStillRendersHTML(t *testing.T) {
	t.Parallel()

	h, _, _, _ := buildTestHandler(t, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleReviewSubmit(rec, newSubmitRequest(validFormBody()))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
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

func buildJobStatusHandler(t *testing.T, store domain.StatusStore) *Handler {
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
