package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

// --- GET /history/{jobID}（旧パス。JSON の形だけ旧いまま） ---

func TestHandleLegacyReviewDetail_ReturnsStatusAndReport(t *testing.T) {
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
	req := withURLParam(jsonRequest(http.MethodGet, "/history/x"), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleLegacyReviewDetail(rec, req)

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
func TestHandleLegacyReviewDetail_NullReportWhenAbsent(t *testing.T) {
	t.Parallel()

	history := &recordingHistory{detail: domain.ReviewDetail{
		Status: sampleStatus(jobstatus.StateRunning, ""),
	}}
	h := buildHandlerWithHistory(t, history)

	rec := httptest.NewRecorder()
	req := withURLParam(jsonRequest(http.MethodGet, "/history/x"), "jobID", "20260810-213000-a1b2c3d4")
	h.HandleLegacyReviewDetail(rec, req)

	raw := decodeJSON[map[string]json.RawMessage](t, rec)
	report, ok := raw["report"]
	if !ok {
		t.Fatalf("report キーがありません: %v", raw)
	}
	if string(report) != "null" {
		t.Errorf("report = %s, want null", report)
	}
}
