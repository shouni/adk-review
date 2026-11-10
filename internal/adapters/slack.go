package adapters

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-notify/notify"
	"github.com/shouni/go-notify/slack"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/giturl"
)

// slackTitles are the headings per review outcome.
var slackTitles = notify.Titles{
	Success: "✅ AIレビューが完了しました。",
	Failure: "❌ AIレビューの生成に失敗しました。",
	Skipped: "⏭️ 差分がないため、レビューをスキップしました。",
}

// SlackAdapter is the concrete type satisfying review.Notifier.
type SlackAdapter struct {
	pipeline *notify.Pipeline
}

var _ review.Notifier = (*SlackAdapter)(nil)

// NewSlackAdapter creates a new adapter instance.
// With an empty webhookURL it returns an adapter that sends nothing.
func NewSlackAdapter(httpClient httpkit.Poster, webhookURL string) (*SlackAdapter, error) {
	notifier, err := slack.NewNotifier(httpClient, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialise the slack client: %w", err)
	}

	return &SlackAdapter{
		pipeline: notify.NewPipeline(notifier, slackTitles),
	}, nil
}

// Notify implements review.Notifier.
//
// The link points to the detail page (Request.PublicURL). A signed URL to the artifact is
// not linked directly because it could be read bypassing the app's authentication (see
// ReportPublisher). Put the other way, whoever opens the notice's link must also log in.
func (s *SlackAdapter) Notify(ctx context.Context, n review.Notification) error {
	if !s.pipeline.Enabled() {
		slog.InfoContext(ctx, "skipping: Slack notifications are disabled", "job_id", n.Request.JobID)
		return nil
	}

	if err := s.send(ctx, n); err != nil {
		return fmt.Errorf("failed to post the result to slack: %w", err)
	}

	slog.InfoContext(ctx, "posted the review result to Slack",
		"job_id", n.Request.JobID, "status", n.Result.Status)
	return nil
}

// send sends the notice for the outcome's state.
func (s *SlackAdapter) send(ctx context.Context, n review.Notification) error {
	switch n.Result.Status {
	case review.StatusFailed:
		// The detail page has no result, so no link; the failed step is shown instead.
		// The Pipeline appends the error itself at the end.
		body := buildRepositoryBody(n.Request)
		if step := review.StepOf(n.Err); step != "" {
			body.Field("発生ステップ", step)
		}
		return s.pipeline.Failure(ctx, body, n.Err)

	case review.StatusSkipped:
		// The heading states why it was skipped, so no reason is passed.
		return s.pipeline.Skipped(ctx, buildRepositoryBody(n.Request), nil)

	default:
		body := notify.NewBody().Link("詳細", n.Request.PublicURL, n.Request.JobID)
		writeRepositoryFields(body, n.Request)
		body.Code("モード", n.Request.Mode).
			Code("モデル", n.Request.Model)
		if n.Report != nil {
			body.Field("判定", string(n.Report.Verdict.Decision))
			body.Field("指摘", fmt.Sprintf("%d件", len(n.Report.Findings)))
		}
		return s.pipeline.Success(ctx, body)
	}
}

// buildRepositoryBody returns a body holding only the repository and branches.
func buildRepositoryBody(req review.Request) *notify.Body {
	body := notify.NewBody()
	writeRepositoryFields(body, req)
	return body
}

// writeRepositoryFields appends the repository and the compared branches to the body.
func writeRepositoryFields(body *notify.Body, req review.Request) {
	body.Code("リポジトリ", giturl.GetRepositoryPath(req.RepoURL))

	if req.Base == "" && req.Head == "" {
		return
	}
	body.Field("ブランチ", notify.CodeSpan(req.Base)+" ← "+notify.CodeSpan(req.Head))
}
