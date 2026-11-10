package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/domain"
)

// begin stops a redelivery of a job that already succeeded, and otherwise records the start of the run.
//
// Cloud Tasks delivers at least once. When the worker returns an error, for example because a
// notification failed, the same task is redelivered and the cost of the AI calls is paid twice.
// The decision and the record come from a single read of the previous record, leaving no gap
// for another delivery to cut in.
//
// It does not run when the status cannot be read. It used to proceed, reasoning that "running
// twice is easier to recover from than stopping because the read failed", but then the guard
// itself paid for rebuilding a finished job, so, following the convention, it is left to
// redelivery (a person resubmitting).
func (p *Runner) begin(ctx context.Context, req domain.ReviewRequest) (bool, error) {
	if req.JobID == "" {
		return false, nil
	}

	done, err := p.recorder.Begin(ctx, req.JobID, domain.NewRunningStatus(req), func(next, prev *domain.JobStatus) {
		// Called after the Recorder has carried Attempts and QueuedAt over from the previous record.
		next.Attempts++
		domain.CarryOverExtras(next, prev)
	})
	if err != nil {
		return false, fmt.Errorf("not running: cannot read the job status (%s): %w", req.JobID, err)
	}
	if done {
		slog.InfoContext(ctx, "stopping: a finished task was redelivered")
	}
	return done, nil
}

// recordOutcome records the review's outcome in the progress (the Lifecycle's Finish).
//
// When the review is cut off at the deadline, the caller's context may have expired at the
// same moment. Lifecycle does the detachment that keeps the record from going down with it.
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

// buildOutcomeStatus builds the JobStatus recorded from the outcome.
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
		// A skip is also a normal end for the job. Outcome expresses that there is no artifact.
		status.Title = report.Title
		status.Decision = report.Verdict.Decision
		status.ReportURI = req.StorageURI
	}

	// ★ Failed runs carry the measurements too. The material for judging whether a limit is
	// too strict lies with the rejected runs rather than the ones that passed.
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
