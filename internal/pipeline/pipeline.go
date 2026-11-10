// Package pipeline is the worker body for one review. To the ACL exposing go-review-kit's
// pipeline as a domain.Pipeline it adds the progress records only the worker side knows.
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

// reviewRunner runs the review itself. The implementation is go-review-kit's pipeline, taken
// as an interface so tests can swap it.
type reviewRunner interface {
	Run(ctx context.Context, req review.Request) (review.Result, *review.Report, error)
}

// Runner is the ACL exposing go-review-kit's pipeline as a domain.Pipeline.
// It also records the progress only the worker side knows (start of a run, redelivery).
type Runner struct {
	runner   reviewRunner
	recorder *jobstatus.Recorder[domain.JobStatus]
}

var _ domain.Pipeline = (*Runner)(nil)

// NewRunner builds a Runner.
//
// The per-review limit (PIPELINE_TIMEOUT) is not handled here. The library's
// pipeline.WithRunTimeout owns it (passed in internal/builder/pipeline.go). A deadline put on
// the context by the caller would sit outside the detachment the library does for publishing
// and notifying, and the failure notice would be lost together with the cut-off.
//
// The point of having a limit at all is that the app gives up before Cloud Tasks does. If
// Cloud Tasks gets there first, the process is SIGTERMed and neither a failure record nor a
// Slack notice is left (review-queue has max_attempts = 1, so no retry comes either and the
// task is silently lost).
func NewRunner(runner reviewRunner, store domain.StatusStore) *Runner {
	return &Runner{
		runner:   runner,
		recorder: jobstatus.NewRecorder(store),
	}
}

// runOutcome is what the run hands to the outcome recording.
type runOutcome struct {
	result review.Result
	report *review.Report
}

// Execute satisfies worker.TaskExecutor.
//
// The job's life (redelivery guard -> run -> outcome recording) belongs to
// gcp-kit/worker.Lifecycle; this only supplies the contents of each stage (the public-docs worker
// convention). The per-review limit (PIPELINE_TIMEOUT) belongs to go-review-kit's
// pipeline.WithRunTimeout, so Lifecycle.Timeout is not used. Applied outside the detachment the
// library does for publishing and notifying, it would lose the failure notice together with the
// cut-off.
func (p *Runner) Execute(ctx context.Context, req domain.ReviewRequest) error {
	return p.lifecycle().Execute(ctx, req)
}

// lifecycle fills each stage of the job's life with this app's contents.
func (p *Runner) lifecycle() worker.Lifecycle[domain.ReviewRequest, runOutcome] {
	return worker.Lifecycle[domain.ReviewRequest, runOutcome]{
		// Every later log line from this job carries job_id and mode. They live on the context
		// rather than each call, so logs of concurrent reviews can be separated even when mixed.
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

// permanentCauses are failures that redelivering the same request cannot change.
//
// Each is caused by the input itself and fails at the same place however long one waits.
// Failures not listed here (an empty model response, broken output, transient Vertex or GCS
// outages) can be fixed by redelivery, so they are left to retries.
var permanentCauses = []error{
	review.ErrInvalidRequest,
	review.ErrEmptyDiff,
	review.ErrDiffTooLarge,
	review.ErrRefNotFound,
	review.ErrUnsupportedRepoURL,
}

// classifyForRetry wraps failures that redelivery cannot fix in worker.ErrPermanent.
//
// Without it, permanent failures would also be 500s and get redelivered. Redelivering a request
// whose diff exceeds the limit only fails at the same place again, and the user receives the
// same failure notice twice. Wrapped, it ends with a 2xx, but the failure has already been
// recorded and notified, so nothing changes from the user's point of view.
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
