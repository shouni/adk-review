package domain

import (
	"context"

	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-review-kit/review"
)

// PageMeta is the paging information of the history list. Both the screen and M2M clients
// depend on the existing response shape, so go-job-kit's type is used as is.
type PageMeta = paging.PageMeta

// HistoryPage is one page of the history list.
//
// The JSON tags are explicit because this type is the API response as is. Without them the
// Go field names (Items / Meta) come out capitalised, the only spelling that differs among
// snake_case everywhere else.
type HistoryPage struct {
	Items []JobStatus `json:"items"`
	Meta  PageMeta    `json:"meta"`
}

// ReviewDetail is everything needed to show one history entry in detail.
type ReviewDetail struct {
	Status JobStatus
	// Report is the full review result. It is nil without an artifact (running, failed, skipped).
	Report *review.Report
}

// HistoryRepository lists the history and fetches one entry.
type HistoryRepository interface {
	// List returns page number page, newest first.
	List(ctx context.Context, page, perPage int) (HistoryPage, error)
	// Get returns one entry's progress and full review result.
	Get(ctx context.Context, jobID string) (ReviewDetail, error)
	// Delete removes every object of one entry.
	// Whether it may be deleted is decided by the caller (the handler).
	Delete(ctx context.Context, jobID string) error
	// Invalidate drops the cached list of job IDs. Called right after submitting a new job, it
	// removes the window in which a submitted job does not appear in the history.
	Invalidate()
}

// StatusStore reads and writes job progress.
//
// jobstatus.Store[JobStatus] satisfies it as is, so it is passed without an adapter in between
// (following the shape go-job-kit recommends).
type StatusStore interface {
	Get(ctx context.Context, jobID string) (JobStatus, error)
	Save(ctx context.Context, jobID string, status JobStatus) error
}
