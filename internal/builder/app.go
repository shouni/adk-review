// Package builder provides the factory functions that build the service clients and the
// pipeline's dependencies from the configuration.
package builder

import (
	"context"
	"fmt"
	"io"

	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio/gcs"

	"github.com/shouni/adk-review/internal/adapters"
	"github.com/shouni/adk-review/internal/app"
	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/adk-review/internal/repository"
)

// BuildContainer connects to the external services and returns an app.Container with its dependencies built.
func BuildContainer(ctx context.Context, cfg *config.Config) (container *app.Container, err error) {
	var resources []io.Closer
	defer func() {
		if err != nil {
			for _, r := range resources {
				if r != nil {
					_ = r.Close()
				}
			}
		}
	}()

	// 1. HTTP client
	httpClient := httpkit.New(httpkit.WithTimeout(cfg.HTTP.Timeout))

	// 2. Storage I/O
	storage, err := gcs.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create the GCS storage: %w", err)
	}
	resources = append(resources, storage)
	store, err := storage.Store()
	if err != nil {
		return nil, fmt.Errorf("failed to initialise the I/O components: %w", err)
	}

	// 3. Progress and history
	layout := domain.NewStorageLayout(cfg.Storage.GCSBucket)
	statusStore := buildStatusStore(store, layout)
	history := repository.NewHistory(store, statusStore, layout)

	appCtx := &app.Container{
		Config:      cfg,
		Store:       store,
		Layout:      layout,
		StatusStore: statusStore,
		History:     history,
		// Store only wraps the factory's client; the factory owns its lifetime.
		// When construction fails, resources closes storage directly, so Closers only
		// handles the release after a successful return.
		Closers: []io.Closer{storage},
	}

	// 4. Web-only dependencies: the session store and the Task Enqueuer (where review requests are submitted)
	if cfg.Server.Role.ServesWeb() {
		// Sessions live in a database separate from the job status (SessionDatabase).
		// The store owns the client's lifetime, so the store is registered in Closers.
		sessionStore, err := session.OpenFirestoreStore(ctx, cfg.GCP.ProjectID, cfg.Auth.SessionDatabase, cfg.Auth.SessionCollection)
		if err != nil {
			return nil, fmt.Errorf("failed to build the session store: %w", err)
		}
		resources = append(resources, sessionStore)
		appCtx.Closers = append(appCtx.Closers, sessionStore)
		appCtx.SessionStore = sessionStore

		enqueuer, err := buildTaskEnqueuer(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to build the TaskEnqueuer: %w", err)
		}
		// Assigning a nil pointer to app.TaskEnqueuer makes "a non-nil interface holding
		// nil", which slips past the nil check in Closers and calls Close on a nil receiver.
		// It goes into both only when built, to avoid that. Put only into resources,
		// nobody would close it after a successful return.
		resources = append(resources, enqueuer)
		appCtx.Closers = append(appCtx.Closers, enqueuer)
		appCtx.TaskEnqueuer = enqueuer
	}

	// 5. Worker-only dependencies: prompts, notifications, pipeline (what runs the review)
	if cfg.Server.Role.ServesWorker() {
		promptGen, err := adapters.NewPromptAdapter()
		if err != nil {
			return nil, fmt.Errorf("failed to build the PromptAdapter: %w", err)
		}
		appCtx.PromptGen = promptGen

		slack, err := adapters.NewSlackAdapter(httpClient.WithoutRetry(), cfg.Notification.SlackWebhookURL)
		if err != nil {
			return nil, fmt.Errorf("failed to build the SlackAdapter: %w", err)
		}
		appCtx.Notifier = slack

		pipeline, err := buildPipeline(ctx, appCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to initialise the pipeline: %w", err)
		}
		appCtx.Pipeline = pipeline
	}

	return appCtx, nil
}
