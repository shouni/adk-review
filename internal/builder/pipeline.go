package builder

import (
	"context"
	"fmt"

	"github.com/shouni/go-review-kit/pipeline"

	"github.com/shouni/adk-review/internal/adapters"
	"github.com/shouni/adk-review/internal/app"
	"github.com/shouni/adk-review/internal/domain"
)

// buildPipeline は、実行可能な domain.Pipeline を返します。
func buildPipeline(_ context.Context, appCtx *app.Container) (domain.Pipeline, error) {
	sources, err := adapters.NewDiffSourceFactory(appCtx.Config.Git.SSHKeyPath)
	if err != nil {
		return nil, err
	}

	publisher, err := adapters.NewReportPublisher(appCtx.RemoteIO.Writer)
	if err != nil {
		return nil, fmt.Errorf("ReportPublisher の構築に失敗しました: %w", err)
	}

	core, err := pipeline.New(pipeline.Deps{
		Sources:           sources,
		Prompts:           appCtx.PromptGen,
		WorkspaceReviewer: adapters.NewAgentReviewer(appCtx.Config),
		Publisher:         publisher,
		Notifier:          appCtx.Notifier,
	},
		// 締切はライブラリに持たせます。自前で context に被せると、公開・通知のための
		// 切り離しより外側に掛かり、打ち切りと同時に失敗通知まで落ちます。
		pipeline.WithRunTimeout(appCtx.Config.Pipeline.Timeout),
		// 大きすぎる差分は AI へ送る前に落とします。送っても出力の途中切れか締切超過で
		// 失敗しますが、**どちらもモデルを呼び終えたあとにしか分かりません。**
		pipeline.WithMaxDiffBytes(appCtx.Config.Pipeline.MaxDiffBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("パイプラインの構築に失敗しました: %w", err)
	}

	return adapters.NewReviewPipeline(core, appCtx.StatusStore), nil
}
