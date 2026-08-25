package adapters

import (
	"context"
	"log/slog"
	"runtime/pprof"
	"time"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/adk-review/internal/domain"
)

// recordTimeout は、結末の記録に与える上限です。
const recordTimeout = 30 * time.Second

// reviewRunner は、レビュー本体の実行面です。実体は go-review-kit のパイプラインで、
// テストから差し替えられるようにインターフェースで受けます。
type reviewRunner interface {
	Run(ctx context.Context, req review.Request) (review.Result, *review.Report, error)
}

// ReviewPipeline は go-review-kit のパイプラインを domain.Pipeline として公開する ACL です。
// あわせて、ワーカー側でしか分からない進行状況（実行開始・再配信）を記録します。
type ReviewPipeline struct {
	runner   reviewRunner
	recorder *jobstatus.Recorder[domain.JobStatus]
}

var _ domain.Pipeline = (*ReviewPipeline)(nil)

// NewReviewPipeline は ReviewPipeline を構築します。
//
// レビュー 1 件の上限（PIPELINE_TIMEOUT）はここでは扱いません。ライブラリの
// pipeline.WithRunTimeout が持ちます（internal/builder/pipeline.go で渡しています）。
// 呼び出し側が context に締切を被せると、ライブラリが公開・通知のために行う
// 切り離しより外側に掛かり、打ち切りと同時に失敗通知まで落ちるためです。
//
// 上限を持つこと自体の意味は、Cloud Tasks より先にアプリが諦めることです。先を越されると
// プロセスごと SIGTERM になり、失敗の記録も Slack 通知も残りません（review-queue は
// max_attempts = 1 なので再試行も来ず、タスクは黙って失われます）。
func NewReviewPipeline(runner reviewRunner, store domain.StatusStore) *ReviewPipeline {
	return &ReviewPipeline{
		runner:   runner,
		recorder: jobstatus.NewRecorder(store),
	}
}

// Execute は domain モデルをライブラリのモデルへ変換して実行します。
func (p *ReviewPipeline) Execute(ctx context.Context, req domain.ReviewRequest) error {
	// 以降このジョブから出るログすべてに job_id と mode が載ります。同時に走るレビューの
	// ログが混ざっても分離できるよう、個々の出力ではなく context に持たせます。
	ctx = slogctx.With(ctx,
		slog.String("job_id", req.JobID),
		slog.String("mode", req.Mode),
	)

	// ログの相関に加えて、pprof のゴルーチンラベルにも同じ値を載せます。
	// Go 1.27 以降、ラベルは**パニックのトレースバックの見出し行にも出る**ため、
	// 落ちたときにどのジョブだったかがスタックだけで分かります。slogctx は
	// panic の経路では効かないので、そこを埋めるのがこちらの役目です。
	// ラベルは子ゴルーチン（並列生成など）へも継承されます。
	ctx = pprof.WithLabels(ctx, pprof.Labels("job_id", req.JobID, "mode", req.Mode))
	pprof.SetGoroutineLabels(ctx)

	if p.skipRedelivery(ctx, req.JobID) {
		return nil
	}

	p.recordRunning(ctx, req)

	result, report, err := p.runner.Run(ctx, toReviewRequest(req))
	p.recordOutcome(ctx, req, result, report, err)

	return err
}

// recordOutcome は、レビューの結末を進行状況へ記録します。
//
// レビューが締切で打ち切られた場合、呼び出し元の context も同時に期限切れになっている
// ことがあります。記録まで道連れにしないよう、締切を外したうえで上限を与え直します
// （ライブラリが保存と通知に対して行っているのと同じ切り離しです）。
func (p *ReviewPipeline) recordOutcome(
	ctx context.Context,
	req domain.ReviewRequest,
	result review.Result,
	report *review.Report,
	cause error,
) {
	if req.JobID == "" {
		return
	}

	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()

	p.recorder.Record(recordCtx, req.JobID, buildOutcomeStatus(req, result, report, cause), domain.CarryOverExtras)
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

	// ★ 計測値は**失敗した実行にも**載せます。上限が厳しすぎるかどうかを判断する材料は、
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

// skipRedelivery は、既に成功しているジョブの再配信を打ち切ってよいかを返します。
//
// Cloud Tasks は at-least-once 配信です。通知の失敗などでワーカーがエラーを返すと同じ
// タスクが再配信され、AI の呼び出しコストがそのまま二重に発生します。
func (p *ReviewPipeline) skipRedelivery(ctx context.Context, jobID string) bool {
	if jobID == "" {
		return false
	}

	done, err := p.recorder.AlreadySucceeded(ctx, jobID)
	if err != nil {
		// 読めない場合は未完了として先へ進めます。記録が読めないことを理由に
		// レビューを止めるより、二重実行のほうがまだ回復可能なためです。
		slog.WarnContext(ctx, "完了済みかどうかを確認できませんでした", "error", err)
		return false
	}
	if done {
		slog.InfoContext(ctx, "完了済みのタスクが再配信されたため打ち切ります")
	}
	return done
}

// recordRunning は、処理を開始したことを記録します。
func (p *ReviewPipeline) recordRunning(ctx context.Context, req domain.ReviewRequest) {
	if req.JobID == "" {
		return
	}

	p.recorder.Record(ctx, req.JobID, domain.NewRunningStatus(req), func(next, prev *domain.JobStatus) {
		// Attempts と QueuedAt は Recorder が前回の記録から引き継いだあとに呼ばれます。
		next.Attempts++
		domain.CarryOverExtras(next, prev)
	})
}
