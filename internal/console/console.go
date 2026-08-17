// Package console は、CLI PoC 用の Publisher / Notifier / PromptGenerator 実装です。
//
// Web 版への移植時には、Publisher は GCS 保存、Notifier は Slack 通知、PromptGenerator は
// go-prompt-kit のテンプレートに置き換わります。このパッケージはその席を埋める最小実装で、
// レビューエンジン（go-review-kit + adkagent）の検証に必要なものしか持ちません。
package console

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/shouni/go-review-kit/review"
)

// Publisher は、レビュー結果を StorageURI のローカルパスへ JSON として保存します。
type Publisher struct{}

var _ review.Publisher = (*Publisher)(nil)

// Publish は、Report を req.StorageURI のパスへ書き出します。
func (p *Publisher) Publish(_ context.Context, req review.Request, report review.Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("console: レポートのエンコードに失敗しました: %w", err)
	}

	if dir := filepath.Dir(req.StorageURI); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("console: 保存先ディレクトリの作成に失敗しました: %w", err)
		}
	}
	if err := os.WriteFile(req.StorageURI, data, 0o600); err != nil {
		return fmt.Errorf("console: レポートの保存に失敗しました: %w", err)
	}
	return nil
}

// Notifier は、実行の顛末を slog へ出すだけの review.Notifier です。
type Notifier struct {
	Logger *slog.Logger
}

var _ review.Notifier = (*Notifier)(nil)

// Notify は、結果の要約をログに出します。
func (n *Notifier) Notify(ctx context.Context, note review.Notification) error {
	logger := n.Logger
	if logger == nil {
		logger = slog.Default()
	}

	attrs := []any{"status", note.Result.Status, "duration", note.Result.Duration}
	if note.Report != nil {
		attrs = append(attrs, "findings", len(note.Report.Findings), "decision", note.Report.Verdict.Decision)
	}
	if note.Err != nil {
		attrs = append(attrs, "step", review.StepOf(note.Err), "error", note.Err)
	}
	logger.InfoContext(ctx, "レビューが終了しました", attrs...)
	return nil
}

// Prompts は、固定文面の review.PromptGenerator です。
//
// mode はそのまま指示文へ埋め込みます。モードごとのテンプレート切り替え
// （go-prompt-kit + フロントマター）は Web 版移植時に導入します。
type Prompts struct{}

var _ review.PromptGenerator = (*Prompts)(nil)

// Generate は、レビュー指示と差分からプロンプトを組み立てます。
func (p *Prompts) Generate(mode, diff string) (string, error) {
	var b strings.Builder
	b.WriteString("次の Git 差分をレビューし、指摘をまとめてください。\n")
	if strings.TrimSpace(mode) != "" {
		fmt.Fprintf(&b, "レビュー観点: %s\n", mode)
	}
	b.WriteString("\n--- 差分 ---\n")
	b.WriteString(diff)
	return b.String(), nil
}
