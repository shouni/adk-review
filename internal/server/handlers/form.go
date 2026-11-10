package handlers

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"slices"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-utils/jobid"
)

// ReviewFormPageData is the data passed to the form template.
type ReviewFormPageData struct {
	Message string
	// Notice is a note that is neither success nor failure (such as the form being filled for a rerun).
	// It is separate from Message because that one is fixed to the green submitted box.
	Notice         string
	Error          string
	ResultURL      string
	RepoURL        string
	BaseBranch     string
	FeatureBranch  string
	ReviewMode     string
	ModelName      string
	ReviewModes    []ReviewModeOption
	Models         []ModelOption
	CSRFToken      string
	CSRFTokenField string
	RepoURLPattern string
	BranchPattern  string
}

// ReviewModeOption is a review mode choice shown on the form.
type ReviewModeOption struct {
	Value       string
	Description string
	Selected    bool
}

// ModelOption is a Gemini model choice shown on the form.
type ModelOption struct {
	Value    string
	Selected bool
}

// HandleForm shows the form for a GET request.
//
// With ?from={jobID} the form is filled with that review's request. A failed review is not
// retried, since review-queue has max_attempts = 1, and redoing it used to mean typing the
// request in again.
func (h *Handler) HandleForm(w http.ResponseWriter, r *http.Request) {
	data := defaultReviewFormPageData()
	if from := r.URL.Query().Get("from"); from != "" {
		data = h.rerunFormData(r.Context(), from, data)
	}
	h.renderForm(w, r, http.StatusOK, data)
}

// rerunFormData fills the form's initial values with a past review's request.
//
// Failing to read it is not an error either; the default form is shown with a note. A rerun is a
// convenience that saves typing, and it not working must not block a new request.
//
// Fields empty in the record keep their defaults, so opening a job stored in a format from before
// requests were recorded does not blank the defaults too.
func (h *Handler) rerunFormData(ctx context.Context, jobID string, data ReviewFormPageData) ReviewFormPageData {
	const unavailable = "元のレビューを読み込めなかったため、既定値で表示しています。"

	safeJobID, err := jobid.Sanitize(jobID)
	if err != nil {
		slog.WarnContext(ctx, "invalid source job ID for a rerun", "job_id", jobID, "error", err)
		data.Notice = unavailable
		return data
	}

	status, err := h.statusStore.Get(ctx, safeJobID)
	if err != nil {
		slog.WarnContext(ctx, "could not load the review to rerun", "job_id", safeJobID, "error", err)
		data.Notice = unavailable
		return data
	}

	data.RepoURL = cmp.Or(status.RepoURL, data.RepoURL)
	data.BaseBranch = cmp.Or(status.BaseBranch, data.BaseBranch)
	data.FeatureBranch = cmp.Or(status.FeatureBranch, data.FeatureBranch)
	data.ReviewMode = cmp.Or(status.Mode, data.ReviewMode)
	data.ModelName = cmp.Or(status.ModelName, data.ModelName)
	data.Notice = "過去の依頼内容を引き継いでいます。必要な箇所を直して依頼してください。"
	return data
}

const (
	repoURLPattern       = `^git@github\.com:[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git$`
	branchPattern        = `^[a-zA-Z0-9_.-]+(/[a-zA-Z0-9_.-]+)*$`
	csrfTokenField       = "csrf_token"
	defaultReviewMode    = "code"
	defaultBaseBranch    = "main"
	defaultFeatureBranch = "develop"
)

// renderForm is the helper that handles rendering the review form in one place.
func (h *Handler) renderForm(w http.ResponseWriter, r *http.Request, status int, data ReviewFormPageData) {
	data.RepoURLPattern = repoURLPattern
	data.BranchPattern = branchPattern
	data.CSRFTokenField = csrfTokenField
	if len(data.ReviewModes) == 0 {
		data.ReviewModes = reviewModeOptions(r.Context(), data.ReviewMode)
	}
	if len(data.Models) == 0 {
		data.Models = h.modelOptions(data.ModelName)
	}
	if data.CSRFToken == "" {
		data.CSRFToken = CSRFTokenFromContext(r.Context())
	}

	h.render(w, r, status, reviewFormTemplate, data)
}

func reviewModeOptions(ctx context.Context, selectedMode string) []ReviewModeOption {
	modes, err := assets.AvailableModes()
	if err != nil {
		slog.ErrorContext(ctx, "failed to load the review modes", "error", err)
		if selectedMode == "" {
			selectedMode = defaultReviewMode
		}
		return []ReviewModeOption{{
			Value:       selectedMode,
			Description: selectedMode,
			Selected:    true,
		}}
	}
	if selectedMode == "" {
		selectedMode = defaultReviewMode
	}

	options := make([]ReviewModeOption, 0, len(modes))
	hasSelected := false
	for _, mode := range modes {
		selected := mode.Key == selectedMode
		if selected {
			hasSelected = true
		}
		options = append(options, ReviewModeOption{
			Value:       mode.Key,
			Description: mode.DisplayName(),
			Selected:    selected,
		})
	}
	if len(options) > 0 && !hasSelected {
		options[0].Selected = true
	}
	return options
}

func (h *Handler) modelOptions(selectedModel string) []ModelOption {
	models := h.configuredModels()
	if len(models) == 0 {
		return nil
	}
	if !slices.Contains(models, selectedModel) {
		selectedModel = models[0]
	}

	options := make([]ModelOption, 0, len(models))
	for _, model := range models {
		options = append(options, ModelOption{
			Value:    model,
			Selected: model == selectedModel,
		})
	}
	return options
}

// configuredModels returns the models configured in GEMINI_MODELS.
func (h *Handler) configuredModels() []string {
	if h.cfg == nil {
		return nil
	}
	return h.cfg.AI.GeminiModels
}

func defaultReviewFormPageData() ReviewFormPageData {
	return ReviewFormPageData{
		BaseBranch:    defaultBaseBranch,
		FeatureBranch: defaultFeatureBranch,
		ReviewMode:    defaultReviewMode,
	}
}

func reviewFormPageData(req domain.ReviewRequest, data ReviewFormPageData) ReviewFormPageData {
	data.RepoURL = req.RepoURL
	data.BaseBranch = req.BaseBranch
	data.FeatureBranch = req.FeatureBranch
	data.ReviewMode = req.Mode
	data.ModelName = req.ModelName
	return data
}
