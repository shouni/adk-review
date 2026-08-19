package adapters

import (
	"context"
	"log/slog"
	"time"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/domain"
)

// recordTimeout は、結末の記録に与える上限です。
const recordTimeout = 30 * time.Second

// reviewRunner は、レビュー本体の実行面です。実体は EngineRouter で、依頼ごとに
// go-review-kit のパイプラインを選びます。
//
// review.Request ではなく domain.ReviewRequest を受け取るのは、どのエンジンで実行するかが
// ライブラリの語彙に無い本アプリ固有の情報だからです。変換は選び終えたあとに行います。
type reviewRunner interface {
	Run(ctx context.Context, req domain.ReviewRequest) (review.Result, *review.Report, error)
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
func NewReviewPipeline(runner reviewRunner, store domain.StatusStore) *ReviewPipeline {
	return &ReviewPipeline{
		runner:   runner,
		recorder: jobstatus.NewRecorder(store),
	}
}

// Execute は domain モデルをライブラリのモデルへ変換して実行します。
//
// ★ ここで締切を被せることで、**Cloud Tasks より先にアプリが諦めます**。
//
// Cloud Tasks に先を越されるとプロセスごと SIGTERM になり、失敗の記録も Slack 通知も
// 残りません（review-queue は max_attempts = 1 なので再試行も来ず、タスクは黙って
// 失われます）。自分で諦めれば、打ち切りの理由が結末に載って記録と通知まで届きます。
//
// 締切が保存・通知・後始末を巻き込まないことはライブラリ側が保証しています。
func (p *ReviewPipeline) Execute(ctx context.Context, req domain.ReviewRequest) error {
	// 以降このジョブから出るログすべてに job_id が載ります。同時に走るレビューの
	// ログが混ざっても分離できるよう、個々の出力ではなく context に持たせます。
	ctx = slogctx.With(ctx,
		slog.String("job_id", req.JobID),
		slog.String("mode", req.Mode),
	)

	if p.skipRedelivery(ctx, req.JobID) {
		return nil
	}

	// 実行に使うエンジンを最初に確定させ、以降の記録に載せます。既定に任せた依頼でも
	// 「どちらで走ったか」が履歴に残るようにするためです。
	//
	// 解決できない指定は記録に載せません。載せると「存在しないエンジンで実行して
	// 失敗した」という嘘の記録が残ります（実行側の Run が改めて解決して報告します）。
	engine, resolveErr := assets.ResolveEngine(req.Mode, req.Engine)
	if resolveErr == nil {
		req.Engine = string(engine)
		ctx = slogctx.With(ctx, slog.String("engine", req.Engine))
	} else {
		req.Engine = ""
	}

	p.recordRunning(ctx, req)

	result, report, err := p.runner.Run(ctx, req)
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
	if cause != nil {
		return domain.NewFailedStatus(req, cause)
	}

	// スキップもジョブとしては正常終了です。成果物が無いことは Outcome が表します。
	status := domain.NewSucceededStatus(req, result.Status)
	if report != nil {
		status.Title = report.Title
		status.Decision = report.Verdict.Decision
		status.ReportURI = req.StorageURI
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
