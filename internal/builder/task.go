package builder

import (
	"context"

	"github.com/shouni/gcp-kit/tasks"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// buildTaskEnqueuer initialises the Cloud Tasks enqueuer.
//
// Tasks go to the worker service (WORKER_URL), not to this process. In local development under
// both, an unset WORKER_URL falls back to SERVICE_URL, so it enqueues to itself.
func buildTaskEnqueuer(ctx context.Context, cfg *config.Config) (*tasks.Enqueuer[domain.ReviewRequest], error) {

	taskCfg := tasks.Config{
		ProjectID:           cfg.GCP.ProjectID,
		LocationID:          cfg.GCP.LocationID,
		QueueID:             cfg.Tasks.QueueID,
		WorkerURL:           cfg.Tasks.WorkerURL,
		WorkerPath:          domain.WorkerTaskPath,
		ServiceAccountEmail: cfg.Tasks.CallerServiceAccountEmail,
		Audience:            cfg.Tasks.TaskAudienceURL,
		// Unset, it falls back to the Cloud Tasks default of 10 minutes. It was unset until 2026-08-10.
		DispatchDeadline: cfg.Tasks.DispatchDeadline,
	}
	return tasks.NewEnqueuer[domain.ReviewRequest](ctx, taskCfg)
}
