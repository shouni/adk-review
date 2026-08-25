// Package app は、アプリケーションの依存関係を組み立てて保持する DI コンテナを提供します。
package app

import (
	"context"
	"io"
	"log/slog"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// Container はアプリケーションの依存関係（DIコンテナ）を保持します。
type Container struct {
	Config *config.Config
	// ストレージ I/O
	RemoteIO *RemoteIO
	Layout   domain.StorageLayout
	// 進行状況と履歴
	StatusStore domain.StatusStore
	History     domain.HistoryRepository
	// 非同期タスクの投入
	TaskEnqueuer TaskEnqueuer
	// レビューの実行
	Pipeline domain.Pipeline
	// 外部アダプター
	Notifier  review.Notifier
	PromptGen review.PromptGenerator
	// Closers は、組み立て時に開いた資源です。Container.Close がまとめて閉じます。
	// Close が個々のフィールドを見ないのは、資源が増えたときに builder が append
	// するだけで済ませるためです。
	Closers []io.Closer
}

// TaskEnqueuer は、レビュー要求を非同期タスクとしてキューへ投入する役割です。
type TaskEnqueuer interface {
	Enqueue(ctx context.Context, payload domain.ReviewRequest) error
	Close() error
}

// RemoteIO は外部ストレージ操作に関するコンポーネントをまとめます。
//
// 実体は go-remote-io が持つ remoteio.Bundle です。同じ構造体と組み立て関数を
// 各アプリが個別に持っていたものをライブラリへ引き取ったため、ここはアプリ内での
// 呼び名を保つための別名だけになっています（rio.Reader などの参照はそのまま使えます）。
type RemoteIO = remoteio.Bundle

// Close は、Container が保持するすべての外部接続リソースを安全に解放します。
//
// エラーを返さないのは、呼び出し元が server.Run の defer 1 箇所きりで、返したところで
// slog.Error 以外の行き先が無いためです。ここで必ず記録しておけば、cloudlog が
// level を severity へマップするのでアラートにも乗ります。
func (c *Container) Close() {
	for _, closer := range c.Closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			slog.Error("failed to close resource", "error", err)
		}
	}
}
