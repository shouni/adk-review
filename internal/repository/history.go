// Package repository reads the review history on GCS.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/shouni/go-job-kit/cache"
	"github.com/shouni/go-job-kit/joblist"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-review-kit/review"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/adk-review/internal/domain"
)

// loadConcurrency is the concurrency when fetching one page of metadata.
const loadConcurrency = 10

// maxReportBytes is the read limit for report.json.
// The AI's output is constrained by the schema, but this is a safeguard against loading a
// broken object whole into memory.
const maxReportBytes = 8 << 20 // 8 MiB

// History reads and writes the review history on GCS.
//
// What each method returns is defined by domain.HistoryRepository. Only what is specific to this
// implementation (handling failures of parallel loads, the scope of the deletion walk) is noted here.
type History struct {
	storage remoteio.Store
	store   domain.StatusStore
	layout  domain.StorageLayout
	ids     *cache.IDList
	logger  *slog.Logger
}

var _ domain.HistoryRepository = (*History)(nil)

// NewHistory builds a History.
func NewHistory(
	storage remoteio.Store,
	store domain.StatusStore,
	layout domain.StorageLayout,
) *History {
	return &History{
		storage: storage,
		store:   store,
		layout:  layout,
		ids:     cache.NewIDList(cache.DefaultIDListTTL),
		logger:  slog.Default().With("collection", "reviews"),
	}
}

// List sorts the IDs collected by walking the prefix and reads the progress for that page only.
// What it returns is defined by domain.HistoryRepository.
func (h *History) List(ctx context.Context, page, perPage int) (domain.HistoryPage, error) {
	jobIDs, err := h.listJobIDs(ctx)
	if err != nil {
		return domain.HistoryPage{}, fmt.Errorf("failed to list the review history: %w", err)
	}

	// LoadPage handles each load failure by "drop the row and continue". That suits a job with no
	// record, but applied to read failures too, the job would just vanish from the list with a 200
	// and an outage would not look like one. So the kind is told apart and picked up again.
	var failure loadFailure

	// Sorted by the time embedded in the ID. Lexical order is not relied on because when the
	// minted prefix changes or IDs minted by another service mix in, the prefix difference
	// takes effect before the time and the order ignores the date.
	items, meta, err := paging.LoadPage(ctx, jobIDs, page, perPage, jobid.SortKey, failure.wrap(h.loadStatus),
		paging.WithConcurrency(loadConcurrency),
		paging.WithLogger(h.logger),
	)
	if err != nil {
		return domain.HistoryPage{}, fmt.Errorf("failed to load the review history: %w", err)
	}
	if err := failure.err(); err != nil {
		return domain.HistoryPage{}, fmt.Errorf("failed to load the review history: %w", err)
	}

	return domain.HistoryPage{Items: items, Meta: meta}, nil
}

// loadFailure holds the first failure among the loads running in parallel.
//
// LoadPage does not return load errors to the caller, so they are caught here.
type loadFailure struct {
	mu    sync.Mutex
	first error
}

// wrap wraps a load function so its failure can be recorded.
func (f *loadFailure) wrap(
	load func(context.Context, string) (domain.JobStatus, error),
) func(context.Context, string) (domain.JobStatus, error) {
	return func(ctx context.Context, jobID string) (domain.JobStatus, error) {
		status, err := load(ctx, jobID)
		if err != nil {
			f.mu.Lock()
			if f.first == nil {
				f.first = err
			}
			f.mu.Unlock()
		}
		return status, err
	}
}

// err returns the first failure recorded.
func (f *loadFailure) err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.first
}

// Get reads the progress, and report.json when there is an artifact, and builds one entry.
func (h *History) Get(ctx context.Context, jobID string) (domain.ReviewDetail, error) {
	status, err := h.store.Get(ctx, jobID)
	if err != nil {
		// Wrapped with %w. The caller uses errors.Is to split ErrNotFound (404) from the rest
		// (500), so only context is added and the kind is kept.
		return domain.ReviewDetail{}, fmt.Errorf("failed to get the progress (job_id: %s): %w", jobID, err)
	}

	detail := domain.ReviewDetail{Status: status}
	if !status.HasReport() {
		// Running, failed and skipped jobs have no artifact. The detail is built from the progress alone.
		return detail, nil
	}

	report, err := h.loadReport(ctx, status.ReportURI)
	if err != nil {
		// Even when the result itself cannot be read, the progress is shown. Following what
		// happened on screen leaves more clues than returning a 404.
		h.logger.WarnContext(ctx, "failed to load the review result",
			"job_id", jobID, "uri", status.ReportURI, "error", err)
		return detail, nil
	}

	detail.Report = &report
	return detail, nil
}

// Delete walks the job's prefix and removes it. The deleter therefore need not know "what the
// job made", and new kinds of artifact need no change here (the progress lives under the same
// prefix, so it goes too).
func (h *History) Delete(ctx context.Context, jobID string) error {
	safeJobID, err := jobid.Sanitize(jobID)
	if err != nil {
		return err
	}

	if _, err := remoteio.DeletePrefix(ctx, h.storage, h.layout.JobPrefixURI(safeJobID)); err != nil {
		return err
	}

	// The cached ID list is dropped so the deleted job does not stay listed.
	// Otherwise the unreadable ID would sit as an empty row of the job ID alone until the TTL.
	h.Invalidate()
	return nil
}

// Invalidate drops the review prefix's ID list from the cache.
func (h *History) Invalidate() {
	h.ids.Invalidate(h.layout.ReviewPrefixURI())
}

// loadStatus reads one entry's progress.
//
// An ID with no record yet is not dropped from the list; it stays as a row of the job ID alone.
// Gone from the list, a submitted review could no longer be followed on screen.
//
// ★ That applies only to ErrNotFound. Treating a failed read (revoked permission, storage
// outage) the same way would turn the list into "200 OK with empty rows of job IDs only", and an
// outage would not look like one. The store returns the two as different errors, so the latter
// is raised to the caller.
func (h *History) loadStatus(ctx context.Context, jobID string) (domain.JobStatus, error) {
	status, err := h.store.Get(ctx, jobID)
	switch {
	case err == nil:
		return status, nil
	case errors.Is(err, jobstatus.ErrNotFound):
		return domain.JobStatus{JobID: jobID}, nil
	default:
		h.logger.ErrorContext(ctx, "failed to load the progress", "job_id", jobID, "error", err)
		return domain.JobStatus{}, fmt.Errorf("could not load the progress (job_id: %s): %w", jobID, err)
	}
}

// listJobIDs collects the job IDs directly under the prefix.
//
// joblist.Collect owns the walk itself. Receiving one job as one entry by giving a delimiter is
// shared with the sibling apps, and joblist was extracted to gather that walk. Rewriting it here
// would silently lose details such as deduplication and fixing the prefix's trailing slash.
//
// The collected IDs are not filtered. Whether directories created before minting existed are
// dropped from the list is the reader's decision; here the material is passed on intact.
func (h *History) listJobIDs(ctx context.Context) ([]string, error) {
	prefix := h.layout.ReviewPrefixURI()

	return h.ids.Load(ctx, prefix, func(ctx context.Context) ([]string, error) {
		return joblist.Collect(ctx, h.storage, prefix)
	})
}

// loadReport reads report.json.
func (h *History) loadReport(ctx context.Context, uri string) (review.Report, error) {
	rc, err := h.storage.Open(ctx, uri)
	if err != nil {
		return review.Report{}, fmt.Errorf("could not open the review result: %w", err)
	}
	defer func() { _ = rc.Close() }()

	body, err := io.ReadAll(io.LimitReader(rc, maxReportBytes))
	if err != nil {
		return review.Report{}, fmt.Errorf("failed to read the review result: %w", err)
	}

	var report review.Report
	if err := json.Unmarshal(body, &report); err != nil {
		return review.Report{}, fmt.Errorf("failed to parse the review result: %w", err)
	}
	return report, nil
}
