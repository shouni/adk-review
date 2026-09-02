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

// slackTitles はレビュー結果ごとの見出しです。
var slackTitles = notify.Titles{
	Success: "✅ AIレビューが完了しました。",
	Failure: "❌ AIレビューの生成に失敗しました。",
	Skipped: "⏭️ 差分がないため、レビューをスキップしました。",
}

// SlackAdapter は review.Notifier を満たす具象型です。
type SlackAdapter struct {
	pipeline *notify.Pipeline
}

var _ review.Notifier = (*SlackAdapter)(nil)

// NewSlackAdapter は新しいアダプターインスタンスを作成します。
// webhookURL が空の場合は通知を行わないアダプターを返します。
func NewSlackAdapter(httpClient httpkit.Poster, webhookURL string) (*SlackAdapter, error) {
	notifier, err := slack.NewNotifier(httpClient, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("slackクライアントの初期化に失敗しました: %w", err)
	}

	return &SlackAdapter{
		pipeline: notify.NewPipeline(notifier, slackTitles),
	}, nil
}

// Notify は review.Notifier の実装です。
//
// リンク先は詳細画面（Request.PublicURL）です。成果物への署名付き URL を直接張らないのは、
// アプリの認証を迂回して読めてしまうためです（ReportPublisher の項を参照）。
// 裏返して言えば、通知のリンクを開く人にもログインが要ります。
func (s *SlackAdapter) Notify(ctx context.Context, n review.Notification) error {
	if !s.pipeline.Enabled() {
		slog.InfoContext(ctx, "Slack通知が無効化されているためスキップします。", "job_id", n.Request.JobID)
		return nil
	}

	if err := s.send(ctx, n); err != nil {
		return fmt.Errorf("slackへの結果投稿に失敗しました: %w", err)
	}

	slog.InfoContext(ctx, "レビュー結果を Slack に投稿しました。",
		"job_id", n.Request.JobID, "status", n.Result.Status)
	return nil
}

// send は結果の状態に応じた通知を送信します。
func (s *SlackAdapter) send(ctx context.Context, n review.Notification) error {
	switch n.Result.Status {
	case review.StatusFailed:
		// 詳細画面には結果が無いためリンクは張らず、代わりに失敗した工程を載せます。
		// エラー内容そのものは Pipeline が末尾に追記します。
		body := buildRepositoryBody(n.Request)
		if step := review.StepOf(n.Err); step != "" {
			body.Field("発生ステップ", step)
		}
		return s.pipeline.Failure(ctx, body, n.Err)

	case review.StatusSkipped:
		// スキップの理由は見出しが述べているため、理由は渡しません。
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

// buildRepositoryBody はリポジトリとブランチだけを持つ本文を返します。
func buildRepositoryBody(req review.Request) *notify.Body {
	body := notify.NewBody()
	writeRepositoryFields(body, req)
	return body
}

// writeRepositoryFields はリポジトリと比較ブランチを本文へ追記します。
func writeRepositoryFields(body *notify.Body, req review.Request) {
	body.Code("リポジトリ", giturl.GetRepositoryPath(req.RepoURL))

	if req.Base == "" && req.Head == "" {
		return
	}
	body.Field("ブランチ", notify.CodeSpan(req.Base)+" ← "+notify.CodeSpan(req.Head))
}
