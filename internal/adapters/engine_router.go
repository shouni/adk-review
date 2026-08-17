package adapters

import (
	"context"

	"github.com/shouni/go-review-kit/pipeline"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/assets"
)

// EngineRouter は、レビューモードのメタデータ（<!-- engine: ... -->）に従って
// 単発パイプラインとエージェントパイプラインを使い分ける coreRunner です。
//
// go-review-kit の Pipeline はレビュアーを 1 種類しか持てないため、使い分けは
// リクエスト単位でこの層が行います。フォームにエンジンの選択肢は出しません。
// どのモードをどう実行するかはプロンプト資産側の宣言であり、依頼者が毎回選ぶ
// 性質のものではないためです。
type EngineRouter struct {
	single *pipeline.Pipeline
	agent  *pipeline.Pipeline
}

var _ coreRunner = (*EngineRouter)(nil)

// NewEngineRouter は EngineRouter を構築します。
func NewEngineRouter(single, agent *pipeline.Pipeline) *EngineRouter {
	return &EngineRouter{single: single, agent: agent}
}

// Run は、モードに対応するエンジンのパイプラインへ委譲します。
func (r *EngineRouter) Run(ctx context.Context, req review.Request) (review.Result, *review.Report, error) {
	engine, err := assets.EngineFor(req.Mode)
	if err != nil {
		// モードは受付時に検証済みなので、ここへ来るのはプロンプト資産の破損だけです。
		// 通常の検証エラーと同じ形（StepValidate 付き）で失敗させ、記録に工程名を残します。
		err = review.WrapStep(review.StepValidate, err)
		return review.Failed(req, 0, err), nil, err
	}

	if engine == assets.EngineAgent {
		return r.agent.Run(ctx, req)
	}
	return r.single.Run(ctx, req)
}
