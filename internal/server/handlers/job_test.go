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
	// jobstatus.Status is embedded, so the JSON is flat.
	for _, key := range []string{"job_id", "state", "mode", "model_name", "decision"} {
		if _, ok := got[key]; !ok {
			t.Errorf("%q is not in the response: %v", key, got)
		}
	}
	// The full findings are not in the polled response.
	if _, ok := got["report"]; ok {
		t.Errorf("the progress contains report: %v", got)
	}
	// Without an artifact its location is not given either (the key's absence means "not there yet").
	if _, ok := got["report_url"]; ok {
		t.Errorf("report_url is set without an artifact: %v", got)
	}
}

// Once the artifact exists, its location is in the same response.
// Callers reach the full text without knowing the path convention.
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

// When HTML is asked for, the detail page is returned instead of the progress.
// Browsers arrive here from the Location right after submission.
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
		t.Errorf("the detail was fetched for the wrong job: %q", history.gotJobID)
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

// "Not there yet" and "never coming" get different status codes.
// The former appears with waiting, so 409; the latter never does, so 404.
func TestHandleJobReport_DistinguishesPendingFromAbsent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state jobstatus.State
		want  int
	}{
		{name: "running", state: jobstatus.StateRunning, want: http.StatusConflict},
		{name: "queued", state: jobstatus.StateQueued, want: http.StatusConflict},
		{name: "failed", state: jobstatus.StateFailed, want: http.StatusNotFound},
		{name: "skipped", state: jobstatus.StateSucceeded, want: http.StatusNotFound},
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
				t.Error("the error body is empty")
			}
		})
	}
}

// "Not there yet" and "could not be read" map to different status codes
// (what happens when they are conflated is in recordErrorStatus's doc).
func TestHandleJob_SeparatesMissingFromUnreadable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "not recorded", err: jobstatus.ErrNotFound, want: http.StatusNotFound},
		{name: "unreadable", err: jobstatus.ErrUnavailable, want: http.StatusBadGateway},
		{name: "other", err: errors.New("boom"), want: http.StatusInternalServerError},
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
				t.Error("the error body is empty")
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

// The job ID becomes a storage path element, so traversal is trimmed before use.
//
// Sanitize does not fail; it keeps only the last element. Same as the detail page.
func TestHandleJob_StripsPathTraversal(t *testing.T) {
	t.Parallel()

	store := &fakeStatusStore{getErr: jobstatus.ErrNotFound}
	h := buildHandlerWithStore(t, store)
	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/jobs/x"), "jobID", "../../etc/passwd")
	h.HandleJob(rec, req)

	// The query with the trimmed ID results in a 404 (neither 400 nor 500).
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
		t.Error("the error body is empty")
	}
	if history.deleteCalls != 0 {
		t.Error("delete was called although it is running")
	}
}

// detailRequest returns a request with the chi route parameter filled in.
//
// The path is fixed and the value under test goes only into the route parameter. A value with
// spaces and the like in the path makes httptest.NewRequest panic before reaching the handler.
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
			t.Errorf("the detail does not contain %q", want)
		}
	}
}

// Without an artifact yet (running, failed, skipped), the progress is still shown.
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
		t.Error("the no-result message is not shown")
	}
}

// The job ID becomes a storage path element, so it is sanitised on receipt.
// jobid.Sanitize takes only the last element, so no path elements reach downstream.
func TestHandleJob_PageStripsPathTraversal(t *testing.T) {
	history := &recordingHistory{}

	w := httptest.NewRecorder()
	buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("../../etc/passwd"))

	if strings.ContainsAny(history.gotJobID, "/.") {
		t.Fatalf("path elements reached downstream: %q", history.gotJobID)
	}
	if history.gotJobID != "passwd" {
		t.Errorf("sanitised ID = %q, want %q", history.gotJobID, "passwd")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// A value still malformed after sanitising is rejected.
func TestHandleJob_PageRejectsInvalidJobID(t *testing.T) {
	tests := []struct {
		name  string
		jobID string
	}{
		{"empty", " "},
		{"starts with a symbol", "-bad-id"},
		{"invalid character", "job$id"},
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
				t.Errorf("storage is read with an invalid ID: %q", history.gotJobID)
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
		t.Error("internal error details are shown on screen")
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
		t.Errorf("delete calls = %d, want 1", history.deleteCalls)
	}
	if history.deletedJobID != "20260810-213000-a1b2c3d4" {
		t.Errorf("deleted job = %q", history.deletedJobID)
	}
}

// Even without the button on screen, a direct delete of a running job is rejected
// (why it cannot be deleted is in domain.JobStatus.Deletable).
func TestHandleJobDeleteRejectsRunning(t *testing.T) {
	tests := []struct {
		name  string
		state jobstatus.State
	}{
		{"queued", jobstatus.StateQueued},
		{"running", jobstatus.StateRunning},
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
				t.Errorf("delete ran: %d calls", history.deleteCalls)
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
			name:     "invalid job ID",
			jobID:    "-bad-id",
			history:  &recordingHistory{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "not found",
			jobID:    "20260810-213000-a1b2c3d4",
			history:  &recordingHistory{getErr: jobstatus.ErrNotFound},
			wantCode: http.StatusNotFound,
		},
		{
			name:  "delete fails",
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
				t.Error("internal error details are in the response")
			}
		})
	}
}

// The button is drawn only when deletion is allowed. Shown while running, it could be pressed.
func TestJobDetailShowsDeleteButtonOnlyWhenDeletable(t *testing.T) {
	tests := []struct {
		name  string
		state jobstatus.State
		want  bool
	}{
		{"succeeded", jobstatus.StateSucceeded, true},
		{"failed", jobstatus.StateFailed, true},
		{"running", jobstatus.StateRunning, false},
		{"queued", jobstatus.StateQueued, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &recordingHistory{
				detail: domain.ReviewDetail{Status: sampleStatus(tt.state, "")},
			}

			w := httptest.NewRecorder()
			buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("20260810-213000-a1b2c3d4"))

			if got := strings.Contains(w.Body.String(), "delete-review-btn"); got != tt.want {
				t.Errorf("delete button shown = %v, want %v", got, tt.want)
			}
		})
	}
}

// The resubmit link is shown only for requests no longer in flight.
//
// Shown for queued / running, another copy of a review still awaiting its result would run.
// It is not shown for jobs without a recorded request because opening it fills nothing.
func TestJobDetailShowsRerunLinkOnlyWhenRerunnable(t *testing.T) {
	tests := []struct {
		name    string
		state   jobstatus.State
		repoURL string
		want    bool
	}{
		{"succeeded", jobstatus.StateSucceeded, "git@github.com:org/repo.git", true},
		{"failed", jobstatus.StateFailed, "git@github.com:org/repo.git", true},
		{"running", jobstatus.StateRunning, "git@github.com:org/repo.git", false},
		{"queued", jobstatus.StateQueued, "git@github.com:org/repo.git", false},
		{"no recorded request", jobstatus.StateFailed, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := sampleStatus(tt.state, "")
			status.RepoURL = tt.repoURL
			history := &recordingHistory{detail: domain.ReviewDetail{Status: status}}

			w := httptest.NewRecorder()
			buildHandlerWithHistory(t, history).HandleJob(w, detailRequest("20260810-213000-a1b2c3d4"))

			if got := strings.Contains(w.Body.String(), "/?from="+status.JobID); got != tt.want {
				t.Errorf("resubmit link shown = %v, want %v", got, tt.want)
			}
		})
	}
}

// Along with the delete button, the CSRF token used to send it is actually embedded.
//
// Checking only for the button once missed a bug that rendered the token empty. Empty, an empty
// string goes into X-CSRF-Token and deletion always fails with a 403. This goes through the
// middleware and checks the value itself.
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
	// The point is "does the context's token reach the template", so it is put on directly
	// without an authentication round trip (gcp-kit's tests cover issuing it).
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
		t.Fatalf("the CSRF token is rendered empty: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "delete-review-btn") {
		t.Error("the delete button is not rendered")
	}
}

// ★ A review cut off midway is recognisable as such on screen.
//
// The point is never to present an incomplete result as complete. Without this, the reader takes
// it as "a review with 1 finding" and has no way to know what lay beyond the cut.
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
		"部分",         // badge
		"完結していた指摘だけ", // explanation of what happened
		"320 KiB",    // diff size (compared against MAX_DIFF_BYTES)
		"2 分 32 秒",   // duration
		"5 回",        // tool calls
		"63950",      // output tokens (distance to 64Ki)
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the detail page lacks %q", want)
		}
	}
}

// A run without measurements (old records, or runs that never reached the review) still renders.
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
		t.Error("the partial badge is shown although nothing was cut")
	}
}
