package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

// --- GET /jobs ---

func TestHandleJobList_ReturnsJSON(t *testing.T) {
	t.Parallel()

	history := &recordingHistory{page: domain.HistoryPage{
		Items: []domain.JobStatus{sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)},
	}}
	h := buildHandlerWithHistory(t, history)

	rec := httptest.NewRecorder()
	h.HandleJobList(rec, jsonRequest(http.MethodGet, "/history?page=2&per_page=5"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}

	// Field names are snake_case. A round trip through the same struct cannot catch a misspelling,
	// so the raw keys are checked (the body can be read once, so the bytes are decoded two ways).
	body := rec.Body.Bytes()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("cannot decode the JSON: %v\n%s", err, body)
	}
	for _, key := range []string{"items", "meta"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("%q is not in the response: %v", key, mapKeys(raw))
		}
	}

	var got domain.HistoryPage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("cannot decode the HistoryPage: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Mode != "code" {
		t.Errorf("unexpected items: %+v", got.Items)
	}
	// The paging parameters arrive unchanged.
	if history.gotPage != 2 || history.gotPerPage != 5 {
		t.Errorf("page/per_page = %d/%d, want 2/5", history.gotPage, history.gotPerPage)
	}
}

// A read failure of the list is not flattened into a 500.
func TestHandleJobList_UnreadableIsBadGateway(t *testing.T) {
	t.Parallel()

	h := buildHandlerWithHistory(t, &recordingHistory{listErr: jobstatus.ErrUnavailable})
	rec := httptest.NewRecorder()
	h.HandleJobList(rec, jsonRequest(http.MethodGet, "/history"))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502\n%s", rec.Code, rec.Body.String())
	}
}

func TestHandleJobList_RendersRows(t *testing.T) {
	history := &recordingHistory{
		page: domain.HistoryPage{
			Items: []domain.JobStatus{sampleStatus(jobstatus.StateSucceeded, review.StatusSucceeded)},
			Meta:  domain.PageMeta{Page: 1, PerPage: 20, Total: 1, TotalPages: 1, From: 1, To: 1},
		},
	}

	w := httptest.NewRecorder()
	buildHandlerWithHistory(t, history).HandleJobList(w, httptest.NewRequest(http.MethodGet, "/jobs", nil))

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
		"/jobs/20260810-213000-a1b2c3d4",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the list does not contain %q", want)
		}
	}
}

// The status depends on both the job's state and its outcome. A skip is succeeded, so state
// alone cannot tell it from completion.
func TestHandleJobList_DistinguishesSkipped(t *testing.T) {
	tests := []struct {
		name    string
		state   jobstatus.State
		outcome review.Status
		want    string
	}{
		{"succeeded", jobstatus.StateSucceeded, review.StatusSucceeded, "完了"},
		{"skipped", jobstatus.StateSucceeded, review.StatusSkipped, "スキップ"},
		{"running", jobstatus.StateRunning, "", "実行中"},
		{"queued", jobstatus.StateQueued, "", "受付済み"},
		{"failed", jobstatus.StateFailed, review.StatusFailed, "失敗"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &recordingHistory{
				page: domain.HistoryPage{Items: []domain.JobStatus{sampleStatus(tt.state, tt.outcome)}},
			}

			w := httptest.NewRecorder()
			buildHandlerWithHistory(t, history).HandleJobList(w, httptest.NewRequest(http.MethodGet, "/jobs", nil))

			if !strings.Contains(w.Body.String(), tt.want) {
				t.Errorf("the list does not contain %q", tt.want)
			}
		})
	}
}

func TestHandleJobList_PageParams(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantPage    int
		wantPerPage int
	}{
		{"default", "", 1, defaultPerPage},
		{"given", "?page=3&per_page=5", 3, 5},
		{"invalid falls back to default", "?page=abc&per_page=-1", 1, defaultPerPage},
		{"capped at the limit", "?per_page=1000", 1, maxPerPage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &recordingHistory{}

			w := httptest.NewRecorder()
			buildHandlerWithHistory(t, history).HandleJobList(w, httptest.NewRequest(http.MethodGet, "/jobs"+tt.query, nil))

			if history.gotPage != tt.wantPage {
				t.Errorf("page = %d, want %d", history.gotPage, tt.wantPage)
			}
			if history.gotPerPage != tt.wantPerPage {
				t.Errorf("perPage = %d, want %d", history.gotPerPage, tt.wantPerPage)
			}
		})
	}
}

func TestHandleJobList_EmptyState(t *testing.T) {
	w := httptest.NewRecorder()
	buildHandlerWithHistory(t, &recordingHistory{}).HandleJobList(w, httptest.NewRequest(http.MethodGet, "/jobs", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "まだレビュー履歴がありません") {
		t.Error("the empty-state message is not shown")
	}
}

func TestHandleJobList_ListError(t *testing.T) {
	history := &recordingHistory{listErr: errors.New("gcs down")}

	w := httptest.NewRecorder()
	buildHandlerWithHistory(t, history).HandleJobList(w, httptest.NewRequest(http.MethodGet, "/jobs", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "gcs down") {
		t.Error("internal error details are shown on screen")
	}
}
