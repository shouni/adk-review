package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
)

// maxSubmitBody is the limit on a submission received as JSON.
// The input is five short strings, so anything larger is a mistake or an attack.
const maxSubmitBody = 16 << 10

// submitInput is the input of a JSON submission.
//
// domain.ReviewRequest is not decoded directly. That would let callers set JobID / StorageURI /
// PublicURL and have artifacts written to any path in the bucket. Minting and placement are
// assignJob's job alone.
//
// The field names equal the form's names (both match domain.ReviewRequest's json tags, so only
// the reading branches).
type submitInput struct {
	RepoURL       string `json:"repo_url"`
	BaseBranch    string `json:"base_branch"`
	FeatureBranch string `json:"feature_branch"`
	Mode          string `json:"mode"`
	ModelName     string `json:"model_name"`
}

// submitResponse is the JSON response when a submission is accepted.
//
// It returns no progress. Right after acceptance it is always queued, so callers would call
// GET /jobs/{jobID} anyway.
type submitResponse struct {
	JobID     string `json:"job_id"`
	DetailURL string `json:"detail_url"`
}

// HandleJobCreate submits a review (POST /jobs).
//
// It accepts both a form and a JSON body. The processing is the same; only reading the input
// and the shape of the response differ.
func (h *Handler) HandleJobCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Read the input (form / JSON)
	req, err := readSubmitRequest(r)
	if err != nil {
		h.submitFailure(w, r, req, http.StatusBadRequest, err.Error())
		return
	}

	// 2. Validate the input
	if err := h.validateReviewRequest(req); err != nil {
		h.submitFailure(w, r, req, http.StatusBadRequest, err.Error())
		return
	}

	// 3. Mint the job ID and decide where the artifact goes and where it is viewed
	//
	// Both follow from the job ID. The worker does not rebuild them, so they cannot disagree
	// with what was decided at submission.
	if err := h.assignJob(&req); err != nil {
		slog.ErrorContext(ctx, "failed to mint the job ID", "error", err)
		h.submitFailure(w, r, req, http.StatusInternalServerError, "内部サーバーエラーが発生しました。")
		return
	}

	// 4. Record the acceptance in the history
	//
	// ★ Recorded before enqueueing. Done afterwards, since Cloud Tasks delivers within tens of
	// milliseconds, "the worker writes running -> web overwrites it with queued" can happen, and
	// a running job looks stuck as queued in the history.
	// Keeping jobs that could not be enqueued out of the history is ensured by the rollback on
	// enqueue failure below.
	h.recordQueued(ctx, req)

	// 5. Enqueue the task to Cloud Tasks
	if err := h.taskEnqueuer.Enqueue(ctx, req); err != nil {
		slog.ErrorContext(ctx, "failed to enqueue to Cloud Tasks", "error", err, "repo", req.RepoURL, "job_id", req.JobID)
		h.discardQueued(ctx, req.JobID)
		h.submitFailure(w, r, req, http.StatusServiceUnavailable,
			"現在レビューの受け付けができません。時間をおいて再度お試しください。")
		return
	}

	// 6. Return the success response
	//
	// Location is where progress is polled. The response for the screen carries the same header
	// too, so the next URL is known without reading the JSON body.
	slog.InfoContext(ctx, "enqueued the review task", "repo", req.RepoURL, "job_id", req.JobID)
	w.Header().Set("Location", req.PublicURL)
	if respond.WantsJSON(w, r) {
		respond.JSON(w, r, http.StatusAccepted, submitResponse{JobID: req.JobID, DetailURL: req.PublicURL})
		return
	}
	h.renderForm(w, r, http.StatusAccepted, reviewFormPageData(req, ReviewFormPageData{
		Message:   "✅ レビュータスクを受け付けました。完了後、以下のリンクから結果を確認できます。",
		ResultURL: req.PublicURL,
	}))
}

// readSubmitRequest reads the submission from a form or a JSON body.
//
// Its error text is returned to the caller (form or JSON), so it stays Japanese.
func readSubmitRequest(r *http.Request) (domain.ReviewRequest, error) {
	if isJSONBody(r) {
		var in submitInput
		dec := json.NewDecoder(io.LimitReader(r.Body, maxSubmitBody))
		// Unknown fields are an error. Dropped silently, a caller that sent a field not accepted,
		// such as storage_uri, would believe it took effect.
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return domain.ReviewRequest{}, fmt.Errorf("リクエスト本文を解釈できません: %w", err)
		}
		return domain.ReviewRequest{
			RepoURL:       strings.TrimSpace(in.RepoURL),
			BaseBranch:    strings.TrimSpace(in.BaseBranch),
			FeatureBranch: strings.TrimSpace(in.FeatureBranch),
			Mode:          in.Mode,
			ModelName:     in.ModelName,
		}, nil
	}

	if err := r.ParseForm(); err != nil {
		return domain.ReviewRequest{}, errors.New("リクエストのパースに失敗しました。")
	}
	return domain.ReviewRequest{
		RepoURL:       strings.TrimSpace(r.PostFormValue("repo_url")),
		BaseBranch:    strings.TrimSpace(r.PostFormValue("base_branch")),
		FeatureBranch: strings.TrimSpace(r.PostFormValue("feature_branch")),
		Mode:          r.PostFormValue("mode"),
		ModelName:     r.PostFormValue("model_name"),
	}, nil
}

// isJSONBody reports from Content-Type whether the body is JSON.
func isJSONBody(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

// submitFailure returns a failed submission to the caller.
//
// The screen re-renders the form with its input (so nothing has to be retyped). JSON gets only
// the reason.
func (h *Handler) submitFailure(
	w http.ResponseWriter, r *http.Request, req domain.ReviewRequest, status int, message string,
) {
	if respond.WantsJSON(w, r) {
		respond.ErrorJSON(w, r, status, message)
		return
	}
	h.renderForm(w, r, status, reviewFormPageData(req, ReviewFormPageData{Error: message}))
}

// assignJob mints the job ID and decides where results are stored and viewed.
func (h *Handler) assignJob(req *domain.ReviewRequest) error {
	jobID, err := h.newJobID()
	if err != nil {
		return err
	}

	detailURL := h.jobURL(jobID)
	if detailURL == "" {
		return errors.New("failed to build the detail URL")
	}

	req.JobID = jobID
	req.StorageURI = h.layout.ReportURI(jobID)
	req.PublicURL = detailURL
	return nil
}

// recordQueued records in the progress that the job was accepted.
func (h *Handler) recordQueued(ctx context.Context, req domain.ReviewRequest) {
	status := domain.NewQueuedStatus(req)
	status.QueuedAt = h.now()

	if err := h.statusStore.Save(ctx, req.JobID, status); err != nil {
		slog.WarnContext(ctx, "failed to record the acceptance", "job_id", req.JobID, "error", err)
		return
	}

	// Drop the cached job ID list so the job appears in the list right after submission.
	h.history.Invalidate()
}

// discardQueued rolls back the acceptance record of a submission that failed to enqueue.
//
// Even if the rollback fails, the user has already been told the submission failed, so it only
// logs and continues (a leftover record just goes stale as queued and never runs).
func (h *Handler) discardQueued(ctx context.Context, jobID string) {
	if err := h.history.Delete(ctx, jobID); err != nil {
		slog.WarnContext(ctx, "could not roll back the acceptance record of a failed submission", "job_id", jobID, "error", err)
		return
	}
	h.history.Invalidate()
}

// newJobID mints a job ID.
func newJobID() (string, error) {
	return jobid.New("")
}
