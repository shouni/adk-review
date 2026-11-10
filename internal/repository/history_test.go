package repository

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"testing"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-remote-io/remoteio/memio"

	"github.com/shouni/adk-review/internal/domain"
)

const (
	testBucket = "review-bucket"
	testJobID  = "20260810-213000-a1b2c3d4"
)

// fakeIO is a fake of remoteio reads and writes.
// fakeIO is a storage fake wrapping memio.
//
// memio handles the storage behaviour, such as folding listings and how absence is returned
// (it passes the same conformance suite as the real handlers). All that remains here is
// recording the calls ("which prefix was walked", "what was deleted") and injecting failures.
type fakeIO struct {
	remoteio.Store
	h *memio.Handler

	// objects are the preset objects loaded into memio on init.
	objects   []string
	listErr   error
	deleteErr error

	listedPrefix string
	deleted      []string
}

// ensure builds memio and loads objects into it.
//
// Creation is deferred to the first call so each test can state its preconditions in a struct
// literal. It is idempotent, so whichever entry point is touched first, the state is the same.
func (f *fakeIO) ensure(t *testing.T) {
	t.Helper()
	if f.Store != nil {
		return
	}

	f.h = memio.New(memio.WithScheme(remoteio.SchemeGCS))
	f.Store = remoteio.NewStore(f.h)
	for _, uri := range f.objects {
		if err := f.h.Seed(uri, []byte("x")); err != nil {
			t.Fatalf("seed(%s) error = %v", uri, err)
		}
	}
}

func (f *fakeIO) List(ctx context.Context, name string, opts ...remoteio.ListOption) iter.Seq2[remoteio.Entry, error] {
	f.listedPrefix = name
	if f.listErr != nil {
		return func(yield func(remoteio.Entry, error) bool) { yield(remoteio.Entry{}, f.listErr) }
	}
	return f.Store.List(ctx, name, opts...)
}

func (f *fakeIO) Delete(ctx context.Context, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, name)
	return f.Store.Delete(ctx, name)
}

// fakeStore is a fake progress store.
type fakeStore struct{}

func (fakeStore) Get(context.Context, string) (domain.JobStatus, error) {
	return domain.JobStatus{}, errors.New("not recorded")
}

func (fakeStore) Save(context.Context, string, domain.JobStatus) error { return nil }

func newTestHistory(t *testing.T, fake *fakeIO) *History {
	t.Helper()
	fake.ensure(t)
	return NewHistory(fake, fakeStore{}, domain.NewStorageLayout(testBucket))
}

// Deletion walks the job's prefix. The deleter need not know "what was made", so new kinds of
// artifact need no change here.
func TestDeleteRemovesEverythingUnderTheJobPrefix(t *testing.T) {
	prefix := "gs://" + testBucket + "/reviews/" + testJobID + "/"
	fake := &fakeIO{objects: []string{prefix + "status.json", prefix + "report.json"}}

	if err := newTestHistory(t, fake).Delete(context.Background(), testJobID); err != nil {
		t.Fatalf("failed to delete: %v", err)
	}

	if fake.listedPrefix != prefix {
		t.Errorf("walked prefix = %q, want %q", fake.listedPrefix, prefix)
	}
	if len(fake.deleted) != 2 {
		t.Fatalf("deleted = %d, want 2 (%v)", len(fake.deleted), fake.deleted)
	}
	for _, uri := range fake.deleted {
		if !strings.HasPrefix(uri, prefix) {
			t.Errorf("deleting outside the prefix: %q", uri)
		}
	}
}

// The job ID becomes a storage path element, so it is sanitised before use.
// Unsanitised, the walked prefix could be widened from outside.
func TestDeleteSanitizesJobID(t *testing.T) {
	t.Run("path elements are trimmed", func(t *testing.T) {
		fake := &fakeIO{}

		if err := newTestHistory(t, fake).Delete(context.Background(), "../../etc/passwd"); err != nil {
			t.Fatalf("failed to delete: %v", err)
		}

		want := "gs://" + testBucket + "/reviews/passwd/"
		if fake.listedPrefix != want {
			t.Fatalf("walked prefix = %q, want %q", fake.listedPrefix, want)
		}
	})

	t.Run("rejected when still invalid after sanitising", func(t *testing.T) {
		fake := &fakeIO{}

		if err := newTestHistory(t, fake).Delete(context.Background(), "-bad-id"); err == nil {
			t.Fatal("expected an error, got nil")
		}
		if fake.listedPrefix != "" {
			t.Errorf("walking with an invalid ID: %q", fake.listedPrefix)
		}
	})
}

func TestDeleteReportsFailures(t *testing.T) {
	prefix := "gs://" + testBucket + "/reviews/" + testJobID + "/"

	t.Run("listing fails", func(t *testing.T) {
		fake := &fakeIO{listErr: errors.New("gcs down")}

		err := newTestHistory(t, fake).Delete(context.Background(), testJobID)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if len(fake.deleted) != 0 {
			t.Error("deletion ran although listing failed")
		}
	})

	t.Run("deletion fails", func(t *testing.T) {
		fake := &fakeIO{
			objects:   []string{prefix + "status.json"},
			deleteErr: errors.New("permission denied"),
		}

		if err := newTestHistory(t, fake).Delete(context.Background(), testJobID); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})
}

// An empty walk is not an error (for example when already deleted).
func TestDeleteWithNoObjects(t *testing.T) {
	fake := &fakeIO{}

	if err := newTestHistory(t, fake).Delete(context.Background(), testJobID); err != nil {
		t.Fatalf("failed to delete: %v", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("deleted = %d, want 0", len(fake.deleted))
	}
}

// The bucket root is rejected. Getting this wrong would delete a wide range of the bucket.
// No trailing separator is required (remoteio's List normalises the prefix, so "reviews" and
// "reviews-old/" are not confused). remoteio.DeletePrefix owns the check; this only confirms
// it applies to this layer's storage.
func TestDeletePrefixRefusesBucketRoot(t *testing.T) {
	fake := &fakeIO{}
	history := newTestHistory(t, fake)

	for _, prefix := range []string{"gs://review-bucket", "gs://review-bucket/"} {
		if _, err := remoteio.DeletePrefix(context.Background(), history.storage, prefix); !errors.Is(err, remoteio.ErrInvalidURI) {
			t.Errorf("prefix=%q: err = %v, want ErrInvalidURI", prefix, err)
		}
	}
	if len(fake.deleted) != 0 {
		t.Error("deletion ran with a prefix that should have been rejected")
	}
}

// --- Read paths ---

// stubStore is a fake progress store returning any result per job ID.
type stubStore struct {
	statuses map[string]domain.JobStatus
	errs     map[string]error
}

func (s stubStore) Get(_ context.Context, jobID string) (domain.JobStatus, error) {
	if err, ok := s.errs[jobID]; ok {
		return domain.JobStatus{}, err
	}
	if status, ok := s.statuses[jobID]; ok {
		return status, nil
	}
	return domain.JobStatus{}, jobstatus.ErrNotFound
}

func (stubStore) Save(context.Context, string, domain.JobStatus) error { return nil }

// contentIO is a fakeIO whose Open returns any content.
type contentIO struct {
	*fakeIO
	contents map[string]string
	openErr  error
}

func (c contentIO) Open(_ context.Context, uri string) (io.ReadCloser, error) {
	if c.openErr != nil {
		return nil, c.openErr
	}
	body, ok := c.contents[uri]
	if !ok {
		return nil, errors.New("not found: " + uri)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func jobPrefix(jobID string) string {
	return "gs://" + testBucket + "/reviews/" + jobID + "/"
}

// The list picks up only "pseudo-directories" and returns one row per job.
//
// If the delimiter stopped working, every object underneath would come back, the trailing-slash
// filter would drop them all, and the history list would always be empty.
func TestListReturnsOneRowPerJob(t *testing.T) {
	ids := []string{"20260810-213000-a1b2c3d4", "20260811-090000-b2c3d4e5"}
	fake := &fakeIO{objects: []string{jobPrefix(ids[0]), jobPrefix(ids[1])}}
	store := stubStore{statuses: map[string]domain.JobStatus{
		ids[0]: {Status: jobstatus.Status{JobID: ids[0], State: jobstatus.StateSucceeded}},
		ids[1]: {Status: jobstatus.Status{JobID: ids[1], State: jobstatus.StateSucceeded}},
	}}

	fake.ensure(t)
	h := NewHistory(fake, store, domain.NewStorageLayout(testBucket))
	page, err := h.List(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(page.Items) != len(ids) {
		t.Fatalf("items = %d, want %d (%+v)", len(page.Items), len(ids), page.Items)
	}
}

// The list is ordered newest first by the time embedded in the job ID.
// Without the sort key it regresses to lexical order, and the moment the minted prefix changes
// the order ignores the date.
func TestListSortsNewestFirst(t *testing.T) {
	older := "20260810-213000-a1b2c3d4"
	newer := "20260811-090000-b2c3d4e5"
	// Deliberately returned oldest first, to check that sorting works.
	fake := &fakeIO{objects: []string{jobPrefix(older), jobPrefix(newer)}}
	store := stubStore{statuses: map[string]domain.JobStatus{
		older: {Status: jobstatus.Status{JobID: older}},
		newer: {Status: jobstatus.Status{JobID: newer}},
	}}

	fake.ensure(t)
	h := NewHistory(fake, store, domain.NewStorageLayout(testBucket))
	page, err := h.List(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(page.Items))
	}
	if page.Items[0].JobID != newer {
		t.Errorf("first = %q, want %q (not newest first)", page.Items[0].JobID, newer)
	}
}

// A job with no record yet is not dropped from the list.
// Dropped, a submitted review could no longer be followed on screen.
func TestListKeepsJobsWithoutStatus(t *testing.T) {
	fake := &fakeIO{objects: []string{jobPrefix(testJobID)}}
	store := stubStore{errs: map[string]error{testJobID: jobstatus.ErrNotFound}}

	fake.ensure(t)
	h := NewHistory(fake, store, domain.NewStorageLayout(testBucket))
	page, err := h.List(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %d, want 1 (the unrecorded job was dropped)", len(page.Items))
	}
	if page.Items[0].JobID != testJobID {
		t.Errorf("JobID = %q, want %q", page.Items[0].JobID, testJobID)
	}
}

// ★ A failure of the read itself is not swallowed.
//
// Treated like "no record", a revoked permission or a storage outage would come out as
// "200 OK with empty rows of job IDs only", and an outage would not look like one.
func TestListSurfacesUnavailableStatus(t *testing.T) {
	fake := &fakeIO{objects: []string{jobPrefix(testJobID)}}
	store := stubStore{errs: map[string]error{testJobID: jobstatus.ErrUnavailable}}

	fake.ensure(t)
	h := NewHistory(fake, store, domain.NewStorageLayout(testBucket))
	if _, err := h.List(context.Background(), 1, 20); err == nil {
		t.Fatal("the read failure was swallowed")
	}
}

// The detail reads the report body too.
func TestGetLoadsReport(t *testing.T) {
	uri := jobPrefix(testJobID) + "report.json"
	fake := &fakeIO{}
	rio := contentIO{
		fakeIO:   fake,
		contents: map[string]string{uri: `{"title":"レビュー結果","summary":"要約","verdict":{"decision":"minor"},"findings":[]}`},
	}
	store := stubStore{statuses: map[string]domain.JobStatus{
		testJobID: {
			Status:    jobstatus.Status{JobID: testJobID, State: jobstatus.StateSucceeded},
			ReportURI: uri,
		},
	}}

	fake.ensure(t)
	h := NewHistory(rio, store, domain.NewStorageLayout(testBucket))
	detail, err := h.Get(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("failed to get the detail: %v", err)
	}
	if detail.Report == nil {
		t.Fatal("the report was not loaded")
	}
	if detail.Report.Title != "レビュー結果" {
		t.Errorf("Title = %q", detail.Report.Title)
	}
}

// Even when the report cannot be read, the progress is still shown.
// Being able to follow what happened on screen leaves more clues than returning a 404.
func TestGetKeepsStatusWhenReportUnreadable(t *testing.T) {
	fake := &fakeIO{}
	rio := contentIO{fakeIO: fake, openErr: errors.New("permission denied")}
	store := stubStore{statuses: map[string]domain.JobStatus{
		testJobID: {
			Status:    jobstatus.Status{JobID: testJobID, State: jobstatus.StateSucceeded},
			ReportURI: jobPrefix(testJobID) + "report.json",
		},
	}}

	fake.ensure(t)
	h := NewHistory(rio, store, domain.NewStorageLayout(testBucket))
	detail, err := h.Get(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("the progress was lost too: %v", err)
	}
	if detail.Report != nil {
		t.Error("the unreadable report is set")
	}
	if detail.Status.JobID != testJobID {
		t.Errorf("JobID = %q, want %q", detail.Status.JobID, testJobID)
	}
}

// No record is returned as ErrNotFound unchanged. The handler splits 404 from 500 on it.
func TestGetPreservesNotFound(t *testing.T) {
	fake := &fakeIO{}
	fake.ensure(t)
	h := NewHistory(fake, stubStore{}, domain.NewStorageLayout(testBucket))

	_, err := h.Get(context.Background(), testJobID)
	if !errors.Is(err, jobstatus.ErrNotFound) {
		t.Fatalf("err = %v, cannot be treated as ErrNotFound", err)
	}
}
