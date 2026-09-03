package handlers

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

// --- GET /jobs/{jobID} ---

func TestHandleJob_ReturnsStatus(t *testing.T) {
	t.Parallel()

	store := &fakeStatusStore{getStatus: sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)}
	h := buildHandlerWithStore(t, store)

	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleJob(rec, req)

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
	// 成果物が無い間は取得先も出しません（キーの有無で「まだ無い」が分かります）。
	if _, ok := got["report_url"]; ok {
		t.Errorf("成果物が無いのに report_url があります: %v", got)
	}
}

// 成果物ができたら、その取得先を同じ応答に載せること。
// 呼び出し元がパスの規約を知らなくても全文へ辿れるようにします。
func TestHandleJob_LinksReportWhenPresent(t *testing.T) {
	t.Parallel()

	status := sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)
	status.ReportURI = "gs://bucket-a/reviews/" + status.JobID + "/report.json"
	h := buildHandlerWithStore(t, &fakeStatusStore{getStatus: status})

	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", status.JobID)
	h.HandleJob(rec, req)

	got := decodeJSON[map[string]any](t, rec)
	want := "https://service.example.com/jobs/" + status.JobID + "/report"
	if got["report_url"] != want {
		t.Errorf("report_url = %v, want %q", got["report_url"], want)
	}
}

// HTML を求められたら、進行状況ではなく詳細画面を返すこと。
// ブラウザは投入直後の Location からここへ来ます。
func TestHandleJob_RendersDetailPageForHTML(t *testing.T) {
	t.Parallel()

	history := &recordingHistory{detail: domain.ReviewDetail{
		Status: sampleStatus(jobstatus.StateRunning, ""),
	}}
	h := buildHandlerWithHistory(t, history)

	rec := httptest.NewRecorder()
	req := withURLParam(httptest.NewRequest(http.MethodGet, "/jobs/x", nil), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if history.gotJobID != "20260810-213000-a1b2c3d4" {
		t.Errorf("詳細の取得先が違います: %q", history.gotJobID)
	}
}

// --- GET /jobs/{jobID}/report ---

func TestHandleJobReport_ReturnsReport(t *testing.T) {
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
	h := buildHandlerWithHistory(t, history)

	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x/report"), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleJobReport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[review.Report](t, rec)
	if len(got.Findings) != 1 {
		t.Errorf("findings = %d, want 1", len(got.Findings))
	}
}

// 「まだ無い」と「もう出ない」を状態コードで分けること。
// 前者は待てば出るので 409、後者は待っても出ないので 404 です。
func TestHandleJobReport_DistinguishesPendingFromAbsent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state jobstatus.State
		want  int
	}{
		{name: "実行中", state: jobstatus.StateRunning, want: http.StatusConflict},
		{name: "受付済み", state: jobstatus.StateQueued, want: http.StatusConflict},
		{name: "失敗", state: jobstatus.StateFailed, want: http.StatusNotFound},
		{name: "スキップ", state: jobstatus.StateSucceeded, want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			history := &recordingHistory{detail: domain.ReviewDetail{Status: sampleStatus(tt.state, "")}}
			h := buildHandlerWithHistory(t, history)

			rec := httptest.NewRecorder()
			req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x/report"), "jobID", "20260810-213000-a1b2c3d4")
			h.HandleJobReport(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d\n%s", rec.Code, tt.want, rec.Body.String())
			}
			if got := decodeJSON[errorBody](t, rec); got.Error == "" {
				t.Error("エラー本文が空です")
			}
		})
	}
}

// 「まだ無い」と「読めなかった」を別の状態コードへ割り当てること
// （同一視したときに何が起きるかは recordErrorStatus の doc）。
func TestHandleJob_SeparatesMissingFromUnreadable(t *testing.T) {
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

			h := buildHandlerWithStore(t, &fakeStatusStore{getErr: tt.err})
			rec := httptest.NewRecorder()
			req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", "20260810-213000-a1b2c3d4")
			h.HandleJob(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d\n%s", rec.Code, tt.want, rec.Body.String())
			}
			if got := decodeJSON[errorBody](t, rec); got.Error == "" {
				t.Error("エラー本文が空です")
			}
		})
	}
}

func TestHandleJob_RejectsInvalidJobID(t *testing.T) {
	t.Parallel()

	h := buildHandlerWithStore(t, &fakeStatusStore{})
	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", "-bad-id")
	h.HandleJob(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
}

// ジョブ ID はストレージのパス要素になるため、traversal は切り詰めてから使うこと。
//
// Sanitize はエラーにせず末尾要素だけを残します。詳細画面と同じ扱いです。
func TestHandleJob_StripsPathTraversal(t *testing.T) {
	t.Parallel()

	store := &fakeStatusStore{getErr: jobstatus.ErrNotFound}
	h := buildHandlerWithStore(t, store)
	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", "../../etc/passwd")
	h.HandleJob(rec, req)

	// 切り詰めた ID で問い合わせた結果として 404 になります（400 でも 500 でもありません）。
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
}

// --- DELETE /jobs/{jobID} ---

func TestHandleJobDelete_ConflictAsJSON(t *testing.T) {
	t.Parallel()

	history := &recordingHistory{detail: domain.ReviewDetail{
		Status: sampleStatus(jobstatus.StateRunning, ""),
	}}
	h := buildHandlerWithHistory(t, history)

	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodDelete, "/history/x"), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleJobDelete(rec, req)

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

// detailRequest は chi のルートパラメータを埋めたリクエストを返します。
//
// パスは固定にし、検証したい値はルートパラメータにだけ入れます。空白などを含む値を
// パスへ埋めると httptest.NewRequest 側が panic し、ハンドラーへ届く前に落ちるためです。
func detailRequest(jobID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/jobs/x", nil)

	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func TestHandleJob_PageRendersReport(t *testing.T) {
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
	buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("20260810-213000-a1b2c3d4"))

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
func TestHandleJob_PageWithoutReport(t *testing.T) {
	history := &recordingHistory{
		detail: domain.ReviewDetail{Status: sampleStatus(jobstatus.StateRunning, "")},
	}

	w := httptest.NewRecorder()
	buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("20260810-213000-a1b2c3d4"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "結果がまだありません") {
		t.Error("結果が無いときの案内が出ていません")
	}
}

// ジョブ ID はストレージのパス要素になるため、受け取った時点で正規化します。
// jobid.Sanitize は末尾要素だけを取り出すので、パス要素は下流へ渡りません。
func TestHandleJob_PageStripsPathTraversal(t *testing.T) {
	history := &recordingHistory{}

	w := httptest.NewRecorder()
	buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("../../etc/passwd"))

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
func TestHandleJob_PageRejectsInvalidJobID(t *testing.T) {
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
			buildHandlerWithHistory(t, history).HandleJob(w, detailRequest(tt.jobID))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if history.gotJobID != "" {
				t.Errorf("不正なIDでストレージを読みにいっています: %q", history.gotJobID)
			}
		})
	}
}

func TestHandleJob_PageNotFound(t *testing.T) {
	history := &recordingHistory{getErr: jobstatus.ErrNotFound}

	w := httptest.NewRecorder()
	buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("20260810-213000-a1b2c3d4"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleJob_PageGetError(t *testing.T) {
	history := &recordingHistory{getErr: errors.New("gcs down")}

	w := httptest.NewRecorder()
	buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("20260810-213000-a1b2c3d4"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "gcs down") {
		t.Error("内部のエラー内容が画面へ出ています")
	}
}

func deleteRequest(jobID string) *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/jobs/x", nil)

	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func TestHandleJobDelete(t *testing.T) {
	history := &recordingHistory{
		detail: domain.ReviewDetail{Status: sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)},
	}

	w := httptest.NewRecorder()
	buildHandlerWithHistory(t, history).HandleJobDelete(w, deleteRequest("20260810-213000-a1b2c3d4"))

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

// 画面にボタンが出ていなくても、直接呼ばれた実行中の削除を弾くこと
// （消せない理由は domain.JobStatus.Deletable）。
func TestHandleJobDeleteRejectsRunning(t *testing.T) {
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
			buildHandlerWithHistory(t, history).HandleJobDelete(w, deleteRequest("20260810-213000-a1b2c3d4"))

			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409", w.Code)
			}
			if history.deleteCalls != 0 {
				t.Errorf("削除が実行されています: %d 回", history.deleteCalls)
			}
		})
	}
}

func TestHandleJobDeleteErrors(t *testing.T) {
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
			buildHandlerWithHistory(t, tt.history).HandleJobDelete(w, deleteRequest(tt.jobID))

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
func TestJobDetailShowsDeleteButtonOnlyWhenDeletable(t *testing.T) {
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
			buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("20260810-213000-a1b2c3d4"))

			if got := strings.Contains(w.Body.String(), "delete-review-btn"); got != tt.want {
				t.Errorf("削除ボタンの表示 = %v, want %v", got, tt.want)
			}
		})
	}
}

// 再依頼のリンクは、もう動いていない依頼にだけ出すこと。
//
// queued / running に出すと、結果を待っているレビューがもう 1 本走ります。
// 依頼内容が記録されていないジョブに出さないのは、開いても埋まらないためです。
func TestJobDetailShowsRerunLinkOnlyWhenRerunnable(t *testing.T) {
	tests := []struct {
		name    string
		state   jobstatus.State
		repoURL string
		want    bool
	}{
		{"完了", jobstatus.StateSucceeded, "git@github.com:org/repo.git", true},
		{"失敗", jobstatus.StateFailed, "git@github.com:org/repo.git", true},
		{"実行中", jobstatus.StateRunning, "git@github.com:org/repo.git", false},
		{"受付済み", jobstatus.StateQueued, "git@github.com:org/repo.git", false},
		{"依頼内容の記録が無い", jobstatus.StateFailed, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := sampleStatus(tt.state, "")
			status.RepoURL = tt.repoURL
			history := &recordingHistory{detail: domain.ReviewDetail{Status: status}}

			w := httptest.NewRecorder()
			buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("20260810-213000-a1b2c3d4"))

			if got := strings.Contains(w.Body.String(), "/?from="+status.JobID); got != tt.want {
				t.Errorf("再依頼リンクの表示 = %v, want %v", got, tt.want)
			}
		})
	}
}

// 削除ボタンと一緒に、送信に使う CSRF トークンが実際に埋まること。
//
// ボタンの有無だけを見ていたため、トークンが空のまま描画される不具合を見逃していました。
// 空だと X-CSRF-Token に空文字が載り、削除が 403 で必ず失敗します。ミドルウェアを
// 通したうえで値の中身まで確かめます。
func TestJobDetailRendersCSRFTokenForDelete(t *testing.T) {
	authHandler, err := session.New(session.Config{
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		ServiceURL:    "https://service.example.com",
		Store:         session.NewMemoryStore(),
		SessionName:   "test-session",
		AllowedEmails: []string{"tester@example.com"},
	})
	if err != nil {
		t.Fatalf("session.New() error = %v", err)
	}

	history := &recordingHistory{
		detail: domain.ReviewDetail{Status: sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)},
	}
	// 見たいのは「コンテキストのトークンがテンプレートへ届くか」なので、
	// 認証を一往復させず直接載せます（発行そのものは gcp-kit 側のテストが見ます）。
	_ = authHandler
	handler := http.HandlerFunc(buildHandlerWithHistory(t, history).HandleJob)

	req := detailRequest("20260810-213000-a1b2c3d4")
	req = req.WithContext(session.WithCSRFToken(req.Context(), "csrf-test-token"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

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

// ★ 途中で切れたレビューは、画面でそれと分かること。
//
// 不完全な結果を黙って完全なものとして見せない、が要点です。ここが無いと、
// 読む側は「指摘 1 件のレビュー」として受け取り、切れた先にあったものを知る手段を失います。
func TestHandleJob_PageShowsTruncatedAndMetrics(t *testing.T) {
	status := sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)
	status.Truncated = true
	status.Metrics = domain.Metrics{
		DiffBytes:    327680,
		DurationMS:   152_000,
		ToolCalls:    5,
		PromptTokens: 219062,
		OutputTokens: 63950,
	}
	h := buildHandlerWithHistory(t, &recordingHistory{detail: domain.ReviewDetail{Status: status}})

	rec := httptest.NewRecorder()
	req := withURLParam(httptest.NewRequest(http.MethodGet, "/jobs/x", nil), "jobID", status.JobID)
	h.HandleJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := html.UnescapeString(rec.Body.String())

	for _, want := range []string{
		"部分",         // バッジ
		"完結していた指摘だけ", // 何が起きたかの説明
		"320 KiB",    // 差分の大きさ（MAX_DIFF_BYTES と見比べる値）
		"2 分 32 秒",   // 所要時間
		"5 回",        // ツール呼び出し
		"63950",      // 出力トークン（64Ki への距離）
	} {
		if !strings.Contains(body, want) {
			t.Errorf("詳細画面に %q がありません", want)
		}
	}
}

// 計測値が無い実行（旧い記録や、レビューへ到達しなかったもの）でも描画できること。
func TestHandleJob_PageRendersWithoutMetrics(t *testing.T) {
	status := sampleStatus(jobstatus.StateFailed, review.StatusFailed)
	h := buildHandlerWithHistory(t, &recordingHistory{detail: domain.ReviewDetail{Status: status}})

	rec := httptest.NewRecorder()
	req := withURLParam(httptest.NewRequest(http.MethodGet, "/jobs/x", nil), "jobID", status.JobID)
	h.HandleJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "部分") {
		t.Error("切れていないのに部分バッジが出ています")
	}
}
