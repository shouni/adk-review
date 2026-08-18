// Package app は、アプリケーションの依存関係を組み立てて保持する DI コンテナを提供します。
package app

import (
	"context"
	"log/slog"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// Container はアプリケーションの依存関係（DIコンテナ）を保持します。
type Container struct {
	Config *config.Config
	// I/O and Storage
	RemoteIO *RemoteIO
	Layout   domain.StorageLayout
	// Job State and History
	StatusStore domain.StatusStore
	History     domain.HistoryRepository
	// Asynchronous Task
	TaskEnqueuer TaskEnqueuer
	// Business Logic
	Pipeline domain.Pipeline
	// External Adapters
	Notifier  review.Notifier
	PromptGen review.PromptGenerator
}

// TaskEnqueuer は、レビュー要求を非同期タスクとしてキューへ投入する役割です。
type TaskEnqueuer interface {
	Enqueue(ctx context.Context, payload domain.ReviewRequest) error
	Close() error
}

// RemoteIO は外部ストレージ操作に関するコンポーネントをまとめます。
type RemoteIO struct {
	Factory remoteio.IOFactory
	Reader  remoteio.InputReader
	Writer  remoteio.OutputWriter
}

// Close は、RemoteIO が保持する Factory などの内部リソースを解放します。
func (r *RemoteIO) Close() error {
	if r.Factory != nil {
		return r.Factory.Close()
	}
	return nil
}

// Close は、Container が保持するすべての外部接続リソースを安全に解放します。
func (c *Container) Close() {
	// RemoteIO のリソース解放を委譲
	if c.RemoteIO != nil {
		if err := c.RemoteIO.Close(); err != nil {
			slog.Error("failed to close RemoteIO", "error", err)
		}
	}

	// TaskEnqueuer のリソース解放
	if c.TaskEnqueuer != nil {
		if err := c.TaskEnqueuer.Close(); err != nil {
			slog.Error("failed to close task enqueuer", "error", err)
		}
	}
}
