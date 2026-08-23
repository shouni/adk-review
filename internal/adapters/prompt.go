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
// partial として本文から参照します。断片の側でも Agent を見た条件分岐を書けるように
// するためです。
type reviewData struct {
	DiffContent string
	// Agent は、作業ディレクトリをツールで調べるエンジンで実行されるかどうかです。
	//
	// **単発エンジンに「ツールで確認しろ」「evidence を挙げろ」と指示すると、
	// 確認しようのない事柄について根拠を捏造させることになります。** 単発の
	// 出力スキーマには evidence がそもそも無く、書かせても捨てられます。
	Agent bool
}

// promptBuilder は、フォーマット済みのプロンプトを作成するためのインターフェース
type promptBuilder interface {
	Build(mode string, data any) (string, error)
}

// PromptAdapter は、モードとエンジンに応じたプロンプト生成器を配ります。
type PromptAdapter struct {
	builder promptBuilder
}

// NewPromptAdapter は動的に読み込んだテンプレートを使用して Builder を構築します。
func NewPromptAdapter() (*PromptAdapter, error) {
	templates, err := assets.PromptTemplates()
	if err != nil {
		return nil, fmt.Errorf("レビューテンプレートの読み込みに失敗: %w", err)
	}

	// WithTrimPartials を付けるのは、断片を箇条書きの**途中**へ差し込むためです。
	// ファイル末尾の改行が残ると、差し込んだ位置に空行が入り、後ろに続くモード固有の
	// 方針が別のリストとして分かれて見えます。
	builder, err := prompts.NewBuilder(templates, prompts.WithTrimPartials())
	if err != nil {
		return nil, fmt.Errorf("レビュービルダーの構築に失敗: %w", err)
	}

	return &PromptAdapter{builder: builder}, nil
}

// For は、指定したエンジンで実行することを前提にしたプロンプト生成器を返します。
//
// エンジンはパイプライン単位で決まるため、依頼ごとに切り替える必要はありません。
// review.PromptGenerator の Generate はモードと差分しか受け取らないので、
// **エンジンの違いは生成器そのものを分けて表します。**
func (pa *PromptAdapter) For(engine assets.Engine) review.PromptGenerator {
	return engineGenerator{builder: pa.builder, agent: engine == assets.EngineAgent}
}

// engineGenerator は、1 つのエンジン向けに固定したプロンプト生成器です。
type engineGenerator struct {
	builder promptBuilder
	agent   bool
}

var _ review.PromptGenerator = engineGenerator{}

// Generate はレビューのプロンプトを生成します。
func (g engineGenerator) Generate(mode, diff string) (string, error) {
	prompt, err := g.builder.Build(mode, reviewData{DiffContent: diff, Agent: g.agent})
	if err != nil {
		return "", fmt.Errorf("レビューテンプレートの実行に失敗: %w", err)
	}
	return prompt, nil
}
