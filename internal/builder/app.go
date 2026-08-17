// Package builder は、設定値から各サービスクライアント・パイプラインの
// 依存関係を組み立てるファクトリ関数を提供します。
package builder

import (
	"context"
	"fmt"
	"io"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio/gcs"

	"github.com/shouni/adk-review/internal/adapters"
	"github.com/shouni/adk-review/internal/app"
	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/adk-review/internal/repository"
)

// BuildContainer は外部サービスとの接続を確立し、依存関係を組み立てた app.Container を返します。
func BuildContainer(ctx context.Context, cfg *config.Config) (container *app.Container, err error) {
	var resources []io.Closer
	defer func() {
		if err != nil {
			for _, r := range resources {
				if r != nil {
					_ = r.Close()
				}
			}
		}
	}()

	// 1. HttpClient
	httpClient := httpkit.New(config.DefaultHTTPTimeout)

	// 2. I/O Infrastructure
	storage, err := gcs.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("GCSストレージの生成に失敗しました: %w", err)
	}
	resources = append(resources, storage)
	rio, err := buildRemoteIO(storage)
	if err != nil {
		return nil, fmt.Errorf("I/Oコンポーネントの初期化に失敗しました: %w", err)
	}

	// 3. 進行状況と履歴
	layout := domain.NewStorageLayout(cfg.GCSBucket)
	statusStore := buildStatusStore(rio, layout)
	history := repository.NewHistory(rio.Reader, rio.Writer, statusStore, layout)

	appCtx := &app.Container{
		Config:      cfg,
		RemoteIO:    rio,
		Layout:      layout,
		StatusStore: statusStore,
		History:     history,
		HTTPClient:  httpClient,
	}

	// 4. Web 面だけの依存: Task Enqueuer（レビュー依頼の投入口）
	if cfg.Role.ServesWeb() {
		enqueuer, err := buildTaskEnqueuer(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("TaskEnqueuer の構築に失敗しました: %w", err)
		}
		resources = append(resources, enqueuer)
		appCtx.TaskEnqueuer = enqueuer
	}

	// 5. Worker 面だけの依存: プロンプト・通知・パイプライン（レビューの実行系）
	if cfg.Role.ServesWorker() {
		promptGen, err := adapters.NewPromptAdapter()
		if err != nil {
			return nil, fmt.Errorf("PromptAdapter の構築に失敗しました: %w", err)
		}
		appCtx.PromptGen = promptGen

		slack, err := adapters.NewSlackAdapter(httpClient.WithoutRetry(), cfg.SlackWebhookURL)
		if err != nil {
			return nil, fmt.Errorf("SlackAdapter の構築に失敗しました: %w", err)
		}
		appCtx.Notifier = slack

		pipeline, err := buildPipeline(ctx, appCtx)
		if err != nil {
			return nil, fmt.Errorf("パイプラインの初期化に失敗しました: %w", err)
		}
		appCtx.Pipeline = pipeline
	}

	return appCtx, nil
}
