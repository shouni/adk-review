// adk-review is a web-based orchestrator that has an AI agent review the diff of a Git
// repository.
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

// logLevelEnvKey is the environment variable that sets the log level.
const logLevelEnvKey = "LOG_LEVEL"

func main() {
	// Logger setup (LOG_LEVEL support, structured logs compatible with Cloud Logging).
	//
	// cloudlog maps the level to severity. Without it every line looks the same severity in
	// Cloud Logging, and slog.Error never reaches log-based alerts or metrics.
	// slogctx adds the attributes put on the context (job_id and so on) to every output.
	// Only the assembly is done in the app, the same shape as the sibling apps.
	level := slogctx.ParseLevel(os.Getenv(logLevelEnvKey))
	slog.SetDefault(slog.New(cloudlog.NewHandler(os.Stdout, level)))

	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run initialises the application and starts the server. The exit code is left to main so
// os.Exit does not skip the deferred cleanup.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("failed to load the configuration", "error", err)
		return err
	}
	if err := cfg.ValidateEssentialConfig(); err != nil {
		slog.Error("failed to validate the required configuration", "error", err)
		return err
	}

	if err := server.Run(ctx, cfg); err != nil {
		slog.Error("the application exited abnormally", "error", err)
		return err
	}
	return nil
}
