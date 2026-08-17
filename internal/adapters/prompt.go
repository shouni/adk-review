package adapters

import (
	"fmt"

	"github.com/shouni/go-prompt-kit/prompts"

	"github.com/shouni/adk-review/assets"
)

// reviewData はレビュープロンプトのテンプレートに渡すデータ構造です。
// FindingsFormat/VerdictFormat は、全モードのプロンプトで共有される、AIの構造化出力
// (findings配列・verdictオブジェクト)のフォーマット説明です。
type reviewData struct {
	DiffContent    string
	FindingsFormat string
	VerdictFormat  string
}

// promptBuilder は、フォーマット済みのプロンプトを作成するためのインターフェース
type promptBuilder interface {
	Build(mode string, data any) (string, error)
}

// PromptAdapter は、さまざまなモードとデータに基づいてプロンプトを生成する役割を担います。
type PromptAdapter struct {
	reviewBuilder  promptBuilder
	findingsFormat string
	verdictFormat  string
}

// NewPromptAdapter は動的に読み込んだテンプレートを使用して Builder を構築します。
func NewPromptAdapter() (*PromptAdapter, error) {
	reviewTemplates, err := assets.LoadPrompts()
	if err != nil {
		return nil, fmt.Errorf("レビューテンプレートの読み込みに失敗: %w", err)
	}

	review, err := prompts.NewBuilder(reviewTemplates)
	if err != nil {
		return nil, fmt.Errorf("レビュービルダーの構築に失敗: %w", err)
	}

	findingsFormat, err := assets.LoadFindingsFormat()
	if err != nil {
		return nil, fmt.Errorf("指摘フォーマットの読み込みに失敗: %w", err)
	}

	verdictFormat, err := assets.LoadVerdictFormat()
	if err != nil {
		return nil, fmt.Errorf("判定フォーマットの読み込みに失敗: %w", err)
	}

	return &PromptAdapter{
		reviewBuilder:  review,
		findingsFormat: findingsFormat,
		verdictFormat:  verdictFormat,
	}, nil
}

// Generate はコードレビューのプロンプトを生成します。
func (pa *PromptAdapter) Generate(mode, codeDiff string) (string, error) {
	data := reviewData{
		DiffContent:    codeDiff,
		FindingsFormat: pa.findingsFormat,
		VerdictFormat:  pa.verdictFormat,
	}
	prompt, err := pa.reviewBuilder.Build(mode, data)
	if err != nil {
		return "", fmt.Errorf("レビューテンプレートの実行に失敗: %w", err)
	}
	return prompt, nil
}
