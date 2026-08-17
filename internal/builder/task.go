package builder

import (
	"context"
	"fmt"
	"net/url"

	"github.com/shouni/gcp-kit/tasks"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// buildTaskEnqueuer は、Cloud Tasks エンキューアを初期化します。
//
// 投入先は自分自身ではなく worker サービス（WORKER_URL）です。both で動かすローカル
// 開発では WORKER_URL 未設定 → SERVICE_URL に落ちるため、自己投入になります。
func buildTaskEnqueuer(ctx context.Context, cfg *config.Config) (*tasks.Enqueuer[domain.ReviewRequest], error) {
	workerURL, err := url.JoinPath(cfg.WorkerURL, "/tasks/execute_review")
	if err != nil {
		return nil, fmt.Errorf("failed to build worker URL: %w", err)
	}

	taskCfg := tasks.Config{
		ProjectID:           cfg.ProjectID,
		LocationID:          cfg.LocationID,
		QueueID:             cfg.QueueID,
		WorkerURL:           workerURL,
		ServiceAccountEmail: cfg.ServiceAccountEmail,
		Audience:            cfg.TaskAudienceURL,
		// 未指定だと既定 10 分に落ちる。2026-08-10 まで指定が無かった。
		DispatchDeadline: config.TaskDispatchDeadline,
	}
	return tasks.NewEnqueuer[domain.ReviewRequest](ctx, taskCfg)
}
