package config

import (
	"strings"
	"testing"

	"time"

	"github.com/shouni/gcp-kit/serverrole"
)

func TestValidateEssentialConfig(t *testing.T) {
	base := &Config{
		Role:               serverrole.Both,
		ServiceURL:         "https://example.com",
		GoogleClientID:     "client-id",
		GoogleClientSecret: "client-secret",
		SessionSecret:      "session-secret",
		SessionEncryptKey:  "1234567890123456",
		AllowedEmails:      []string{"user@example.com"},
		GeminiModels:       []string{"gemini-test-flash"},
	}

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
			mutate:  func(c *Config) { c.ServiceURL = "http://example.com" },
			wantErr: "HTTPS",
		},
		{
			name:    "missing oauth setting",
			mutate:  func(c *Config) { c.GoogleClientID = "" },
			wantErr: "google OAuth",
		},
		{
			name: "missing allow list",
			mutate: func(c *Config) {
				c.AllowedEmails = nil
				c.AllowedDomains = nil
			},
			wantErr: "認可リスト",
		},
		{
			name:    "invalid encrypt key length",
			mutate:  func(c *Config) { c.SessionEncryptKey = "short" },
			wantErr: "長さが不正",
		},
		{
			// 既定値へ黙って落ちると、古いモデルを指したまま動き続けます。
			name:    "missing gemini model",
			mutate:  func(c *Config) { c.GeminiModels = nil },
			wantErr: "GEMINI_MODELS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := *base
			tt.mutate(&cfg)

			err := cfg.ValidateEssentialConfig()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestValidatePipelineTimeout(t *testing.T) {
	t.Parallel()

	base := func() *Config {
		return &Config{
			ServiceURL:         "https://example.com",
			GoogleClientID:     "client-id",
			GoogleClientSecret: "client-secret",
			SessionSecret:      "session-secret",
			SessionEncryptKey:  "1234567890123456",
			AllowedEmails:      []string{"user@example.com"},
			GeminiModels:       []string{"gemini-test-flash"},
			PipelineTimeout:    DefaultPipelineTimeout,
		}
	}

	t.Run("既定値は dispatch deadline より短い", func(t *testing.T) {
		t.Parallel()
		if DefaultPipelineTimeout >= TaskDispatchDeadline {
			t.Fatalf("既定値 %s が dispatch deadline %s 以上", DefaultPipelineTimeout, TaskDispatchDeadline)
		}
		if err := base().ValidateEssentialConfig(); err != nil {
			t.Errorf("既定値で失敗した: %v", err)
		}
	})

	t.Run("dispatch deadline 以上は起動時に落とす", func(t *testing.T) {
		t.Parallel()
		for _, d := range []time.Duration{TaskDispatchDeadline, TaskDispatchDeadline + time.Minute, time.Hour} {
			c := base()
			c.PipelineTimeout = d
			err := c.ValidateEssentialConfig()
			if err == nil {
				t.Errorf("PipelineTimeout=%s で通ってしまった", d)
				continue
			}
			if !strings.Contains(err.Error(), "PIPELINE_TIMEOUT") {
				t.Errorf("エラーに変数名が無い: %v", err)
			}
		}
	})

	t.Run("0 以下は無制限として通す", func(t *testing.T) {
		t.Parallel()
		c := base()
		c.PipelineTimeout = 0
		if err := c.ValidateEssentialConfig(); err != nil {
			t.Errorf("0（無制限）で失敗した: %v", err)
		}
	})

}

// 環境変数を触るため t.Parallel() は使わない（t.Setenv と併用できない）。
func TestPipelineTimeoutFromEnv(t *testing.T) {
	t.Run("書式エラーは既定値へ黙って落とさず起動時に落とす", func(t *testing.T) {
		t.Setenv("PIPELINE_TIMEOUT", "25min") // Go の Duration では不正
		t.Setenv("SERVER_ROLE", "both")
		c := LoadConfig()
		c.ServiceURL = "https://example.com"
		c.GoogleClientID, c.GoogleClientSecret = "id", "secret"
		c.SessionSecret, c.SessionEncryptKey = "s", "1234567890123456"
		c.AllowedEmails = []string{"user@example.com"}
		c.GeminiModels = []string{"gemini-test-flash"}

		err := c.ValidateEssentialConfig()
		if err == nil {
			t.Fatal("不正な書式が素通りした")
		}
		if !strings.Contains(err.Error(), "PIPELINE_TIMEOUT") {
			t.Errorf("エラーに変数名が無い: %v", err)
		}
	})

	t.Run("正しい書式は読める", func(t *testing.T) {
		t.Setenv("PIPELINE_TIMEOUT", "10m")
		if got := LoadConfig().PipelineTimeout; got != 10*time.Minute {
			t.Errorf("PipelineTimeout = %s, want 10m", got)
		}
	})

	t.Run("未設定なら既定値", func(t *testing.T) {
		t.Setenv("PIPELINE_TIMEOUT", "")
		if got := LoadConfig().PipelineTimeout; got != DefaultPipelineTimeout {
			t.Errorf("PipelineTimeout = %s, want %s", got, DefaultPipelineTimeout)
		}
	})
}
