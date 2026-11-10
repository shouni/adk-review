// Package app provides the DI container that builds and holds the application's dependencies.
package app

import (
	"context"
	"io"
	"log/slog"

	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// Container holds the application's dependencies (the DI container).
type Container struct {
	Config *config.Config
	// Storage I/O
	Store  remoteio.Store
	Layout domain.StorageLayout
	// Progress and history
	StatusStore domain.StatusStore
	History     domain.HistoryRepository
	// Asynchronous task submission
	TaskEnqueuer TaskEnqueuer
	// SessionStore is where login sessions are stored. Only the web role has one.
	SessionStore session.Store
	// Running reviews
	Pipeline domain.Pipeline
	// External adapters
	Notifier  review.Notifier
	PromptGen review.PromptGenerator
	// Closers are the resources opened during construction. Container.Close closes them all.
	// Close does not look at individual fields so that a new resource only needs an
	// append in the builder.
	Closers []io.Closer
}

// TaskEnqueuer submits a review request to the queue as an asynchronous task.
type TaskEnqueuer interface {
	Enqueue(ctx context.Context, payload domain.ReviewRequest) error
	Close() error
}

// Close safely releases every external connection the Container holds.
//
// It returns no error because its only caller is a single defer in server.Run, where a
// returned error would have nowhere to go but slog.Error. Logged here without fail, it also
// reaches alerting, since cloudlog maps the level to severity.
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
