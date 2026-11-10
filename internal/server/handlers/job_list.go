package handlers

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
)

const (
	// defaultPerPage is the number of rows per page of the history list.
	defaultPerPage = 20
	// maxPerPage is the limit allowed for per_page. The rows read per page are the round trips
	// to storage, so it cannot grow without bound.
	maxPerPage = 100
)

// JobListPageData is the data passed to the history list template.
type JobListPageData struct {
	Items []domain.JobStatus
	Meta  domain.PageMeta
	Error string
}

// HandleJobList shows the job list (GET /jobs).
func (h *Handler) HandleJobList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page := positiveIntParam(r, "page", 1)
	perPage := clamp(positiveIntParam(r, "per_page", defaultPerPage), 1, maxPerPage)

	result, err := h.history.List(ctx, page, perPage)
	if err != nil {
		// "Not there yet" and "could not be read" are returned as different things (see recordErrorStatus).
		code := recordErrorStatus(err)
		slog.ErrorContext(ctx, "failed to get the review history", "error", err, "status", code)
		const message = "履歴を取得できませんでした。時間をおいて再度お試しください。"
		if respond.WantsJSON(w, r) {
			respond.ErrorJSON(w, r, code, message)
			return
		}
		h.render(w, r, code, historyTemplate, JobListPageData{Error: message})
		return
	}

	if respond.WantsJSON(w, r) {
		respond.JSON(w, r, http.StatusOK, result)
		return
	}
	h.render(w, r, http.StatusOK, historyTemplate, JobListPageData{
		Items: result.Items,
		Meta:  result.Meta,
	})
}

// positiveIntParam reads a positive integer from the query.
// Missing, invalid and values at or below 0 all fall back to the default. Showing the first page
// is easier to deal with than an error page over a mistyped page number.
func positiveIntParam(r *http.Request, name string, fallback int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}
