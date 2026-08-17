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
	if len(data.GeminiModels) == 0 {
		data.GeminiModels = h.geminiModelOptions(data.GeminiModel)
	}
	if len(data.Engines) == 0 {
		data.Engines = engineOptions(data.Engine)
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

// engineOptions は、レビューエンジンの選択肢を返します。
//
// 既定（空値）を先頭に置くのは、モードが front matter で宣言した深さが通常は妥当で、
// 上書きは例外だからです。選択肢に出しておくと、急ぎの確認や比較のときだけ倒せます。
func engineOptions(selected string) []EngineOption {
	options := []EngineOption{
		{Value: "", Label: "モードの既定", Description: "モードごとに適した方法で実行します"},
		{Value: string(assets.EngineAgent), Label: "エージェント", Description: "差分の外も調べます。深いが時間がかかります"},
		{Value: string(assets.EngineSingle), Label: "単発", Description: "差分だけを見ます。速いが文脈は見ません"},
	}
	for i := range options {
		options[i].Selected = options[i].Value == selected
	}
	return options
}

func (h *Handler) geminiModelOptions(selectedModel string) []GeminiModelOption {
	models := h.configuredGeminiModels()
	if len(models) == 0 {
		return nil
	}
	if !slices.Contains(models, selectedModel) {
		selectedModel = models[0]
	}

	options := make([]GeminiModelOption, 0, len(models))
	for _, model := range models {
		options = append(options, GeminiModelOption{
			Value:    model,
			Selected: model == selectedModel,
		})
	}
	return options
}

// configuredGeminiModels は GEMINI_MODELS で設定されたモデル一覧を返します。
func (h *Handler) configuredGeminiModels() []string {
	if h.cfg == nil {
		return nil
	}
	return h.cfg.GeminiModels
}

// validateReviewRequest は入力内容が正しいかまとめてチェックする。
func (h *Handler) validateReviewRequest(req domain.ReviewRequest) error {
	if req.RepoURL == "" || req.BaseBranch == "" || req.FeatureBranch == "" || req.Mode == "" || req.ModelName == "" {
		return errors.New("すべてのフィールドを入力してください。")
	}

	// レビューモードの動的バリデーション
	if !assets.IsValidMode(req.Mode) {
		return fmt.Errorf("不正なレビューモードです: %s", req.Mode)
	}

	// エンジンの上書き指定は、実行時と同じ関数で受付時にも確かめます。
	// 不正な値をタスクに載せると、ワーカーまで運ばれてから失敗します。
	if _, err := assets.ResolveEngine(req.Mode, req.Engine); err != nil {
		return err
	}

	if !slices.Contains(h.configuredGeminiModels(), req.ModelName) {
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

// validateBranchName は Git のブランチ名として正当かどうかを判定する。
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
