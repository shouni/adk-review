package builder

import (
	"context"
	"fmt"

	reviewpipeline "github.com/shouni/go-review-kit/pipeline"

	"github.com/shouni/adk-review/internal/adapters"
	"github.com/shouni/adk-review/internal/app"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/adk-review/internal/pipeline"
)

// buildPipeline returns a runnable domain.Pipeline.
func buildPipeline(_ context.Context, appCtx *app.Container) (domain.Pipeline, error) {
	sources, err := adapters.NewDiffSourceFactory(appCtx.Config.Git.SSHKeyPath)
	if err != nil {
		return nil, err
	}

	publisher, err := adapters.NewReportPublisher(appCtx.Store)
	if err != nil {
		return nil, fmt.Errorf("failed to build the ReportPublisher: %w", err)
	}

	core, err := reviewpipeline.New(reviewpipeline.Deps{
		Sources:           sources,
		Prompts:           appCtx.PromptGen,
		WorkspaceReviewer: adapters.NewAgentReviewer(appCtx.Config),
		Publisher:         publisher,
		Notifier:          appCtx.Notifier,
	},
		// The deadline is left to the library. Put on the context by hand, it would sit outside
		// the detachment done for publishing and notifying, and the failure notice would be lost
		// together with the cut-off.
		reviewpipeline.WithRunTimeout(appCtx.Config.Pipeline.Timeout),
		// An oversized diff fails before it is sent to the AI. Sent, it would fail anyway with
		// truncated output or a missed deadline, but either is known only after the model call.
		reviewpipeline.WithMaxDiffBytes(appCtx.Config.Pipeline.MaxDiffBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build the pipeline: %w", err)
	}

	return pipeline.NewRunner(core, appCtx.StatusStore), nil
}
