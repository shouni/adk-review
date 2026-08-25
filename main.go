// adk-review は、Git リポジトリの差分を AI エージェントにレビューさせる
// Web ベースのオーケストレーターです。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/server"
)

// logLevelEnvKey はログ出力レベルを指定する環境変数名です。
const logLevelEnvKey = "LOG_LEVEL"

func main() {
	// ロガーの設定（LOG_LEVEL 対応・Cloud Logging 互換の構造化ログ）。
	//
	// cloudlog が level を severity へマップします。これが無いと Cloud Logging 上では
	// 全行が同じ重大度に見え、slog.Error がログベースの通知やメトリクスに乗りません。
	// slogctx は context に載せた属性（job_id など）を全出力へ付けます。
	// 組み立てだけをアプリ側で行うのは兄弟アプリと同じ形です。
	level := slogctx.ParseLevel(os.Getenv(logLevelEnvKey))
	base := slog.NewJSONHandler(os.Stdout, cloudlog.HandlerOptions(level))
	slog.SetDefault(slog.New(slogctx.NewHandler(base)))

	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run はアプリケーションの初期化とサーバー起動を行います。defer によるクリーンアップが
// os.Exit で無視されないよう、終了コードの決定は main 側に委ねます。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("設定の読み込みに失敗しました", "error", err)
		return err
	}
	if err := cfg.ValidateEssentialConfig(); err != nil {
		slog.Error("必須設定のバリデーションに失敗しました", "error", err)
		return err
	}

	if err := server.Run(ctx, cfg); err != nil {
		slog.Error("アプリケーションが異常終了しました", "error", err)
		return err
	}
	return nil
}
