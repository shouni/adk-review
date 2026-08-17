package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/shouni/adk-review/internal/builder"
	"github.com/shouni/adk-review/internal/config"
)

// シャットダウンのデフォルト猶予時間
const defaultShutdownTimeout = 30 * time.Second

// Run は、サーバーの構築、起動、およびライフサイクル管理を行います。
func Run(ctx context.Context, cfg *config.Config) error {
	slog.Info("🛠️ サーバー依存関係を構築中...")

	// 1. アプリケーションコンテキストの構築
	appCtx, err := builder.BuildContainer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("アプリケーションコンテキストの構築に失敗しました: %w", err)
	}
	defer func() {
		slog.Info("♻️ アプリケーションコンテキストをクローズ中...")
		appCtx.Close()
	}()

	// 2. ハンドラーの組み立て
	appHandlers, err := builder.BuildHandlers(appCtx)
	if err != nil {
		return fmt.Errorf("ハンドラーの構築に失敗しました: %w", err)
	}

	// 3. ルーターの作成
	router := NewRouter(appHandlers)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	// 5. サーバー起動（別ゴルーチン）
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("🚀 サーバーを起動中...", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// 6. 停止待機
	select {
	case err := <-serverErrors:
		return fmt.Errorf("サーバーエラーが発生しました: %w", err)

	case <-ctx.Done():
		slog.Info("⚠️ コンテキストのキャンセルを受信、グレースフルシャットダウンを開始します...")
		return gracefulShutdown(srv)
	}
}

// gracefulShutdown は、サーバーを安全に停止させます。
func gracefulShutdown(srv *http.Server) error {
	// シャットダウン用のタイムアウト付きコンテキスト
	ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("グレースフルシャットダウンに失敗しました、強制停止します", "error", err)
		if closeErr := srv.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("サーバーの強制クローズにも失敗しました: %w", closeErr))
		}
		return fmt.Errorf("グレースフルシャットダウン失敗により強制停止しました: %w", err)
	}

	slog.Info("✅ サーバーは正常に停止しました")
	return nil
}
