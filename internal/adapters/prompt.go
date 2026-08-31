package adapters

import (
	"fmt"

	"github.com/shouni/go-prompt-kit/prompts"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/assets"
)

// reviewData はレビュープロンプトのテンプレートに渡すデータです。
//
// 共有断片（指摘の方針・出力フォーマット）は文字列として渡さず、テンプレート集の
// partial として本文から参照します。
type reviewData struct {
	DiffContent string
}

// promptBuilder は、フォーマット済みのプロンプトを作成するためのインターフェース
type promptBuilder interface {
	Build(mode string, data any) (string, error)
}

// PromptAdapter は、モードに応じたレビュープロンプトを組み立てます。
type PromptAdapter struct {
	builder promptBuilder
}

var _ review.PromptGenerator = (*PromptAdapter)(nil)

// NewPromptAdapter は動的に読み込んだテンプレートを使用して Builder を構築します。
func NewPromptAdapter() (*PromptAdapter, error) {
	templates, err := assets.PromptTemplates()
	if err != nil {
		return nil, fmt.Errorf("レビューテンプレートの読み込みに失敗: %w", err)
	}

	// WithTrimPartials を付けるのは、断片を箇条書きの途中へ差し込むためです。
	// ファイル末尾の改行が残ると、差し込んだ位置に空行が入り、後ろに続くモード固有の
	// 方針が別のリストとして分かれて見えます。
	builder, err := prompts.NewBuilder(templates, prompts.WithTrimPartials())
	if err != nil {
		return nil, fmt.Errorf("レビュービルダーの構築に失敗: %w", err)
	}

	return &PromptAdapter{builder: builder}, nil
}

// Generate はレビューのプロンプトを生成します。
func (pa *PromptAdapter) Generate(mode, diff string) (string, error) {
	prompt, err := pa.builder.Build(mode, reviewData{DiffContent: diff})
	if err != nil {
		return "", fmt.Errorf("レビューテンプレートの実行に失敗: %w", err)
	}
	return prompt, nil
}
