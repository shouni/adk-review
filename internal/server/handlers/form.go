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

// ReviewFormPageData はフォームテンプレートに渡すデータ構造です。
type ReviewFormPageData struct {
	Message string
	// Notice は、成功でも失敗でもない補足です（再実行でフォームを埋めたこと等）。
	// Message と分けているのは、そちらが投入完了の緑色の枠に固定されているためです。
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

// ReviewModeOption はフォームに表示するレビューモードの選択肢です。
type ReviewModeOption struct {
	Value       string
	Description string
	Selected    bool
}

// ModelOption はフォームに表示するGeminiモデルの選択肢です。
type ModelOption struct {
	Value    string
	Selected bool
}

// HandleForm は GET リクエストに対してフォームを表示します。
//
// ?from={jobID} を付けると、そのレビューの依頼内容でフォームを埋めます。失敗した
// レビューは review-queue が max_attempts = 1 なので再試行されず、やり直すには
// 依頼内容を打ち直すしかありませんでした。
func (h *Handler) HandleForm(w http.ResponseWriter, r *http.Request) {
	data := defaultReviewFormPageData()
	if from := r.URL.Query().Get("from"); from != "" {
		data = h.rerunFormData(r.Context(), from, data)
	}
	h.renderForm(w, r, http.StatusOK, data)
}

// rerunFormData は、過去のレビューの依頼内容でフォームの初期値を埋めます。
//
// 読めなかった場合もエラーにはせず、既定値のフォームを断り書き付きで出します。
// 再実行は入力を省くための便宜で、それが効かないことは新しい依頼を妨げません。
//
// 記録が空の項目には既定値を残します。依頼内容を記録する前の形式で保存された
// ジョブを開いたときに、既定値まで空へ倒さないためです。
func (h *Handler) rerunFormData(ctx context.Context, jobID string, data ReviewFormPageData) ReviewFormPageData {
	const unavailable = "元のレビューを読み込めなかったため、既定値で表示しています。"

	safeJobID, err := jobid.Sanitize(jobID)
	if err != nil {
		slog.WarnContext(ctx, "再実行の元ジョブIDが不正です", "job_id", jobID, "error", err)
		data.Notice = unavailable
		return data
	}

	status, err := h.statusStore.Get(ctx, safeJobID)
	if err != nil {
		slog.WarnContext(ctx, "再実行の元になるレビューを読み込めませんでした", "job_id", safeJobID, "error", err)
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

// renderForm はレビューフォームの表示を一括管理するヘルパーメソッドです。
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
		slog.ErrorContext(ctx, "レビューモード一覧の読み込みに失敗しました", "error", err)
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

// configuredModels は GEMINI_MODELS で設定されたモデル一覧を返します。
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
