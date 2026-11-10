package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/shouni/gcp-kit/worker"
	"github.com/shouni/go-job-kit/jobstatus"
	reviewpipeline "github.com/shouni/go-review-kit/pipeline"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/domain"
)

// stubSource is a fake diff source. It lets tests observe the context Diff receives.
type stubSource struct {
	seen func(ctx context.Context)
	diff string
	err  error
}

func (s stubSource) Diff(ctx context.Context, _, _ string) (string, error) {
	if s.seen != nil {
		s.seen(ctx)
	}
	if s.err != nil {
		return "", s.err
	}
	// Like the real diff retrieval, it returns an error once cut off.
	// Returning success here would keep the deadline tests from reproducing a deadline.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.diff != "" {
		return s.diff, nil
	}
	return "diff --git a/main.go b/main.go", nil
}

func (s stubSource) CheckoutHead(context.Context, string) (string, error) { return "/tmp/workdir", nil }

func (s stubSource) Close(context.Context) error { return nil }

type stubFactory struct{ source stubSource }

func (f stubFactory) Open(context.Context, review.Request) (review.DiffSource, error) {
	return f.source, nil
}

type stubPrompts struct{}

func (stubPrompts) Generate(_, _ string) (string, error) { return "prompt", nil }

type stubReviewer struct{}

func (stubReviewer) Review(
	context.Context, string, string, review.Workspace,
) (review.Report, review.RunInfo, error) {
	return review.Report{
		Title:   "レビュー結果",
		Verdict: review.Verdict{Decision: review.DecisionNone, Reason: "問題なし"},
	}, review.RunInfo{}, nil
}

// stubPublisher is a fake publisher. It lets tests observe the context at save time.
type stubPublisher struct {
	seen func(ctx context.Context)
	err  error
}

func (p stubPublisher) Publish(ctx context.Context, _ review.Request, _ review.Report) error {
	if p.seen != nil {
		p.seen(ctx)
	}
	return p.err
}

// stubNotifier is a fake notifier. It lets tests observe the context at notification time.
type stubNotifier struct {
	mu     sync.Mutex
	calls  int
	ctxErr error
}

func (n *stubNotifier) Notify(ctx context.Context, _ review.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.calls++
	n.ctxErr = ctx.Err()
	return nil
}

// result returns the number of notifications and the context error the last one received.
func (n *stubNotifier) result() (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls, n.ctxErr
}

// stubStore is a fake progress store. It records the saved states in order.
type stubStore struct {
	mu    sync.Mutex
	saved []domain.JobStatus
	// With getErr set, reads fail with it (reproducing an unreadable status).
	getErr error
}

func (s *stubStore) Get(_ context.Context, jobID string) (domain.JobStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.getErr != nil {
		return domain.JobStatus{}, s.getErr
	}
	for _, v := range slices.Backward(s.saved) {
		if v.JobID == jobID {
			return v, nil
		}
	}
	// No record returns ErrNotFound, as the real store does. Recorder.Begin proceeds on it as
	// "the first run", and treats any other failure as "unreadable" and does not run.
	return domain.JobStatus{}, jobstatus.ErrNotFound
}

func (s *stubStore) Save(_ context.Context, jobID string, status domain.JobStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	status.JobID = jobID
	s.saved = append(s.saved, status)
	return nil
}

func (s *stubStore) states() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	states := make([]string, 0, len(s.saved))
	for _, status := range s.saved {
		states = append(states, string(status.State))
	}
	return states
}

// newTestPipeline builds a pipeline with the observation hooks plugged in.
func newTestPipeline(t *testing.T, source stubSource, publisher stubPublisher, opts ...reviewpipeline.Option) reviewRunner {
	t.Helper()
	return newTestPipelineWith(t, source, publisher, &stubNotifier{}, opts...)
}

func newTestPipelineWith(
	t *testing.T,
	source stubSource,
	publisher stubPublisher,
	notifier review.Notifier,
	opts ...reviewpipeline.Option,
) reviewRunner {
	t.Helper()

	p, err := reviewpipeline.New(reviewpipeline.Deps{
		Sources:           stubFactory{source: source},
		Prompts:           stubPrompts{},
		WorkspaceReviewer: stubReviewer{},
		Publisher:         publisher,
		Notifier:          notifier,
	}, opts...)
	if err != nil {
		t.Fatalf("failed to build the pipeline: %v", err)
	}
	return p
}

func testDomainRequest() domain.ReviewRequest {
	return domain.ReviewRequest{
		JobID:         "20260810-213000-a1b2c3d4",
		RepoURL:       "git@github.com:org/repo.git",
		BaseBranch:    "main",
		FeatureBranch: "develop",
		Mode:          "code",
		ModelName:     "gemini-2.5-pro",
		StorageURI:    "gs://bucket/reviews/20260810-213000-a1b2c3d4/report.json",
	}
}

// ★ PIPELINE_TIMEOUT applies to the review.
//
// The library (reviewpipeline.WithRunTimeout) owns the deadline, but if it were not passed, the
// Cloud Tasks dispatch deadline would come first, the process would be SIGTERMed, and neither a
// failure record nor a Slack notice would be left. This pins the wiring.
func TestRunner_PutsADeadlineOnTheReview(t *testing.T) {
	t.Parallel()

	var reviewDeadline time.Time
	var hasDeadline bool
	const timeout = 25 * time.Minute
	runner := newTestPipeline(t,
		stubSource{seen: func(ctx context.Context) { reviewDeadline, hasDeadline = ctx.Deadline() }},
		stubPublisher{},
		reviewpipeline.WithRunTimeout(timeout),
	)

	before := time.Now()
	if err := NewRunner(runner, &stubStore{}).Execute(context.Background(), testDomainRequest()); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	if !hasDeadline {
		t.Fatal("the review context has no deadline (PIPELINE_TIMEOUT does not apply)")
	}
	if d := reviewDeadline.Sub(before); d < timeout-time.Minute || d > timeout+time.Minute {
		t.Errorf("time to deadline = %s, want ≈ %s", d, timeout)
	}
}

// 0 or below is unlimited. The escape hatch for long local debugging must stay open.
func TestRunner_ZeroMeansNoLimit(t *testing.T) {
	t.Parallel()

	var hasDeadline bool
	runner := newTestPipeline(t,
		stubSource{seen: func(ctx context.Context) { _, hasDeadline = ctx.Deadline() }},
		stubPublisher{},
	)

	if err := NewRunner(runner, &stubStore{}).Execute(context.Background(), testDomainRequest()); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if hasDeadline {
		t.Error("a deadline is set although timeout=0")
	}
}

// The notification still goes out after a cut-off at the deadline (an integration check that
// the library's detachment works).
//
// When cut off there is no result to save, so the Publisher is not called. This is exactly when
// a report is needed, so it checks that the Notifier is called with an unexpired context.
func TestRunner_NotifiesAfterTheCutOff(t *testing.T) {
	t.Parallel()

	notifier := &stubNotifier{}
	runner := newTestPipelineWith(t,
		stubSource{seen: func(ctx context.Context) {
			<-ctx.Done() // wait for the deadline = make the review cut off
		}},
		stubPublisher{},
		notifier,
		reviewpipeline.WithRunTimeout(10*time.Millisecond),
	)

	if err := NewRunner(runner, &stubStore{}).Execute(context.Background(), testDomainRequest()); err == nil {
		t.Fatal("the cut-off is not returned as an error")
	}

	calls, ctxErr := notifier.result()
	if calls == 0 {
		t.Fatal("no notification after the cut-off")
	}
	if calls != 1 {
		t.Errorf("notifications = %d, want 1", calls)
	}
	if ctxErr != nil {
		t.Errorf("the notification context has expired: %v", ctxErr)
	}
}

// The start of the run is recorded and the attempt count is incremented.
func TestRunner_RecordsTheStart(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	runner := newTestPipeline(t, stubSource{}, stubPublisher{})

	if err := NewRunner(runner, store).Execute(context.Background(), testDomainRequest()); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	states := store.states()
	if len(states) == 0 || states[0] != "running" {
		t.Fatalf("first record = %v, want running", states)
	}
	if store.saved[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", store.saved[0].Attempts)
	}
}

// A redelivered finished task stops without running the review.
// Cloud Tasks delivers at least once; without this the AI calls would run twice.
func TestRunner_StopsARedeliveryOfASucceededJob(t *testing.T) {
	t.Parallel()

	req := testDomainRequest()
	store := &stubStore{}
	if err := store.Save(context.Background(), req.JobID, domain.NewSucceededStatus(req, review.StatusSucceeded)); err != nil {
		t.Fatalf("failed to write the prior record: %v", err)
	}

	reviewed := false
	runner := newTestPipeline(t,
		stubSource{seen: func(context.Context) { reviewed = true }},
		stubPublisher{},
	)

	if err := NewRunner(runner, store).Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if reviewed {
		t.Error("the review ran although the task had finished")
	}
}

// When the status cannot be read, the review does not run and an error is returned for
// redelivery (worker convention 2.1, item 5). It used to proceed, reasoning that "running twice
// is easier to recover from", but proceeding rebuilds a finished job and pays the very cost this
// guard exists to prevent.
func TestRunner_DoesNotRunWhenTheStatusIsUnreadable(t *testing.T) {
	t.Parallel()

	unavailable := errors.New("gcs unavailable")
	store := &stubStore{getErr: unavailable}

	reviewed := false
	runner := newTestPipeline(t,
		stubSource{seen: func(context.Context) { reviewed = true }},
		stubPublisher{},
	)

	err := NewRunner(runner, store).Execute(context.Background(), testDomainRequest())
	if !errors.Is(err, unavailable) {
		t.Fatalf("Execute() error = %v, want %v", err, unavailable)
	}
	if errors.Is(err, worker.ErrPermanent) {
		t.Error("an unreadable status became Permanent (it should stay a 500, since redelivery may fix it)")
	}
	if reviewed {
		t.Error("the review ran although the status could not be read")
	}
	if states := store.states(); len(states) != 0 {
		t.Errorf("records were written although the status could not be read: %v", states)
	}
}

// The outcome is recorded from Run's return values. It used to ride on the notification hook,
// but now that Run returns the Report it can be built directly.
func TestRunner_RecordsTheOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      stubSource
		publisher   stubPublisher
		wantState   string
		wantOutcome review.Status
		wantReport  bool
	}{
		{
			name:        "success",
			wantState:   "succeeded",
			wantOutcome: review.StatusSucceeded,
			wantReport:  true,
		},
		{
			name:        "no diff",
			source:      stubSource{diff: "   "},
			wantState:   "succeeded",
			wantOutcome: review.StatusSkipped,
		},
		{
			name:        "diff retrieval fails",
			source:      stubSource{err: errors.New("boom")},
			wantState:   "failed",
			wantOutcome: review.StatusFailed,
		},
		{
			name:        "save fails",
			publisher:   stubPublisher{err: errors.New("gcs down")},
			wantState:   "failed",
			wantOutcome: review.StatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubStore{}
			runner := newTestPipeline(t, tt.source, tt.publisher)

			_ = NewRunner(runner, store).Execute(context.Background(), testDomainRequest())

			// The first record is running, the last is the outcome.
			if len(store.saved) < 2 {
				t.Fatalf("records = %d, want at least 2 (%v)", len(store.saved), store.states())
			}

			got := store.saved[len(store.saved)-1]
			if string(got.State) != tt.wantState {
				t.Errorf("State = %q, want %q", got.State, tt.wantState)
			}
			if got.Outcome != tt.wantOutcome {
				t.Errorf("Outcome = %q, want %q", got.Outcome, tt.wantOutcome)
			}
			if got.HasReport() != tt.wantReport {
				t.Errorf("HasReport() = %v, want %v", got.HasReport(), tt.wantReport)
			}
			if tt.wantState == "failed" && got.Error == "" {
				t.Error("the failure reason is not recorded")
			}
		})
	}
}

// On success the review's contents (title, decision) are recorded too; they become a row of the history list.
func TestRunner_PutsTheReviewOnTheOutcome(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	runner := newTestPipeline(t, stubSource{}, stubPublisher{})

	req := testDomainRequest()
	if err := NewRunner(runner, store).Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	got := store.saved[len(store.saved)-1]
	if got.Title != "レビュー結果" {
		t.Errorf("Title = %q, want %q", got.Title, "レビュー結果")
	}
	if got.Decision != review.DecisionNone {
		t.Errorf("Decision = %q, want %q", got.Decision, review.DecisionNone)
	}
	if got.ReportURI != req.StorageURI {
		t.Errorf("ReportURI = %q, want %q", got.ReportURI, req.StorageURI)
	}
}

// The outcome is recorded even when the review is cut off at the deadline.
// Recording with the expired context would leave nothing exactly when the record matters most.
func TestRunner_RecordsTheOutcomeAfterTheCutOff(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	runner := newTestPipeline(t,
		stubSource{seen: func(ctx context.Context) { <-ctx.Done() }},
		stubPublisher{},
		reviewpipeline.WithRunTimeout(10*time.Millisecond),
	)

	_ = NewRunner(runner, store).Execute(context.Background(), testDomainRequest())

	states := store.states()
	if len(states) < 2 || states[len(states)-1] != "failed" {
		t.Fatalf("the cut-off is not recorded: %v", states)
	}
}

// ★ Measurements are recorded for failed runs too. Kept only on success, the history would
// lack the tail most worth seeing when recalibrating the limits (the reason is inside
// buildOutcomeStatus).
func TestBuildOutcomeStatusRecordsMetrics(t *testing.T) {
	t.Parallel()

	req := testDomainRequest()
	result := review.Result{
		Status:    review.StatusSucceeded,
		Duration:  152 * time.Second,
		DiffBytes: 327680,
		Run: review.RunInfo{
			Truncated:     true,
			PromptTokens:  219062,
			OutputTokens:  63950,
			ThoughtTokens: 1579,
			ToolCalls:     5,
		},
	}
	want := domain.Metrics{
		DiffBytes:     327680,
		DurationMS:    152_000,
		PromptTokens:  219062,
		OutputTokens:  63950,
		ThoughtTokens: 1579,
		ToolCalls:     5,
	}

	report := &review.Report{Title: "レビュー", Verdict: review.Verdict{Decision: review.DecisionMinor}}

	t.Run("success", func(t *testing.T) {
		got := buildOutcomeStatus(req, result, report, nil)
		if got.Metrics != want {
			t.Errorf("Metrics = %+v, want %+v", got.Metrics, want)
		}
		if !got.Truncated {
			t.Error("Truncated was not carried over")
		}
		if got.Title != "レビュー" || got.ReportURI != req.StorageURI {
			t.Errorf("artifact information is missing: %+v", got)
		}
	})

	t.Run("kept on failure too", func(t *testing.T) {
		failed := result
		failed.Status = review.StatusFailed

		got := buildOutcomeStatus(req, failed, nil, errors.New("boom"))
		if got.State != jobstatus.StateFailed {
			t.Fatalf("State = %q, want failed", got.State)
		}
		if got.Metrics != want {
			t.Errorf("on failure Metrics = %+v, want %+v", got.Metrics, want)
		}
		if got.ReportURI != "" {
			t.Errorf("an artifact URI is set although it failed: %q", got.ReportURI)
		}
	})
}

// TestClassifyForRetry pins the split between failures redelivery can fix and those it cannot.
//
// Getting it wrong breaks quietly either way. Retrying a permanent failure sends the same notice
// twice; treating a transient failure as permanent ends a job redelivery would have fixed as a
// success, and it never runs again.
func TestClassifyForRetry(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err           error
		wantPermanent bool
	}{
		"success":                      {err: nil},
		"invalid request":              {err: review.ErrInvalidRequest, wantPermanent: true},
		"empty diff":                   {err: review.ErrEmptyDiff, wantPermanent: true},
		"diff too large":               {err: review.ErrDiffTooLarge, wantPermanent: true},
		"ref not found":                {err: review.ErrRefNotFound, wantPermanent: true},
		"unsupported repository URL":   {err: review.ErrUnsupportedRepoURL, wantPermanent: true},
		"classified even when wrapped": {err: fmt.Errorf("failed to get the diff: %w", review.ErrDiffTooLarge), wantPermanent: true},
		"empty model response retries": {err: review.ErrEmptyResponse},
		"broken output retries":        {err: review.ErrInvalidReport},
		"unknown failure retries":      {err: errors.New("vertex ai: 503")},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := classifyForRetry(tt.err)
			if tt.err == nil {
				if got != nil {
					t.Fatalf("classifyForRetry(nil) = %v, want nil", got)
				}
				return
			}
			if permanent := errors.Is(got, worker.ErrPermanent); permanent != tt.wantPermanent {
				t.Errorf("permanent = %v, want %v", permanent, tt.wantPermanent)
			}
			// The original cause stays reachable after classification (otherwise the record and the notice cannot state the reason).
			if !errors.Is(got, tt.err) {
				t.Errorf("the cause was lost: %v", got)
			}
		})
	}
}
