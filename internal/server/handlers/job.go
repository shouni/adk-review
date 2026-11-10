package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
)

const (
	// jobsBasePath is the jobs path. A job is addressed under it from submission to deletion,
	// and it also builds the detail and artifact URLs.
	jobsBasePath = "/jobs"
	// reportSegment is the trailing segment under a job that names the full findings.
	reportSegment = "report"
)

// JobDetailPageData is the data passed to the job detail template.
type JobDetailPageData struct {
	Detail domain.ReviewDetail
	Error  string
	// CSRFToken is the value put in the delete request's header. It is not a form submission,
	// so JavaScript reads it from a hidden field.
	CSRFToken string
}

// jobURL builds the absolute URL of one job (and the artifacts under it).
func (h *Handler) jobURL(jobID string, segments ...string) string {
	u, err := url.JoinPath(h.cfg.Server.ServiceURL, append([]string{jobsBasePath, jobID}, segments...)...)
	if err != nil {
		// ServiceURL is checked at startup and jobID is sanitised, so this does not normally happen.
		return ""
	}
	return u
}

// jobResponse is the JSON response of GET /jobs/{jobID}.
//
// It embeds domain.JobStatus flat because this is the single endpoint polled from right after
// submission. The full findings are not included; ReportURL points at GET /jobs/{jobID}/report.
// Returning the full text on every completion check would be too heavy.
type jobResponse struct {
	domain.JobStatus
	// ReportURL is where the full findings are fetched. Omitted while there is no artifact.
	ReportURL string `json:"report_url,omitempty"`
}

// HandleJob returns one job.
//
// JSON gives the progress and a link to the artifact, HTML the detail page. The same URL names
// the job from the moment it is submitted until it is deleted, so callers need not switch "which
// one to call now" by state. The HTML includes the full text because a separate source would be
// pointless for the screen.
func (h *Handler) HandleJob(w http.ResponseWriter, r *http.Request) {
	if !respond.WantsJSON(w, r) {
		h.renderJobDetail(w, r)
		return
	}

	ctx := r.Context()
	safeJobID, err := jobid.Sanitize(chi.URLParam(r, "jobID"))
	if err != nil {
		slog.WarnContext(ctx, "received an invalid job ID", "error", err)
		respond.ErrorJSON(w, r, http.StatusBadRequest, "ジョブIDの形式が不正です。")
		return
	}

	status, err := h.statusStore.Get(ctx, safeJobID)
	if err != nil {
		code := recordErrorStatus(err)
		if code == http.StatusNotFound {
			slog.WarnContext(ctx, "review history not found", "job_id", safeJobID, "error", err)
			respond.ErrorJSON(w, r, code, "指定されたレビューは見つかりませんでした。")
			return
		}
		slog.ErrorContext(ctx, "failed to get the progress", "job_id", safeJobID, "error", err)
		respond.ErrorJSON(w, r, code, "進行状況を取得できませんでした。")
		return
	}

	out := jobResponse{JobStatus: status}
	if status.HasReport() {
		out.ReportURL = h.jobURL(safeJobID, reportSegment)
	}
	respond.JSON(w, r, http.StatusOK, out)
}

// HandleJobReport returns the full review result (JSON only).
//
// 409 while running, 404 when finished without an artifact (failed, skipped). Both are "not
// there", but the former appears with waiting, so the status code tells callers whether a retry
// makes sense.
func (h *Handler) HandleJobReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	safeJobID, err := jobid.Sanitize(chi.URLParam(r, "jobID"))
	if err != nil {
		slog.WarnContext(ctx, "received an invalid job ID", "error", err)
		respond.ErrorJSON(w, r, http.StatusBadRequest, "ジョブIDの形式が不正です。")
		return
	}

	detail, err := h.history.Get(ctx, safeJobID)
	if err != nil {
		code := recordErrorStatus(err)
		if code == http.StatusNotFound {
			slog.WarnContext(ctx, "review history not found", "job_id", safeJobID, "error", err)
			respond.ErrorJSON(w, r, code, "指定されたレビューは見つかりませんでした。")
			return
		}
		slog.ErrorContext(ctx, "failed to get the review history", "job_id", safeJobID, "error", err, "status", code)
		respond.ErrorJSON(w, r, code, "レビューを取得できませんでした。")
		return
	}

	if detail.Report == nil {
		if !detail.Status.Finished() {
			respond.ErrorJSON(w, r, http.StatusConflict, "レビューはまだ実行中です。")
			return
		}
		respond.ErrorJSON(w, r, http.StatusNotFound, "このレビューには成果物がありません。")
		return
	}
	respond.JSON(w, r, http.StatusOK, detail.Report)
}

// renderJobDetail renders one job's detail page.
func (h *Handler) renderJobDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rawJobID := chi.URLParam(r, "jobID")
	safeJobID, err := jobid.Sanitize(rawJobID)
	if err != nil {
		slog.WarnContext(ctx, "received an invalid job ID", "job_id", rawJobID, "error", err)
		h.render(w, r, http.StatusBadRequest, reviewDetailTemplate, JobDetailPageData{Error: "ジョブIDの形式が不正です。"})
		return
	}

	detail, err := h.history.Get(ctx, safeJobID)
	if err != nil {
		code, message := detailFailure(ctx, safeJobID, err)
		h.render(w, r, code, reviewDetailTemplate, JobDetailPageData{Error: message})
		return
	}

	h.render(w, r, http.StatusOK, reviewDetailTemplate, JobDetailPageData{
		Detail:    detail,
		CSRFToken: CSRFTokenFromContext(ctx),
	})
}

// detailFailure logs a failure to read the detail and returns the status code and the user-facing text.
// Only "not there yet" is a 404 (see recordErrorStatus).
func detailFailure(ctx context.Context, jobID string, err error) (int, string) {
	code := recordErrorStatus(err)
	if code == http.StatusNotFound {
		slog.WarnContext(ctx, "review history not found", "job_id", jobID, "error", err)
		return code, "指定されたレビューは見つかりませんでした。"
	}
	slog.ErrorContext(ctx, "failed to get the review history", "job_id", jobID, "error", err, "status", code)
	return code, "レビューを取得できませんでした。"
}

// HandleJobDelete deletes a review from the history (DELETE /jobs/{jobID}).
//
// The DELETE method and the bodiless response match the sibling apps (in the browser,
// App.deleteResource in assets/static/js/app.js calls it). On failure the body goes straight
// into an alert on screen, so it carries no internal details.
func (h *Handler) HandleJobDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	safeJobID, err := jobid.Sanitize(chi.URLParam(r, "jobID"))
	if err != nil {
		slog.WarnContext(ctx, "received an invalid job ID", "error", err)
		respond.Error(w, r, http.StatusBadRequest, "ジョブIDの形式が不正です。")
		return
	}

	detail, err := h.history.Get(ctx, safeJobID)
	if err != nil {
		code := recordErrorStatus(err)
		if code == http.StatusNotFound {
			slog.WarnContext(ctx, "review history not found", "job_id", safeJobID, "error", err)
			respond.Error(w, r, code, "指定されたレビューは見つかりませんでした。")
			return
		}
		slog.ErrorContext(ctx, "failed to get the review history", "job_id", safeJobID, "error", err, "status", code)
		respond.Error(w, r, code, "レビューを取得できませんでした。")
		return
	}

	// The reasoning lives in domain.JobStatus.Deletable. The screen does not show the button,
	// but it is checked here too so a direct call is rejected.
	if !detail.Status.Deletable() {
		slog.WarnContext(ctx, "delete requested for a running review", "job_id", safeJobID, "state", detail.Status.State)
		respond.Error(w, r, http.StatusConflict, "実行中のレビューは削除できません。")
		return
	}

	if err := h.history.Delete(ctx, safeJobID); err != nil {
		respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to delete the review history", "job_id", safeJobID)
		return
	}

	slog.InfoContext(ctx, "deleted the review history", "job_id", safeJobID)
	w.WriteHeader(http.StatusNoContent)
}
