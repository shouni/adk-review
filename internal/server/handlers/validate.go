package handlers

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/domain"
)

var (
	// gitURLRegexp は、GitリポジトリURLの形式をチェックします。
	gitURLRegexp = regexp.MustCompile(repoURLPattern)
	// gitBranchRegexp は、ブランチ名の命名規則をチェックします。
	gitBranchRegexp = regexp.MustCompile(branchPattern)
)

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
