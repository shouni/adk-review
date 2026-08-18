// git-gemini-web は、Webベースのコードレビュー・オーケストレーターです。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/server"
)

func main() {
	// 構造化ログの設定
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run はアプリケーションの初期化とサーバー起動を行います。defer によるクリーンアップが
// os.Exit で無視されないよう、終了コードの決定は main 側に委ねます。
func run() error {
	// シグナルを監視するコンテキスト
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 設定のロードとバリデーション
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("設定の読み込みに失敗しました", "error", err)
		return err
	}
	if err := cfg.ValidateEssentialConfig(); err != nil {
		slog.Error("必須設定のバリデーションに失敗しました", "error", err)
		return err
	}

	// サーバー実行
	if err := server.Run(ctx, cfg); err != nil {
		slog.Error("アプリケーションが異常終了しました", "error", err)
		return err
	}
	return nil
}
