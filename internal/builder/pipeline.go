package builder

import (
	"context"
	"fmt"

	"github.com/shouni/go-review-kit/pipeline"

	"github.com/shouni/adk-review/assets"
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
	sources, err := adapters.NewDiffSourceFactory(appCtx.Config.Git.SSHKeyPath)
	if err != nil {
		return nil, err
	}

	publisher, err := adapters.NewReportPublisher(appCtx.RemoteIO.Writer)
	if err != nil {
		return nil, fmt.Errorf("ReportPublisher の構築に失敗しました: %w", err)
	}

	client, err := adapters.NewGeminiClient(ctx, appCtx.Config)
	if err != nil {
		return nil, err
	}
	singleReviewer, err := adapters.NewGeminiReviewer(client)
	if err != nil {
		return nil, err
	}
	agentReviewer := adapters.NewAgentReviewer(appCtx.Config)

	// Prompts は shared に置きません。**プロンプトはエンジンごとに別物です。**
	// 単発のレビュアーに「ツールで確認しろ」「evidence を挙げろ」と書いたプロンプトを
	// 渡すと、確認しようのない事柄について根拠を捏造させることになります。
	shared := pipeline.Deps{
		Sources:   sources,
		Publisher: publisher,
		Notifier:  appCtx.Notifier,
	}

	// 締切はライブラリに持たせます。自前で context に被せると、公開・通知のための
	// 切り離しより外側に掛かり、打ち切りと同時に失敗通知まで落ちます。
	runTimeout := pipeline.WithRunTimeout(appCtx.Config.Pipeline.Timeout)

	single, err := newCore(shared, runTimeout, func(d *pipeline.Deps) {
		d.Reviewer = singleReviewer
		d.Prompts = appCtx.PromptGen.For(assets.EngineSingle)
	})
	if err != nil {
		return nil, err
	}
	agent, err := newCore(shared, runTimeout, func(d *pipeline.Deps) {
		d.WorkspaceReviewer = agentReviewer
		d.Prompts = appCtx.PromptGen.For(assets.EngineAgent)
	})
	if err != nil {
		return nil, err
	}

	router := adapters.NewEngineRouter(single, agent)
	return adapters.NewReviewPipeline(router, appCtx.StatusStore), nil
}

// newCore は、共有依存にレビュアーを 1 つ差して pipeline を組み立てます。
func newCore(shared pipeline.Deps, opt pipeline.Option, setReviewer func(*pipeline.Deps)) (*pipeline.Pipeline, error) {
	deps := shared
	setReviewer(&deps)

	core, err := pipeline.New(deps, opt)
	if err != nil {
		return nil, fmt.Errorf("パイプラインの構築に失敗しました: %w", err)
	}
	return core, nil
}
