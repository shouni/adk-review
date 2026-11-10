package domain

import (
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

// CommandReview is the jobstatus.Status.Command value naming the kind of job.
// This service's only job so far is the review, but it is stated so that readers of the
// record can tell the kind.
const CommandReview = "review"

// JobStatus is the progress of one review job and the metadata shown in the history list.
//
// It embeds jobstatus.Status, so the JSON is flat (a nested payload would make the stored
// status.json files unreadable).
//
// Everything the display needs lives here, so the list reads one object per row. Only the
// full findings, which grow with their count, live in report.json.
//
// The tags on numbers and booleans are omitzero. Do not change them back to omitempty.
// This struct is written to status.json by jobstatus.Store, which uses encoding/json/v2. v2's
// omitempty judges by "does it encode to an empty JSON value", so it does not drop 0 and false;
// with omitempty, every record would carry truncated:false and metrics full of zeros. For
// strings the empty string is empty in v2 too, so omitempty is fine there.
type JobStatus struct {
	jobstatus.Status

	// Outcome is the outcome of the review itself.
	//
	// jobstatus.State (queued / running / succeeded / failed) only says whether the job lived.
	// "No diff, so no review" is a normal end for the job, so it is succeeded, yet it has no
	// artifact and no decision. A separate value lets the list tell them apart.
	Outcome review.Status `json:"outcome,omitempty"`

	RepoURL       string `json:"repo_url,omitempty"`
	BaseBranch    string `json:"base_branch,omitempty"`
	FeatureBranch string `json:"feature_branch,omitempty"`
	Mode          string `json:"mode,omitempty"`
	ModelName     string `json:"model_name,omitempty"`

	// Decision is the review's overall decision. Set only on success.
	Decision review.Decision `json:"decision,omitempty"`
	// ReportURI is the URI of the full review result (report.json). Set only on success.
	ReportURI string `json:"report_uri,omitempty"`

	// Truncated means the model's output was cut off and only the completed part was saved.
	//
	// ★ It is recorded as a success, but the findings are not all there is. Saved without this,
	// the screen and the API would present "a review with 2 findings", and the reader would
	// have no way to know there was more.
	Truncated bool `json:"truncated,omitzero"`

	// Metrics is the material for recalibrating the limits from measurements.
	Metrics Metrics `json:"metrics,omitzero"`
}

// Metrics are the measurements of one review run.
//
// Failed runs carry them too. The material for judging whether a limit (diff size, tool calls,
// output tokens) is too strict lies with the rejected runs rather than the ones that passed.
// Before this existed, deciding `MAX_DIFF_BYTES` meant digging through Cloud Logging and
// matching log lines by hand.
type Metrics struct {
	// DiffBytes is the size of the diff sent to the AI. Used to tune MAX_DIFF_BYTES.
	DiffBytes int `json:"diff_bytes,omitzero"`
	// DurationMS is how long the review took. Used to tune PIPELINE_TIMEOUT.
	DurationMS int64 `json:"duration_ms,omitzero"`
	// The following are the model's usage, showing how close the output came to the limit (64Ki tokens).
	PromptTokens  int `json:"prompt_tokens,omitzero"`
	OutputTokens  int `json:"output_tokens,omitzero"`
	ThoughtTokens int `json:"thought_tokens,omitzero"`
	// ToolCalls is how many times the agent called a tool. Used to tune AGENT_MAX_TOOL_CALLS.
	ToolCalls int `json:"tool_calls,omitzero"`
}

// Empty reports whether no measurement is set.
func (m Metrics) Empty() bool { return m == Metrics{} }

// HasReport reports whether the full review result is stored.
func (s JobStatus) HasReport() bool {
	return s.ReportURI != ""
}

// Deletable reports whether the entry may be deleted from the history.
//
// queued / running cannot be deleted because the worker would write status.json back after
// the deletion and revive the prefix, leaving an empty row in the history.
func (s JobStatus) Deletable() bool {
	return s.Finished()
}

// Rerunnable reports whether the form can be refilled from this request.
//
// It is not offered while queued / running because resubmitting a request still awaiting its
// result would run the same review twice. RepoURL is checked because jobs may have been stored in
// a format from before requests were recorded, and opening a form that cannot be filled is no
// better than typing it again.
func (s JobStatus) Rerunnable() bool {
	if s.RepoURL == "" {
		return false
	}
	return s.Finished()
}

// carryOverFrom moves the service-specific fields that must carry over from the previous record.
//
// The worker rebuilds JobStatus from the task at every transition, so information known only
// at submission (values not in the task, as opposed to values settled later such as Outcome or
// Decision) would be lost if written naively. jobstatus.Recorder carries over the common fields,
// so this handles only what this type adds.
func (s *JobStatus) carryOverFrom(prev *JobStatus) {
	if prev == nil {
		return
	}
	if s.ReportURI == "" {
		s.ReportURI = prev.ReportURI
	}
	if s.Decision == "" {
		s.Decision = prev.Decision
	}
	if s.Outcome == "" {
		s.Outcome = prev.Outcome
	}
	if s.Metrics.Empty() {
		s.Metrics = prev.Metrics
		s.Truncated = prev.Truncated
	}
}

// CarryOverExtras is the carry-over function passed to jobstatus.Recorder.Record.
func CarryOverExtras(next, prev *JobStatus) {
	next.carryOverFrom(prev)
}

// NewQueuedStatus builds the JobStatus recorded when a task is submitted.
func NewQueuedStatus(req ReviewRequest) JobStatus {
	return newStatus(req, jobstatus.StateQueued)
}

// NewRunningStatus builds the JobStatus recorded when the worker starts.
func NewRunningStatus(req ReviewRequest) JobStatus {
	return newStatus(req, jobstatus.StateRunning)
}

// NewFailedStatus builds the JobStatus that records a failure.
func NewFailedStatus(req ReviewRequest, err error) JobStatus {
	status := newStatus(req, jobstatus.StateFailed)
	status.Outcome = review.StatusFailed
	if err != nil {
		status.Error = err.Error()
	}
	return status
}

// NewSucceededStatus builds the JobStatus recording that the review finished.
// A skip is also recorded as succeeded ("a normal end for the job") and told apart by Outcome.
func NewSucceededStatus(req ReviewRequest, outcome review.Status) JobStatus {
	status := newStatus(req, jobstatus.StateSucceeded)
	status.Outcome = outcome
	return status
}

func newStatus(req ReviewRequest, state jobstatus.State) JobStatus {
	return JobStatus{
		JobID:         req.JobID,
		Command:       CommandReview,
		State:         state,
		RepoURL:       req.RepoURL,
		BaseBranch:    req.BaseBranch,
		FeatureBranch: req.FeatureBranch,
		Mode:          req.Mode,
		ModelName:     req.ModelName,
	}
}
