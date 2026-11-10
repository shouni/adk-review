package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shouni/go-notify/notify"
	"github.com/shouni/go-review-kit/review"
)

// recordingNotifier is a fake that records the notify.Message sent.
// Converting to Slack markup is go-notify's job, so only the heading and body
// adk-review builds are checked here.
type recordingNotifier struct {
	got []notify.Message
}

// Notify implements notify.Notifier and records the Message it receives.
func (r *recordingNotifier) Notify(_ context.Context, msg notify.Message) error {
	r.got = append(r.got, msg)
	return nil
}

// last returns the last Message sent.
func (r *recordingNotifier) last(t *testing.T) notify.Message {
	t.Helper()
	if len(r.got) == 0 {
		t.Fatal("no notification was sent")
	}
	return r.got[len(r.got)-1]
}

// newTestSlackAdapter returns an adapter wired to the recording Notifier.
func newTestSlackAdapter() (*SlackAdapter, *recordingNotifier) {
	rec := &recordingNotifier{}
	return &SlackAdapter{pipeline: notify.NewPipeline(rec, slackTitles)}, rec
}

// testReviewRequest is the review request shared by the tests.
func testReviewRequest() review.Request {
	return review.Request{
		JobID:      "20260810-213000-a1b2c3d4",
		RepoURL:    "git@github.com:org/repo.git",
		Base:       "main",
		Head:       "feature/new-ui",
		Mode:       "code",
		Model:      "gemini-2.5-pro",
		StorageURI: "gs://bucket/reviews/20260810-213000-a1b2c3d4/report.json",
		PublicURL:  "https://service.example.com/history/20260810-213000-a1b2c3d4",
	}
}

// notification builds a Notification for the given outcome.
func notification(status review.Status, report *review.Report, cause error) review.Notification {
	req := testReviewRequest()
	return review.Notification{
		Request: req,
		Result: review.Result{
			Status:     status,
			StorageURI: req.StorageURI,
			PublicURL:  req.PublicURL,
			Duration:   3 * time.Second,
		},
		Report: report,
		Err:    cause,
	}
}

func testReport() review.Report {
	return review.Report{
		Title:   "認証処理のレビュー",
		Summary: "概ね良好です。",
		Verdict: review.Verdict{Decision: review.DecisionMinor, Reason: "軽微な指摘が1件"},
		Findings: []review.Finding{
			{Severity: review.SeverityMinor, File: "main.go", Excerpt: "x := 1", Message: "未使用です。"},
		},
	}
}

func TestNotifySuccessIncludesModelName(t *testing.T) {
	adapter, rec := newTestSlackAdapter()

	report := testReport()
	err := adapter.Notify(context.Background(), notification(review.StatusSucceeded, &report, nil))
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	msg := rec.last(t)
	if msg.Title != slackTitles.Success {
		t.Errorf("Title = %q, want %q", msg.Title, slackTitles.Success)
	}

	for _, want := range []string{
		"**詳細:** [20260810-213000-a1b2c3d4](https://service.example.com/history/20260810-213000-a1b2c3d4)",
		"**リポジトリ:** `org/repo`",
		"**ブランチ:** `main` ← `feature/new-ui`",
		"**モード:** `code`",
		"**モデル:** `gemini-2.5-pro`",
		"**判定:** Minor",
		"**指摘:** 1件",
	} {
		if !strings.Contains(msg.Body, want) {
			t.Fatalf("slack content should contain %q, got:\n%s", want, msg.Body)
		}
	}
}

// A success notice must go out even without a report (even with the result saved, a failure
// while building the notice makes the worker return an error and the task is redelivered).
func TestNotifySuccessWithoutReport(t *testing.T) {
	adapter, rec := newTestSlackAdapter()

	if err := adapter.Notify(context.Background(), notification(review.StatusSucceeded, nil, nil)); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	msg := rec.last(t)
	if msg.Title != slackTitles.Success {
		t.Errorf("Title = %q, want %q", msg.Title, slackTitles.Success)
	}
	if strings.Contains(msg.Body, "判定") {
		t.Errorf("Body = %q, the decision should be omitted without a report", msg.Body)
	}
}

// TestNotifyFailureIncludesStepAndCause checks that a failure notice carries the failed step
// and the cause but no detail link. On failure no result is saved, so a link would lead
// to nothing.
func TestNotifyFailureIncludesStepAndCause(t *testing.T) {
	adapter, rec := newTestSlackAdapter()

	cause := review.WrapStep(review.StepReview, errors.New("Gemini がタイムアウトしました"))
	if err := adapter.Notify(context.Background(), notification(review.StatusFailed, nil, cause)); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	msg := rec.last(t)
	if msg.Title != slackTitles.Failure {
		t.Errorf("Title = %q, want %q", msg.Title, slackTitles.Failure)
	}
	if !strings.Contains(msg.Body, "**発生ステップ:** "+review.StepReview) {
		t.Errorf("Body = %q, want the failing step", msg.Body)
	}
	if !strings.Contains(msg.Body, "Gemini がタイムアウトしました") {
		t.Errorf("Body = %q, want the cause", msg.Body)
	}
	if strings.Contains(msg.Body, "詳細") {
		t.Errorf("Body = %q, want no result link on failure", msg.Body)
	}
}

// A failure notice must go out even for an error without a step name.
func TestNotifyFailureWithoutStep(t *testing.T) {
	adapter, rec := newTestSlackAdapter()

	if err := adapter.Notify(context.Background(), notification(review.StatusFailed, nil, errors.New("boom"))); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	msg := rec.last(t)
	if strings.Contains(msg.Body, "発生ステップ") {
		t.Errorf("Body = %q, the whole line should be omitted without a step name", msg.Body)
	}
	if !strings.Contains(msg.Body, "boom") {
		t.Errorf("Body = %q, want the cause", msg.Body)
	}
}

// TestNotifySkippedOmitsReasonSection checks that a skip notice carries only the repository
// and branches. The heading states why it was skipped, so an empty "理由: N/A" (reason)
// line is not shown.
func TestNotifySkippedOmitsReasonSection(t *testing.T) {
	adapter, rec := newTestSlackAdapter()

	if err := adapter.Notify(context.Background(), notification(review.StatusSkipped, nil, nil)); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	msg := rec.last(t)
	if msg.Title != slackTitles.Skipped {
		t.Errorf("Title = %q, want %q", msg.Title, slackTitles.Skipped)
	}
	if strings.Contains(msg.Body, "理由") {
		t.Errorf("Body = %q, the reason section should be omitted", msg.Body)
	}
	if !strings.Contains(msg.Body, "**リポジトリ:** `org/repo`") {
		t.Errorf("Body = %q, want the repository", msg.Body)
	}
	if strings.Contains(msg.Body, "詳細") {
		t.Errorf("Body = %q, want no result link when skipped", msg.Body)
	}
}

// TestWriteRepositoryFieldsOmitsEmptyBranches checks that without branch information the
// whole line is omitted.
func TestWriteRepositoryFieldsOmitsEmptyBranches(t *testing.T) {
	body := notify.NewBody()
	writeRepositoryFields(body, review.Request{RepoURL: "git@github.com:org/repo.git"})

	got := body.String()
	if strings.Contains(got, "ブランチ") {
		t.Errorf("body = %q, the branch line should be omitted", got)
	}
	if !strings.Contains(got, "**リポジトリ:** `org/repo`") {
		t.Errorf("body = %q, want the repository", got)
	}
}

// TestNewSlackAdapterDisabledWhenWebhookURLEmpty checks that an unset webhook URL is not an
// error and disables notifications.
func TestNewSlackAdapterDisabledWhenWebhookURLEmpty(t *testing.T) {
	adapter, err := NewSlackAdapter(nil, "")
	if err != nil {
		t.Fatalf("NewSlackAdapter() error = %v", err)
	}
	if adapter.pipeline.Enabled() {
		t.Fatal("notifications are enabled although the webhook URL is unset")
	}

	if err := adapter.Notify(context.Background(), notification(review.StatusSucceeded, nil, nil)); err != nil {
		t.Errorf("Notify() = %v, want nil", err)
	}
}

// TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet checks that a nil HTTP client with a
// webhook URL set is an error.
func TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet(t *testing.T) {
	if _, err := NewSlackAdapter(nil, "https://hooks.slack.example/webhook"); err == nil {
		t.Fatal("no error although the HTTP client is nil")
	}
}

// TestNotifySetsLevel checks that each of the 3 outcomes is sent with its level.
// Slack maps it to the attachment colour, so it is needed besides the heading's emoji.
func TestNotifySetsLevel(t *testing.T) {
	tests := []struct {
		name   string
		status review.Status
		cause  error
		want   notify.Level
	}{
		{"success", review.StatusSucceeded, nil, notify.LevelSuccess},
		{"failure", review.StatusFailed, errors.New("boom"), notify.LevelFailure},
		{"skipped", review.StatusSkipped, nil, notify.LevelSkipped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, rec := newTestSlackAdapter()

			if err := adapter.Notify(context.Background(), notification(tt.status, nil, tt.cause)); err != nil {
				t.Fatalf("Notify failed: %v", err)
			}
			if got := rec.last(t).Level; got != tt.want {
				t.Errorf("Level = %v, want %v", got, tt.want)
			}
		})
	}
}
