// Package pipeline は、レビュー 1 件のワーカー本体です。go-review-kit のパイプラインを
// domain.Pipeline として公開する ACL に、ワーカー側でしか分からない進行状況の記録を足します。
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/shouni/gcp-kit/worker"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/adk-review/internal/domain"
)

// reviewRunner は、レビュー本体の実行面です。実体は go-review-kit のパイプラインで、
// テストから差し替えられるようにインターフェースで受けます。
type reviewRunner interface {
	Run(ctx context.Context, req review.Request) (review.Result, *review.Report, error)
}

// Runner は go-review-kit のパイプラインを domain.Pipeline として公開する ACL です。
// あわせて、ワーカー側でしか分からない進行状況（実行開始・再配信）を記録します。
type Runner struct {
	runner   reviewRunner
	recorder *jobstatus.Recorder[domain.JobStatus]
}

var _ domain.Pipeline = (*Runner)(nil)

// NewRunner は Runner を構築します。
//
// レビュー 1 件の上限（PIPELINE_TIMEOUT）はここでは扱いません。ライブラリの
// pipeline.WithRunTimeout が持ちます（internal/builder/pipeline.go で渡しています）。
// 呼び出し側が context に締切を被せると、ライブラリが公開・通知のために行う
// 切り離しより外側に掛かり、打ち切りと同時に失敗通知まで落ちるためです。
//
// 上限を持つこと自体の意味は、Cloud Tasks より先にアプリが諦めることです。先を越されると
// プロセスごと SIGTERM になり、失敗の記録も Slack 通知も残りません（review-queue は
// max_attempts = 1 なので再試行も来ず、タスクは黙って失われます）。
func NewRunner(runner reviewRunner, store domain.StatusStore) *Runner {
	return &Runner{
		runner:   runner,
		recorder: jobstatus.NewRecorder(store),
	}
}

// runOutcome は、実行本体が結末の記録へ渡すものです。
type runOutcome struct {
	result review.Result
	report *review.Report
}

// Execute は worker.TaskExecutor を満たします。
//
// ジョブの一生（再配信ガード → 実行 → 結末の記録）は gcp-kit/worker.Lifecycle が持ち、
// ここはそれぞれの中身だけを渡します（public-docs のワーカー規約）。レビュー 1 件の上限
// （PIPELINE_TIMEOUT）は go-review-kit の pipeline.WithRunTimeout が持つので、
// Lifecycle.Timeout は使いません。ライブラリが公開・通知のために行う切り離しより外側に
// 掛けると、打ち切りと同時に失敗通知まで落ちるためです。
func (p *Runner) Execute(ctx context.Context, req domain.ReviewRequest) error {
	return p.lifecycle().Execute(ctx, req)
}

// lifecycle は、ジョブの一生の各段にこのアプリの中身を当てはめます。
func (p *Runner) lifecycle() worker.Lifecycle[domain.ReviewRequest, runOutcome] {
	return worker.Lifecycle[domain.ReviewRequest, runOutcome]{
		// 以降このジョブから出るログすべてに job_id と mode が載ります。同時に走るレビューの
		// ログが混ざっても分離できるよう、個々の出力ではなく context に持たせます。
		Prepare: func(ctx context.Context, req domain.ReviewRequest) context.Context {
			return slogctx.With(ctx,
				slog.String("job_id", req.JobID),
				slog.String("mode", req.Mode),
			)
		},
		Labels: func(req domain.ReviewRequest) map[string]string {
			return map[string]string{"job_id": req.JobID, "mode": req.Mode}
		},
		Begin: p.begin,
		Run: func(ctx context.Context, req domain.ReviewRequest) (runOutcome, error) {
			result, report, err := p.runner.Run(ctx, toReviewRequest(req))
			return runOutcome{result: result, report: report}, err
		},
		Finish: func(ctx context.Context, req domain.ReviewRequest, out runOutcome, cause error) error {
			p.recordOutcome(ctx, req, out.result, out.report, cause)
			return classifyForRetry(cause)
		},
	}
}

// begin は、既に成功しているジョブの再配信を打ち切り、未完了なら実行開始を記録します。
//
// Cloud Tasks は at-least-once 配信です。通知の失敗などでワーカーがエラーを返すと同じ
// タスクが再配信され、AI の呼び出しコストがそのまま二重に発生します。判定と記録は前回の
// 記録の 1 回の読みで行うので、間に別の配信が割り込む隙がありません。
//
// 状態を読めなかった場合は実行しません。以前は「読めないことを理由に止めるより二重実行の
// ほうが回復可能」として進んでいましたが、完了済みを作り直す費用をガード自身が払う形なので、
// 規約に合わせて再配信（人の投げ直し）に委ねます。
func (p *Runner) begin(ctx context.Context, req domain.ReviewRequest) (bool, error) {
	if req.JobID == "" {
		return false, nil
	}

	done, err := p.recorder.Begin(ctx, req.JobID, domain.NewRunningStatus(req), func(next, prev *domain.JobStatus) {
		// Attempts と QueuedAt は Recorder が前回の記録から引き継いだあとに呼ばれます。
		next.Attempts++
		domain.CarryOverExtras(next, prev)
	})
	if err != nil {
		return false, fmt.Errorf("ジョブ状態を読めないため実行を見送ります (%s): %w", req.JobID, err)
	}
	if done {
		slog.InfoContext(ctx, "完了済みのタスクが再配信されたため打ち切ります")
	}
	return done, nil
}

// permanentCauses は、同じ依頼を配り直しても結果が変わらない失敗です。
//
// いずれも入力そのものが原因で、時間を置いても同じところで落ちます。ここに
// 挙げていない失敗（モデルの空応答・壊れた出力・Vertex や GCS の一時障害）は
// 再配信で直り得るので、再試行に任せます。
var permanentCauses = []error{
	review.ErrInvalidRequest,
	review.ErrEmptyDiff,
	review.ErrDiffTooLarge,
	review.ErrRefNotFound,
	review.ErrUnsupportedRepoURL,
}

// classifyForRetry は、再配信しても直らない失敗に worker.ErrPermanent を被せます。
//
// これが無いと、恒久的な失敗も 500 として再配信の対象になります。差分が上限を
// 超えている依頼を配り直しても同じ場所で落ちるだけで、利用者には同じ失敗通知が
// 2 通届きます。被せた場合は 2xx で打ち切られますが、失敗として記録も通知も
// 済んでいるので、利用者から見た結果は変わりません。
func classifyForRetry(err error) error {
	if err == nil {
		return nil
	}
	for _, cause := range permanentCauses {
		if errors.Is(err, cause) {
			return fmt.Errorf("%w: %w", worker.ErrPermanent, err)
		}
	}
	return err
}

// recordOutcome は、レビューの結末を進行状況へ記録します（Lifecycle の Finish）。
//
// レビューが締切で打ち切られた場合、呼び出し元の context も同時に期限切れになっている
// ことがあります。記録まで道連れにしないための切り離しは Lifecycle が行います。
func (p *Runner) recordOutcome(
	ctx context.Context,
	req domain.ReviewRequest,
	result review.Result,
	report *review.Report,
	cause error,
) {
	if req.JobID == "" {
		return
	}

	p.recorder.Record(ctx, req.JobID, buildOutcomeStatus(req, result, report, cause), domain.CarryOverExtras)
}

// buildOutcomeStatus は、結末から記録する JobStatus を組み立てます。
func buildOutcomeStatus(
	req domain.ReviewRequest,
	result review.Result,
	report *review.Report,
	cause error,
) domain.JobStatus {
	status := domain.NewSucceededStatus(req, result.Status)
	if cause != nil {
		status = domain.NewFailedStatus(req, cause)
	} else if report != nil {
		// スキップもジョブとしては正常終了です。成果物が無いことは Outcome が表します。
		status.Title = report.Title
		status.Decision = report.Verdict.Decision
		status.ReportURI = req.StorageURI
	}

	// ★ 計測値は失敗した実行にも載せます。上限が厳しすぎるかどうかを判断する材料は、
	// 通った実行より弾かれた実行の側にあります。
	status.Truncated = result.Run.Truncated
	status.Metrics = domain.Metrics{
		DiffBytes:     result.DiffBytes,
		DurationMS:    result.Duration.Milliseconds(),
		PromptTokens:  result.Run.PromptTokens,
		OutputTokens:  result.Run.OutputTokens,
		ThoughtTokens: result.Run.ThoughtTokens,
		ToolCalls:     result.Run.ToolCalls,
	}
	return status
}
