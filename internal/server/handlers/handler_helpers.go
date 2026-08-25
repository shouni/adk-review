package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/domain"
)

const (
	repoURLPattern       = `^git@github\.com:[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git$`
	branchPattern        = `^[a-zA-Z0-9_.-]+(/[a-zA-Z0-9_.-]+)*$`
	csrfTokenField       = "csrf_token"
	defaultReviewMode    = "code"
	defaultBaseBranch    = "main"
	defaultFeatureBranch = "develop"
)

var (
	// gitURLRegexp は、GitリポジトリURLの形式をチェックします。
	gitURLRegexp = regexp.MustCompile(repoURLPattern)
	// gitBranchRegexp は、ブランチ名の命名規則をチェックします。
	gitBranchRegexp = regexp.MustCompile(branchPattern)
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

// validateReviewRequest は、投入内容をまとめて検証します。
func (h *Handler) validateReviewRequest(req domain.ReviewRequest) error {
	if req.RepoURL == "" || req.BaseBranch == "" || req.FeatureBranch == "" || req.Mode == "" || req.ModelName == "" {
		return errors.New("すべてのフィールドを入力してください。")
	}

	if !assets.IsValidMode(req.Mode) {
		return fmt.Errorf("不正なレビューモードです: %s", req.Mode)
	}

	if !slices.Contains(h.configuredModels(), req.ModelName) {
		return fmt.Errorf("不正なGeminiモデルです: %s", req.ModelName)
	}

	if !gitURLRegexp.MatchString(req.RepoURL) {
		return errors.New("リポジトリURLの形式が不正です。git@github.com:owner/repo.git の形式のみ使用できます。")
	}

	if err := validateBranchName(req.BaseBranch); err != nil {
		return fmt.Errorf("ベースブランチ名: %w", err)
	}

	if err := validateBranchName(req.FeatureBranch); err != nil {
		return fmt.Errorf("フィーチャーブランチ名: %w", err)
	}

	return nil
}

// validateBranchName は、Git のブランチ名として正当かどうかを返します。
func validateBranchName(branchName string) error {
	if !gitBranchRegexp.MatchString(branchName) {
		return errors.New("形式が不正です。英数字、ハイフン、ドット、スラッシュのみ使用可能です。")
	}
	if strings.Contains(branchName, "..") || strings.Contains(branchName, "//") {
		return errors.New("'..' または '//' は使用できません。")
	}
	if strings.HasSuffix(branchName, "/") || strings.HasSuffix(branchName, ".") {
		return errors.New("末尾に '/' や '.' は使用できません。")
	}
	return nil
}
