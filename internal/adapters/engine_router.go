package adapters

import (
	"context"

	"github.com/shouni/go-review-kit/pipeline"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/domain"
)

// EngineRouter は、依頼ごとに単発パイプラインとエージェントパイプラインを使い分けます。
//
// go-review-kit の Pipeline はレビュアーを 1 種類しか持てないため、使い分けはこの層で
// 行います。どちらで実行するかは「依頼の指定 → モードが front matter で宣言した既定」の
// 順に決まります。モードの宣言を既定に置くのは、モードごとに妥当な深さが違うためです
// （原稿の整合性確認は差分の外を読まないと成立しない）。上書きを許すのは、同じモードでも
// 急ぎの確認と腰を据えた確認があるためです。
type EngineRouter struct {
	single *pipeline.Pipeline
	agent  *pipeline.Pipeline
}

var _ reviewRunner = (*EngineRouter)(nil)

// NewEngineRouter は EngineRouter を構築します。
func NewEngineRouter(single, agent *pipeline.Pipeline) *EngineRouter {
	return &EngineRouter{single: single, agent: agent}
}

// Run は、解決したエンジンのパイプラインへ委譲します。
func (r *EngineRouter) Run(ctx context.Context, req domain.ReviewRequest) (review.Result, *review.Report, error) {
	engine, err := assets.ResolveEngine(req.Mode, req.Engine)
	if err != nil {
		// モードもエンジンも受付時に検証済みなので、ここへ来るのはプロンプト資産の破損だけです。
		// 通常の検証エラーと同じ形（StepValidate 付き）で失敗させ、記録に工程名を残します。
		err = review.WrapStep(review.StepValidate, err)
		return review.Failed(toReviewRequest(req), 0, err), nil, err
	}

	selected := r.single
	if engine == assets.EngineAgent {
		selected = r.agent
	}
	return selected.Run(ctx, toReviewRequest(req))
}
