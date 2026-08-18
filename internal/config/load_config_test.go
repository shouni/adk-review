package config

import (
	"os"
	"reflect"
	"testing"
)

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("failed to unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func TestLoadConfig_FromEnvironment(t *testing.T) {
	t.Setenv("SERVER_ROLE", "both")
	t.Setenv("SERVICE_URL", "https://service.example.com")
	t.Setenv("PORT", "9090")
	t.Setenv("GCP_PROJECT_ID", "project-1")
	t.Setenv("GCP_LOCATION_ID", "us-central1")
	t.Setenv("CLOUD_TASKS_QUEUE_ID", "queue-a")
	t.Setenv("TASK_AUDIENCE_URL", "https://aud.example.com")
	t.Setenv("TASK_CALLER_SERVICE_ACCOUNT_EMAIL", "sa@example.iam.gserviceaccount.com")
	t.Setenv("ALLOWED_TASK_SERVICE_ACCOUNTS", "web@example.iam.gserviceaccount.com")
	t.Setenv("GCS_REVIEW_BUCKET", "bucket-a")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.test")
	t.Setenv("GEMINI_MODELS", "gemini-2.5-pro, gemini-2.5-flash")
	t.Setenv("SSH_KEY_PATH", "/tmp/id_rsa")
	t.Setenv("GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("SESSION_SECRET", "session-secret")
	t.Setenv("SESSION_ENCRYPT_KEY", "1234567890123456")
	t.Setenv("ALLOWED_EMAILS", "alice@example.com,bob@example.com")
	t.Setenv("ALLOWED_DOMAINS", "example.com,example.org")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Server.ServiceURL != "https://service.example.com" || cfg.Server.Port != "9090" || cfg.GCP.ProjectID != "project-1" {
		t.Fatalf("unexpected core config: %+v", cfg)
	}
	if cfg.Tasks.TaskAudienceURL != "https://aud.example.com" {
		t.Fatalf("unexpected task audience: %s", cfg.Tasks.TaskAudienceURL)
	}
	wantModels := []string{"gemini-2.5-pro", "gemini-2.5-flash"}
	if !reflect.DeepEqual(cfg.AI.GeminiModels, wantModels) {
		t.Fatalf("gemini models mismatch: got=%v want=%v", cfg.AI.GeminiModels, wantModels)
	}

	wantEmails := []string{"alice@example.com", "bob@example.com"}
	if !reflect.DeepEqual(cfg.Auth.AllowedEmails, wantEmails) {
		t.Fatalf("allowed emails mismatch: got=%v want=%v", cfg.Auth.AllowedEmails, wantEmails)
	}

	wantDomains := []string{"example.com", "example.org"}
	if !reflect.DeepEqual(cfg.Auth.AllowedDomains, wantDomains) {
		t.Fatalf("allowed domains mismatch: got=%v want=%v", cfg.Auth.AllowedDomains, wantDomains)
	}
}

func TestLoadConfig_TaskAudienceDefaultsToServiceURL(t *testing.T) {
	t.Setenv("SERVER_ROLE", "both")
	t.Setenv("SERVICE_URL", "https://service.example.com")
	unsetEnvForTest(t, "TASK_AUDIENCE_URL")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Tasks.TaskAudienceURL != "https://service.example.com" {
		t.Fatalf("expected TaskAudienceURL to default to SERVICE_URL, got %s", cfg.Tasks.TaskAudienceURL)
	}
}

// SERVER_ROLE は明示が必須で、未設定は起動時（LoadConfig）に落ちる契約です。
func TestLoadConfig_RequiresServerRole(t *testing.T) {
	unsetEnvForTest(t, "SERVER_ROLE")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("SERVER_ROLE 未設定が素通りした")
	}
}
