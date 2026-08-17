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
//
// go-review-kit の Pipeline はレビュアーを 1 種類しか持てないため、単発（差分のみ）と
// エージェント（作業ディレクトリを調査）の 2 本をアダプター共有で組み、モードごとの
// 使い分けは adapters.EngineRouter が行います。
func buildPipeline(ctx context.Context, appCtx *app.Container) (domain.Pipeline, error) {
	sources, err := adapters.NewDiffSourceFactory(appCtx.Config.SSHKeyPath)
	if err != nil {
		return nil, err
	}

	publisher, err := adapters.NewReportPublisher(appCtx.RemoteIO.Writer, appCtx.Layout)
	if err != nil {
		return nil, fmt.Errorf("ReportPublisher の構築に失敗しました: %w", err)
	}

	client, err := adapters.NewGeminiClient(ctx, appCtx.Config)
	if err != nil {
		return nil, err
	}
	singleReviewer, err := adapters.NewReviewer(client)
	if err != nil {
		return nil, err
	}
	agentReviewer := adapters.NewAgentReviewer(appCtx.Config)

	shared := pipeline.Deps{
		Sources:   sources,
		Prompts:   appCtx.PromptGen,
		Publisher: publisher,
		Notifier:  appCtx.Notifier,
	}

	single, err := newCore(shared, func(d *pipeline.Deps) { d.Reviewer = singleReviewer })
	if err != nil {
		return nil, err
	}
	agent, err := newCore(shared, func(d *pipeline.Deps) { d.WorkspaceReviewer = agentReviewer })
	if err != nil {
		return nil, err
	}

	router := adapters.NewEngineRouter(single, agent)
	return adapters.NewReviewPipeline(router, appCtx.StatusStore, appCtx.Config.PipelineTimeout), nil
}

// newCore は、共有依存にレビュアーを 1 つ差して pipeline を組み立てます。
func newCore(shared pipeline.Deps, setReviewer func(*pipeline.Deps)) (*pipeline.Pipeline, error) {
	deps := shared
	setReviewer(&deps)

	core, err := pipeline.New(deps)
	if err != nil {
		return nil, fmt.Errorf("パイプラインの構築に失敗しました: %w", err)
	}
	return core, nil
}
