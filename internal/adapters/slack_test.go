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

// recordingNotifier は送信された notify.Message を記録するフェイクです。
// Slack 記法への変換は go-notify 側の責務なので、ここでは git-gemini-web が
// 組み立てた見出しと本文だけを検証します。
type recordingNotifier struct {
	got []notify.Message
}

// Notify は notify.Notifier を実装し、受け取った Message を記録します。
func (r *recordingNotifier) Notify(_ context.Context, msg notify.Message) error {
	r.got = append(r.got, msg)
	return nil
}

// last は最後に送信された Message を返します。
func (r *recordingNotifier) last(t *testing.T) notify.Message {
	t.Helper()
	if len(r.got) == 0 {
		t.Fatal("通知が送信されていません")
	}
	return r.got[len(r.got)-1]
}

// newTestSlackAdapter は記録用 Notifier を差し込んだアダプターを返します。
func newTestSlackAdapter() (*SlackAdapter, *recordingNotifier) {
	rec := &recordingNotifier{}
	return &SlackAdapter{pipeline: notify.NewPipeline(rec, slackTitles)}, rec
}

// testReviewRequest はテストで使う共通のレビュー要求です。
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

// notification は指定した結末の Notification を組み立てます。
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

// レポートが無くても成功通知は送れること（結果は保存済みでも、通知の組み立てで
// 落ちるとワーカーがエラーを返し、タスクが再配信されます）。
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
		t.Errorf("Body = %q, レポートが無い場合に判定は出さない想定です", msg.Body)
	}
}

// TestNotifyFailureIncludesStepAndCause は、失敗通知が発生ステップと原因を含み、
// 詳細リンクを含まないことを検証します。失敗時は結果が保存されないため、
// リンクを出しても中身がありません。
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

// 工程名の付いていないエラーでも失敗通知は送れること。
func TestNotifyFailureWithoutStep(t *testing.T) {
	adapter, rec := newTestSlackAdapter()

	if err := adapter.Notify(context.Background(), notification(review.StatusFailed, nil, errors.New("boom"))); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	msg := rec.last(t)
	if strings.Contains(msg.Body, "発生ステップ") {
		t.Errorf("Body = %q, 工程名が無いなら行ごと出さない想定です", msg.Body)
	}
	if !strings.Contains(msg.Body, "boom") {
		t.Errorf("Body = %q, want the cause", msg.Body)
	}
}

// TestNotifySkippedOmitsReasonSection は、スキップ通知がリポジトリとブランチだけを
// 載せることを検証します。スキップの理由は見出しが述べているため、
// 「理由: N/A」という中身のない行は出しません。
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
		t.Errorf("Body = %q, 理由の節は出さない想定です", msg.Body)
	}
	if !strings.Contains(msg.Body, "**リポジトリ:** `org/repo`") {
		t.Errorf("Body = %q, want the repository", msg.Body)
	}
	if strings.Contains(msg.Body, "詳細") {
		t.Errorf("Body = %q, want no result link when skipped", msg.Body)
	}
}

// TestWriteRepositoryFieldsOmitsEmptyBranches は、ブランチ情報が無い場合に
// 行ごと省かれることを検証します。
func TestWriteRepositoryFieldsOmitsEmptyBranches(t *testing.T) {
	body := notify.NewBody()
	writeRepositoryFields(body, review.Request{RepoURL: "git@github.com:org/repo.git"})

	got := body.String()
	if strings.Contains(got, "ブランチ") {
		t.Errorf("body = %q, ブランチの行は出さない想定です", got)
	}
	if !strings.Contains(got, "**リポジトリ:** `org/repo`") {
		t.Errorf("body = %q, want the repository", got)
	}
}

// TestNewSlackAdapterDisabledWhenWebhookURLEmpty は、Webhook URL が未設定なら
// エラーにならず通知が無効化されることを検証します。
func TestNewSlackAdapterDisabledWhenWebhookURLEmpty(t *testing.T) {
	adapter, err := NewSlackAdapter(nil, "")
	if err != nil {
		t.Fatalf("NewSlackAdapter() error = %v", err)
	}
	if adapter.pipeline.Enabled() {
		t.Fatal("Webhook URL 未設定なのに通知が有効になっています")
	}

	if err := adapter.Notify(context.Background(), notification(review.StatusSucceeded, nil, nil)); err != nil {
		t.Errorf("Notify() = %v, want nil", err)
	}
}

// TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet は、Webhook URL があるのに
// HTTP クライアントが nil の場合はエラーになることを検証します。
func TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet(t *testing.T) {
	if _, err := NewSlackAdapter(nil, "https://hooks.slack.example/webhook"); err == nil {
		t.Fatal("HTTPクライアントが nil なのにエラーになりません")
	}
}

// TestNotifySetsLevel は、3 つの結果それぞれが種別を伴って送信されることを検証します。
// Slack 側はこれを attachment の色に落とすため、見出しの絵文字とは別に必要です。
func TestNotifySetsLevel(t *testing.T) {
	tests := []struct {
		name   string
		status review.Status
		cause  error
		want   notify.Level
	}{
		{"成功", review.StatusSucceeded, nil, notify.LevelSuccess},
		{"失敗", review.StatusFailed, errors.New("boom"), notify.LevelFailure},
		{"スキップ", review.StatusSkipped, nil, notify.LevelSkipped},
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
