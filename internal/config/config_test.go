package config

import (
	"strings"
	"testing"

	"time"

	"github.com/shouni/go-serve-kit/serverrole"
)

// testDispatchDeadline is the cut-off used in tests. The app has no default, so it is set
// explicitly, as in the real deployment settings.
const testDispatchDeadline = 10 * time.Minute

// testPipelineTimeout is the per-review limit used in tests. Like the cut-off, the app has
// no default, so it is set explicitly, as in the real deployment settings.
const testPipelineTimeout = 5 * time.Minute

// validBase returns the minimal configuration that passes validation.
// Each test breaks a single item and checks that it is detected.
func validBase() *Config {
	return &Config{
		Server: ServerConfig{
			Role:       serverrole.Both,
			ServiceURL: "https://example.com",
		},
		Tasks: TasksConfig{
			QueueID:                   "review-queue",
			WorkerURL:                 "https://worker.example.com",
			TaskAudienceURL:           "https://worker.example.com",
			CallerServiceAccountEmail: "web@example.iam.gserviceaccount.com",
			AllowedServiceAccounts:    []string{"web@example.iam.gserviceaccount.com"},
			DispatchDeadline:          testDispatchDeadline,
		},
		GCP:      GCPConfig{ProjectID: "project-1", LocationID: "asia-northeast1"},
		AI:       AIConfig{GeminiModels: []string{"gemini-test-flash"}},
		Pipeline: PipelineConfig{Timeout: testPipelineTimeout},
		Storage:  StorageConfig{GCSBucket: "bucket-a"},
		Auth: AuthConfig{
			GoogleClientID:     "client-id",
			GoogleClientSecret: "client-secret",
			AllowedEmails:      []string{"user@example.com"},
		},
	}
}

func TestValidateEssentialConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:   "valid config",
			mutate: func(_ *Config) {},
		},
		{
			name:    "insecure service url",
			mutate:  func(c *Config) { c.Server.ServiceURL = "http://example.com" },
			wantErr: "HTTPS",
		},
		{
			// With a default, a missing production setting would slip through as localhost.
			name:    "missing service url",
			mutate:  func(c *Config) { c.Server.ServiceURL = "" },
			wantErr: "SERVICE_URL",
		},
		{
			name:    "missing oauth setting",
			mutate:  func(c *Config) { c.Auth.GoogleClientID = "" },
			wantErr: "google OAuth",
		},
		{
			name: "missing allow list",
			mutate: func(c *Config) {
				c.Auth.AllowedEmails = nil
				c.Auth.AllowedDomains = nil
			},
			wantErr: "allowlist",
		},
		{
			// Silently falling back to a default would keep running on an outdated model.
			name:    "missing gemini model",
			mutate:  func(c *Config) { c.AI.GeminiModels = nil },
			wantErr: "GEMINI_MODELS",
		},
		{
			// The placeholder default is gone, so unset fails at startup.
			name:    "missing project id",
			mutate:  func(c *Config) { c.GCP.ProjectID = "" },
			wantErr: "GCP_PROJECT_ID",
		},
		{
			// Otherwise it would first fail at the final save, after the AI call.
			name:    "missing bucket",
			mutate:  func(c *Config) { c.Storage.GCSBucket = "" },
			wantErr: "GCS_REVIEW_BUCKET",
		},
		{
			name:    "missing queue id on web",
			mutate:  func(c *Config) { c.Tasks.QueueID = "" },
			wantErr: "CLOUD_TASKS_QUEUE_ID",
		},
		{
			name:    "missing worker url on web",
			mutate:  func(c *Config) { c.Tasks.WorkerURL = "" },
			wantErr: "WORKER_URL",
		},
		{
			// The queue's region is also a web-only requirement (Gemini is fixed to "global",
			// so the worker does not use it).
			name:    "missing location id on web",
			mutate:  func(c *Config) { c.GCP.LocationID = "" },
			wantErr: "GCP_LOCATION_ID",
		},
		{
			name:    "missing caller sa on web",
			mutate:  func(c *Config) { c.Tasks.CallerServiceAccountEmail = "" },
			wantErr: "TASK_CALLER_SERVICE_ACCOUNT_EMAIL",
		},
		{
			name:    "missing allowed sa on worker",
			mutate:  func(c *Config) { c.Tasks.AllowedServiceAccounts = nil },
			wantErr: "ALLOWED_TASK_SERVICE_ACCOUNTS",
		},
		{
			name:    "missing audience on worker",
			mutate:  func(c *Config) { c.Tasks.TaskAudienceURL = "" },
			wantErr: "TASK_AUDIENCE_URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validBase()
			tt.mutate(cfg)

			err := cfg.ValidateEssentialConfig()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// The worker role does not require OAuth settings. Doing so would defeat the point of separate
// services per role (keeping the worker's env minimal).
func TestValidateEssentialConfig_WorkerSkipsWebSettings(t *testing.T) {
	t.Parallel()

	cfg := validBase()
	cfg.Server.Role = serverrole.Worker
	cfg.Auth = AuthConfig{}
	cfg.Tasks.QueueID = ""
	cfg.Tasks.CallerServiceAccountEmail = ""
	cfg.GCP.LocationID = ""

	if err := cfg.ValidateEssentialConfig(); err != nil {
		t.Fatalf("the worker role requires web settings: %v", err)
	}
}

func TestValidateTimeouts(t *testing.T) {
	t.Parallel()

	t.Run("the default PIPELINE_TIMEOUT is shorter than the cut-off", func(t *testing.T) {
		t.Parallel()
		if testPipelineTimeout >= testDispatchDeadline {
			t.Fatalf("%s is not shorter than the cut-off %s", testPipelineTimeout, testDispatchDeadline)
		}
		if err := validBase().ValidateEssentialConfig(); err != nil {
			t.Errorf("failed with the defaults: %v", err)
		}
	})

	t.Run("at or above the dispatch deadline fails startup", func(t *testing.T) {
		t.Parallel()
		for _, d := range []time.Duration{
			testDispatchDeadline,
			testDispatchDeadline + time.Minute,
			time.Hour,
		} {
			c := validBase()
			c.Pipeline.Timeout = d
			err := c.ValidateEssentialConfig()
			if err == nil {
				t.Errorf("passed with Pipeline.Timeout=%s", d)
				continue
			}
			if !strings.Contains(err.Error(), "PIPELINE_TIMEOUT") {
				t.Errorf("the error does not name the variable: %v", err)
			}
		}
	})

	// Raising the dispatch deadline lets the pipeline be raised with it.
	// If this went back to a constant, a long review could not be saved without a rebuild.
	t.Run("raising the dispatch deadline lets the pipeline grow", func(t *testing.T) {
		t.Parallel()
		c := validBase()
		c.Tasks.DispatchDeadline = 30 * time.Minute
		c.Pipeline.Timeout = 25 * time.Minute
		if err := c.ValidateEssentialConfig(); err != nil {
			t.Errorf("the raised combination failed: %v", err)
		}
	})

	// Unlimited is not allowed. If Cloud Tasks cuts first, nothing is retried and neither a
	// failure report nor a Slack notice is left, so the review vanishes while queued.
	t.Run("unset or unlimited fails on the worker", func(t *testing.T) {
		t.Parallel()
		for _, d := range []time.Duration{0, -time.Second} {
			c := validBase()
			c.Pipeline.Timeout = d
			err := c.ValidateEssentialConfig()
			if err == nil {
				t.Errorf("Pipeline.Timeout=%s passed", d)
				continue
			}
			if !strings.Contains(err.Error(), "PIPELINE_TIMEOUT") {
				t.Errorf("the error does not name the variable: %v", err)
			}
		}
	})

	// Only the worker role receives it, so the web role does not require it.
	t.Run("the web role does not require PIPELINE_TIMEOUT", func(t *testing.T) {
		t.Parallel()
		c := validBase()
		c.Server.Role = serverrole.Web
		c.Pipeline.Timeout = 0
		if err := c.ValidateEssentialConfig(); err != nil {
			t.Errorf("the web role requires PIPELINE_TIMEOUT: %v", err)
		}
	})

	t.Run("a dispatch deadline at or below 0 fails", func(t *testing.T) {
		t.Parallel()
		c := validBase()
		c.Tasks.DispatchDeadline = 0
		err := c.ValidateEssentialConfig()
		if err == nil {
			t.Fatal("0 passed")
		}
		if !strings.Contains(err.Error(), "TASK_DISPATCH_DEADLINE") {
			t.Errorf("the error does not name the variable: %v", err)
		}
	})
}

// It touches environment variables, so no t.Parallel() (it cannot be combined with t.Setenv).
func TestPipelineTimeoutFromEnv(t *testing.T) {
	t.Run("a format error fails startup instead of falling back to the default", func(t *testing.T) {
		t.Setenv("SERVER_ROLE", "both")
		t.Setenv("PIPELINE_TIMEOUT", "25min") // invalid as a Go Duration

		if _, err := LoadConfig(); err == nil {
			t.Fatal("an invalid format passed")
		}
	})

	t.Run("a valid format is read", func(t *testing.T) {
		t.Setenv("SERVER_ROLE", "both")
		t.Setenv("PIPELINE_TIMEOUT", "10m")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}
		if cfg.Pipeline.Timeout != 10*time.Minute {
			t.Errorf("Pipeline.Timeout = %s, want 10m", cfg.Pipeline.Timeout)
		}
	})

	t.Run("unset gives the default", func(t *testing.T) {
		t.Setenv("SERVER_ROLE", "both")
		t.Setenv("PIPELINE_TIMEOUT", "")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}
		if cfg.Pipeline.Timeout != 0 {
			t.Errorf("Pipeline.Timeout = %s; do not give it a default", cfg.Pipeline.Timeout)
		}
	})

	t.Run("an explicit 0 reads as unlimited", func(t *testing.T) {
		t.Setenv("SERVER_ROLE", "both")
		t.Setenv("PIPELINE_TIMEOUT", "0")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}
		if cfg.Pipeline.Timeout != 0 {
			t.Errorf("Pipeline.Timeout = %s, want 0", cfg.Pipeline.Timeout)
		}
	})
}

// Checks the defaults LoadConfig fills in: HTTP_TIMEOUT's envDefault equals its constant
// (moving only one makes the default the code claims differ from the value actually set), and
// TASK_DISPATCH_DEADLINE has no default.
// The same check for PIPELINE_TIMEOUT is in TestPipelineTimeoutFromEnv.
func TestDefaultsMatchEnvDefaults(t *testing.T) {
	t.Setenv("SERVER_ROLE", "both")
	for _, key := range []string{"TASK_DISPATCH_DEADLINE", "HTTP_TIMEOUT", "MAX_DIFF_BYTES"} {
		unsetEnvForTest(t, key)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// TASK_DISPATCH_DEADLINE has no default (its single source is the deployment settings).
	if cfg.Tasks.DispatchDeadline != 0 {
		t.Errorf("TASK_DISPATCH_DEADLINE = %s; do not give it a default", cfg.Tasks.DispatchDeadline)
	}
	if cfg.HTTP.Timeout != DefaultHTTPTimeout {
		t.Errorf("HTTP_TIMEOUT default = %s, want %s", cfg.HTTP.Timeout, DefaultHTTPTimeout)
	}
	// ★ Unlike the three-tier timeout, this one has a default (the reason is in MaxDiffBytes's doc).
	if cfg.Pipeline.MaxDiffBytes != DefaultMaxDiffBytes {
		t.Errorf("MAX_DIFF_BYTES default = %d, want %d", cfg.Pipeline.MaxDiffBytes, DefaultMaxDiffBytes)
	}
}

// The diff limit is tightened or loosened from measurements. Checks that it can move without a
// rebuild, and that input meant to tighten it does not pass as "unlimited".
func TestMaxDiffBytesFromEnv(t *testing.T) {
	t.Run("env overrides it", func(t *testing.T) {
		t.Setenv("SERVER_ROLE", "both")
		t.Setenv("MAX_DIFF_BYTES", "1048576")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}
		if cfg.Pipeline.MaxDiffBytes != 1048576 {
			t.Errorf("MaxDiffBytes = %d, want 1048576", cfg.Pipeline.MaxDiffBytes)
		}
	})

	t.Run("a format error fails startup instead of falling back to the default", func(t *testing.T) {
		t.Setenv("SERVER_ROLE", "both")
		t.Setenv("MAX_DIFF_BYTES", "320KiB")

		if _, err := LoadConfig(); err == nil {
			t.Fatal("an invalid format passed")
		}
	})

	t.Run("an explicit 0 passes as unlimited", func(t *testing.T) {
		cfg := validBase()
		cfg.Pipeline.MaxDiffBytes = 0

		if err := cfg.ValidateEssentialConfig(); err != nil {
			t.Errorf("0 (unlimited) was rejected: %v", err)
		}
	})

	// ★ The library ignores 0 and below and goes unlimited. Silently falling back would run
	// input meant to tighten the limit with "no limit".
	t.Run("a negative value fails startup", func(t *testing.T) {
		cfg := validBase()
		cfg.Pipeline.MaxDiffBytes = -1

		err := cfg.ValidateEssentialConfig()
		if err == nil {
			t.Fatal("a negative limit passed")
		}
		if !strings.Contains(err.Error(), "MAX_DIFF_BYTES") {
			t.Errorf("the error does not name the offending env var: %v", err)
		}
	})
}

// The bucket is a name, not a URI. Given with gs://, the URI must not be built twice.
func TestGCSBucketNormalization(t *testing.T) {
	t.Setenv("SERVER_ROLE", "both")
	t.Setenv("GCS_REVIEW_BUCKET", " gs://review-archive/ ")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Storage.GCSBucket != "review-archive" {
		t.Errorf("GCSBucket = %q, want %q", cfg.Storage.GCSBucket, "review-archive")
	}
}

// Without the env var, the cut-off fails startup.
//
// The three-tier timeout is decided by the deployment, so its single source is Terraform.
// A default in the app would put the same number in two places, and a missing setting would
// run on "a value nobody chose".
func TestDispatchDeadlineIsRequired(t *testing.T) {
	t.Parallel()

	cfg := validBase()
	cfg.Tasks.DispatchDeadline = 0

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("unset passed")
	}
	if !strings.Contains(err.Error(), "TASK_DISPATCH_DEADLINE") {
		t.Errorf("the error does not name the variable: %v", err)
	}
}
