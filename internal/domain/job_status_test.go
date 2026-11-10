package domain

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"testing"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

func testRequest() ReviewRequest {
	return ReviewRequest{
		JobID:         testJobID,
		RepoURL:       "git@github.com:org/repo.git",
		BaseBranch:    "main",
		FeatureBranch: "develop",
		Mode:          "code",
		ModelName:     "gemini-2.5-pro",
		StorageURI:    "gs://review-bucket/reviews/" + testJobID + "/report.json",
	}
}

func TestNewStatusByState(t *testing.T) {
	req := testRequest()

	tests := []struct {
		name        string
		status      JobStatus
		wantState   jobstatus.State
		wantOutcome review.Status
	}{
		{"queued", NewQueuedStatus(req), jobstatus.StateQueued, ""},
		{"running", NewRunningStatus(req), jobstatus.StateRunning, ""},
		{"failed", NewFailedStatus(req, errors.New("boom")), jobstatus.StateFailed, review.StatusFailed},
		{"succeeded", NewSucceededStatus(req, review.StatusSucceeded), jobstatus.StateSucceeded, review.StatusSucceeded},
		{"skipped", NewSucceededStatus(req, review.StatusSkipped), jobstatus.StateSucceeded, review.StatusSkipped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.status.State != tt.wantState {
				t.Errorf("State = %q, want %q", tt.status.State, tt.wantState)
			}
			if tt.status.Outcome != tt.wantOutcome {
				t.Errorf("Outcome = %q, want %q", tt.status.Outcome, tt.wantOutcome)
			}
			if tt.status.RepoURL != req.RepoURL || tt.status.FeatureBranch != req.FeatureBranch {
				t.Errorf("the request contents were not carried over: %+v", tt.status)
			}
			if tt.status.Command != CommandReview {
				t.Errorf("Command = %q, want %q", tt.status.Command, CommandReview)
			}
		})
	}
}

// A skip is a normal end for the job. State alone cannot tell it from completion, so Outcome
// must distinguish them.
func TestSkippedIsSucceededJobWithDistinctOutcome(t *testing.T) {
	skipped := NewSucceededStatus(testRequest(), review.StatusSkipped)

	if skipped.State != jobstatus.StateSucceeded {
		t.Fatalf("State = %q, want succeeded", skipped.State)
	}
	if skipped.Outcome == review.StatusSucceeded {
		t.Fatal("skipped and succeeded cannot be told apart")
	}
	if skipped.HasReport() {
		t.Error("a skip claims to have an artifact")
	}
}

func TestNewFailedStatusRecordsCause(t *testing.T) {
	status := NewFailedStatus(testRequest(), review.WrapStep(review.StepReview, errors.New("timeout")))

	if status.Error == "" {
		t.Fatal("the failure reason is not recorded")
	}
	if status.HasReport() {
		t.Error("a failure claims to have an artifact")
	}
}

// The worker rebuilds the status from the task at every transition, so values settled later
// (the result's location, the decision, the outcome) are lost unless carried over.
func TestCarryOverExtras(t *testing.T) {
	prev := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	prev.ReportURI = "gs://review-bucket/reviews/" + testJobID + "/report.json"
	prev.Decision = review.DecisionMinor

	next := NewRunningStatus(testRequest())
	CarryOverExtras(&next, &prev)

	if next.ReportURI != prev.ReportURI {
		t.Errorf("ReportURI = %q, want %q", next.ReportURI, prev.ReportURI)
	}
	if next.Decision != prev.Decision {
		t.Errorf("Decision = %q, want %q", next.Decision, prev.Decision)
	}
	if next.Outcome != prev.Outcome {
		t.Errorf("Outcome = %q, want %q", next.Outcome, prev.Outcome)
	}
}

// When this record has a value, the previous value must not overwrite it.
func TestCarryOverExtrasKeepsNewerValues(t *testing.T) {
	prev := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	prev.Decision = review.DecisionNone

	next := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	next.Decision = review.DecisionBlocker

	CarryOverExtras(&next, &prev)

	if next.Decision != review.DecisionBlocker {
		t.Errorf("Decision = %q, want %q", next.Decision, review.DecisionBlocker)
	}
}

func TestCarryOverExtrasWithoutPrevious(t *testing.T) {
	next := NewRunningStatus(testRequest())
	CarryOverExtras(&next, nil)

	if next.ReportURI != "" || next.Decision != "" {
		t.Errorf("values are set although there is no previous record: %+v", next)
	}
}

// Embedding flattens the JSON. Nested, the stored status.json files would become unreadable.
func TestJobStatusJSONIsFlat(t *testing.T) {
	status := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	status.Decision = review.DecisionMinor

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	for _, key := range []string{"job_id", "state", "command", "repo_url", "base_branch", "feature_branch", "outcome", "decision"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("%s is not at the top level: %s", key, data)
		}
	}
	if _, nested := raw["Status"]; nested {
		t.Errorf("the common fields are nested: %s", data)
	}
}

// Empty fields are not written. With an empty decision in a running job's status.json, a
// reader could not tell "no decision" from "not decided yet".
func TestJobStatusOmitsEmptyFields(t *testing.T) {
	data, err := json.Marshal(NewRunningStatus(testRequest()))
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	for _, key := range []string{"outcome", "decision", "report_uri", "error"} {
		if _, ok := raw[key]; ok {
			t.Errorf("the undecided %s is written: %s", key, data)
		}
	}
}

// Measurements are not lost when the status is rewritten later.
//
// The worker rebuilds JobStatus from the task at every transition, so without carrying them
// over, the material for recalibrating the limits would vanish with the last write.
func TestCarryOverExtrasKeepsMetrics(t *testing.T) {
	prev := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	prev.Truncated = true
	prev.Metrics = Metrics{DiffBytes: 327680, DurationMS: 152_000, ToolCalls: 5}

	next := NewRunningStatus(testRequest())
	CarryOverExtras(&next, &prev)

	if next.Metrics != prev.Metrics {
		t.Errorf("Metrics = %+v, want %+v", next.Metrics, prev.Metrics)
	}
	if !next.Truncated {
		t.Error("Truncated was not carried over")
	}
}

// When this record has measurements, the previous values must not overwrite them.
func TestCarryOverExtrasKeepsNewerMetrics(t *testing.T) {
	prev := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	prev.Metrics = Metrics{DiffBytes: 1}

	next := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	next.Metrics = Metrics{DiffBytes: 327680}

	CarryOverExtras(&next, &prev)

	if next.Metrics.DiffBytes != 327680 {
		t.Errorf("DiffBytes = %d, want 327680", next.Metrics.DiffBytes)
	}
}

// Numbers and booleans are omitzero.
//
// status.json is written by jobstatus.Store, which uses encoding/json/v2. v2's omitempty
// judges by "does it encode to an empty JSON value", so it does not drop 0 and false. Back on
// omitempty, even a job with no measurements yet would record truncated:false and metrics full
// of zeros. TestJobStatusOmitsEmptyFields above encodes with v1, so it cannot catch this.
func TestJobStatusOmitsZeroNumbersUnderJSONV2(t *testing.T) {
	data, err := jsonv2.Marshal(NewRunningStatus(testRequest()))
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	for _, key := range []string{"truncated", "metrics"} {
		if _, ok := raw[key]; ok {
			t.Errorf("%s without a value is written: %s", key, data)
		}
	}

	// Even with partly filled measurements, fields still at 0 are not written. omitzero on
	// metrics itself only applies when everything is zero, so this is the real test.
	partial := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	partial.Metrics = Metrics{DurationMS: 152_000}

	data, err = jsonv2.Marshal(partial)
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	metrics, ok := raw["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("metrics is missing: %s", data)
	}
	if _, ok := metrics["duration_ms"]; !ok {
		t.Errorf("duration_ms was dropped: %s", data)
	}
	for _, key := range []string{"diff_bytes", "prompt_tokens", "output_tokens", "thought_tokens", "tool_calls"} {
		if _, ok := metrics[key]; ok {
			t.Errorf("%s still at 0 is written: %s", key, data)
		}
	}
}
