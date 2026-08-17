// adk-review は、Git リポジトリの差分を AI エージェントにレビューさせるツールです。
//
// 現段階は CLI PoC です。Web/Worker 化（git-gemini-web からの移植）の際に、この main は
// サーバー起動へ置き換わります。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/shouni/go-review-kit/pipeline"

	"github.com/shouni/adk-review/internal/builder"
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("レビューに失敗しました", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	var (
		repoURL = flag.String("repo", "", "リポジトリの URL またはローカルパス（必須）")
		base    = flag.String("base", "main", "比較元の参照")
		head    = flag.String("head", "", "比較対象の参照（必須）")
		mode    = flag.String("mode", "", "レビュー観点（プロンプトへそのまま埋め込まれます）")
		model   = flag.String("model", "", "使用する Gemini モデル名（必須）")
		engine  = flag.String("engine", "agent", "レビューエンジン: single | agent")
		out     = flag.String("out", "report.json", "レビュー結果 JSON の保存先パス")
		tools   = flag.Int("max-tool-calls", 0, "エージェントのツール呼び出し回数上限（0 で既定値）")
	)
	flag.Parse()

	workRoot, err := os.MkdirTemp("", "adk-review-*")
	if err != nil {
		return fmt.Errorf("作業ディレクトリの作成に失敗しました: %w", err)
	}
	// GoGit は Close で自分のチェックアウトを消しますが、ルート自体はこちらの後始末です。
	defer os.RemoveAll(workRoot)

	app, err := builder.New(ctx, builder.Config{
		APIKey:       os.Getenv("GEMINI_API_KEY"),
		ProjectID:    os.Getenv("GCP_PROJECT_ID"),
		LocationID:   os.Getenv("GCP_LOCATION_ID"),
		WorkRoot:     workRoot,
		SSHKeyPath:   os.Getenv("SSH_KEY_PATH"),
		MaxToolCalls: *tools,
	})
	if err != nil {
		return err
	}

	var p *pipeline.Pipeline
	switch *engine {
	case "single":
		p = app.Single
	case "agent":
		p = app.Agent
	default:
		return fmt.Errorf("engine には single か agent を指定してください: %s", *engine)
	}

	req, err := builder.Request(*repoURL, *base, *head, *mode, *model, *out)
	if err != nil {
		return err
	}

	result, report, err := p.Run(ctx, req)
	if err != nil {
		return err
	}

	if report == nil {
		fmt.Printf("差分が無いためレビューをスキップしました (status=%s)\n", result.Status)
		return nil
	}

	abs, absErr := filepath.Abs(*out)
	if absErr != nil {
		abs = *out
	}
	fmt.Printf("%s\n判定: %s（%s）\n指摘: %d 件\n保存先: %s\n",
		report.Title, report.Verdict.Decision, report.Verdict.Reason, len(report.Findings), abs)
	return nil
}
