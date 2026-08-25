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

const (
	// readHeaderTimeout はリクエストヘッダの読み取りに許す時間です。
	// Slowloris 対策なので、正常なクライアントには十分すぎる短さで足ります。
	readHeaderTimeout = 5 * time.Second
	// idleTimeout は keep-alive 接続を保持する上限です。
	idleTimeout = 120 * time.Second
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

	router := NewRouter(appHandlers, cfg.GCP.ProjectID)

	srv := &http.Server{
		Addr:    ":" + cfg.Server.Port,
		Handler: router,
		// ヘッダを少しずつ送り続ける接続に同時実行スロットを占有されないよう、
		// 読み取りには必ず上限を置きます（Cloud Run は同時リクエスト数でスケールするため、
		// 遅い接続を数本掴まれるだけでインスタンスが詰まります）。
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		// WriteTimeout は置きません。レビューの実行は worker 側のハンドラーで
		// 数分かかることがあり、ここで切ると正常な応答を落とします。
		// 実行時間の上限は PIPELINE_TIMEOUT と dispatch deadline が受け持ちます。
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("🚀 サーバーを起動中...", "port", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("サーバーエラーが発生しました: %w", err)

	case <-ctx.Done():
		slog.Info("⚠️ コンテキストのキャンセルを受信、グレースフルシャットダウンを開始します...")
		return gracefulShutdown(srv, cfg.Server.ShutdownTimeout)
	}
}

// gracefulShutdown は、サーバーを安全に停止させます。
//
// 猶予は設定から受け取ります。Cloud Run が SIGKILL するまでの時間より長く取っても
// 待ち切れないため、待つ長さはデプロイ側の事情に合わせられる必要があります。
func gracefulShutdown(srv *http.Server, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
