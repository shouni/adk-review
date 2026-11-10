package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/gcp-kit/cloudrun"

	"github.com/shouni/adk-review/internal/builder"
	"github.com/shouni/adk-review/internal/config"
)

// Run builds and starts the server and manages its lifecycle.
func Run(ctx context.Context, cfg *config.Config) error {
	slog.Info("🛠️ building server dependencies...")

	appCtx, err := builder.BuildContainer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to build the application context: %w", err)
	}
	defer func() {
		slog.Info("♻️ closing the application context...")
		appCtx.Close()
	}()

	appHandlers, err := builder.BuildHandlers(appCtx)
	if err != nil {
		return fmt.Errorf("failed to build the handlers: %w", err)
	}

	slog.InfoContext(ctx, "🚀 starting the server...", "port", cfg.Server.Port)

	// Starting, waiting for signals and graceful shutdown (force-closing past the grace period)
	// belong to cloudrun. No WriteTimeout, also as per the default: a review can take minutes on
	// the worker, and a WriteTimeout would cut sound responses midway.
	return cloudrun.Serve(ctx, cloudrun.Config{
		Port:    cfg.Server.Port,
		Handler: NewRouter(appHandlers, cfg.GCP.ProjectID),
	})
}
