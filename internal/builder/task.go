package builder

import (
	"context"
	"fmt"

	"github.com/shouni/gcp-kit/tasks"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// buildTaskEnqueuer は、Cloud Tasks エンキューアを初期化します。
//
// 投入先は自分自身ではなく worker サービス（WORKER_URL）です。both で動かすローカル
// 開発では WORKER_URL 未設定 → SERVICE_URL に落ちるため、自己投入になります。
func buildTaskEnqueuer(ctx context.Context, cfg *config.Config) (*tasks.Enqueuer[domain.ReviewRequest], error) {
	taskURL, err := domain.WorkerTaskURL(cfg.Tasks.WorkerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to build worker URL: %w", err)
	}

	taskCfg := tasks.Config{
		ProjectID:           cfg.GCP.ProjectID,
		LocationID:          cfg.GCP.LocationID,
		QueueID:             cfg.Tasks.QueueID,
		WorkerURL:           taskURL,
		ServiceAccountEmail: cfg.Tasks.CallerServiceAccountEmail,
		Audience:            cfg.Tasks.TaskAudienceURL,
		// 未指定だと Cloud Tasks 既定の 10 分に落ちる。2026-08-10 まで指定が無かった。
		DispatchDeadline: cfg.Tasks.DispatchDeadline,
	}
	return tasks.NewEnqueuer[domain.ReviewRequest](ctx, taskCfg)
}
