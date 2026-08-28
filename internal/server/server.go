package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/gcp-kit/cloudrun"

	"github.com/shouni/adk-review/internal/builder"
	"github.com/shouni/adk-review/internal/config"
)

// Run は、サーバーの構築、起動、およびライフサイクル管理を行います。
func Run(ctx context.Context, cfg *config.Config) error {
	slog.Info("🛠️ サーバー依存関係を構築中...")

	appCtx, err := builder.BuildContainer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("アプリケーションコンテキストの構築に失敗しました: %w", err)
	}
	defer func() {
		slog.Info("♻️ アプリケーションコンテキストをクローズ中...")
		appCtx.Close()
	}()

	appHandlers, err := builder.BuildHandlers(appCtx)
	if err != nil {
		return fmt.Errorf("ハンドラーの構築に失敗しました: %w", err)
	}

	slog.InfoContext(ctx, "🚀 サーバーを起動中...", "port", cfg.Server.Port)

	// 起動・シグナル待ち・正常停止（猶予を超えたら強制クローズ）は cloudrun が持ちます。
	// WriteTimeout を置かないのも既定どおりです。レビューの実行は worker 側で数分
	// かかることがあり、置くと正常な応答を途中で切ります。
	return cloudrun.Serve(ctx, cloudrun.Config{
		Port:            cfg.Server.Port,
		Handler:         NewRouter(appHandlers, cfg.GCP.ProjectID),
		ShutdownTimeout: cfg.Server.ShutdownTimeout,
	})
}
