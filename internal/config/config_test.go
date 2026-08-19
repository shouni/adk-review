package config

import (
	"strings"
	"testing"

	"time"

	"github.com/shouni/gcp-kit/serverrole"
)

// testDispatchDeadline は、テストで使う打ち切りです。アプリは既定値を持たないため、
// 実際のデプロイ設定と同じく明示します。
const testDispatchDeadline = 10 * time.Minute

// validBase は、検証を通る最小構成を返します。
// 各テストは 1 項目だけ崩して、そこが検出されることを確かめます。
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
		Pipeline: PipelineConfig{Timeout: DefaultPipelineTimeout},
		Storage:  StorageConfig{GCSBucket: "bucket-a"},
		Auth: AuthConfig{
			GoogleClientID:     "client-id",
			GoogleClientSecret: "client-secret",
			SessionSecret:      "session-secret",
			SessionEncryptKey:  "1234567890123456",
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
			// 既定値を持たせると、本番の設定漏れが localhost として素通りします。
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
			wantErr: "認可リスト",
		},
		{
			name:    "invalid encrypt key length",
			mutate:  func(c *Config) { c.Auth.SessionEncryptKey = "short" },
			wantErr: "長さが不正",
		},
		{
			// 既定値へ黙って落ちると、古いモデルを指したまま動き続けます。
			name:    "missing gemini model",
			mutate:  func(c *Config) { c.AI.GeminiModels = nil },
			wantErr: "GEMINI_MODELS",
		},
		{
			// プレースホルダ既定値を廃したので、未設定は起動時に落ちます。
			name:    "missing project id",
			mutate:  func(c *Config) { c.GCP.ProjectID = "" },
			wantErr: "GCP_PROJECT_ID",
		},
		{
			// 落ちないと、AI 呼び出しを終えた最後の保存で初めて失敗します。
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
			name:    "missing caller sa on web",
			mutate:  func(c *Config) { c.Tasks.CallerServiceAccountEmail = "" },
			wantErr: "TASK_CALLER_SERVICE_ACCOUNT_EMAIL",
		},
		{
			// 空だと検証器が fail-closed になり、全タスクが失敗し続けます。
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

// worker 面は OAuth の設定を要求しません。面ごとにサービスを分けた意味
// （worker の env を最小に保つ）が失われるためです。
func TestValidateEssentialConfig_WorkerSkipsWebSettings(t *testing.T) {
	t.Parallel()

	cfg := validBase()
	cfg.Server.Role = serverrole.Worker
	cfg.Auth = AuthConfig{}
	cfg.Tasks.QueueID = ""
	cfg.Tasks.CallerServiceAccountEmail = ""

	if err := cfg.ValidateEssentialConfig(); err != nil {
		t.Fatalf("worker 面が web の設定を要求している: %v", err)
	}
}

func TestValidateTimeouts(t *testing.T) {
	t.Parallel()

	t.Run("既定の PIPELINE_TIMEOUT は打ち切りより短い", func(t *testing.T) {
		t.Parallel()
		if DefaultPipelineTimeout >= testDispatchDeadline {
			t.Fatalf("既定値 %s が打ち切り %s 以上", DefaultPipelineTimeout, testDispatchDeadline)
		}
		if err := validBase().ValidateEssentialConfig(); err != nil {
			t.Errorf("既定値で失敗した: %v", err)
		}
	})

	t.Run("dispatch deadline 以上は起動時に落とす", func(t *testing.T) {
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
				t.Errorf("Pipeline.Timeout=%s で通ってしまった", d)
				continue
			}
			if !strings.Contains(err.Error(), "PIPELINE_TIMEOUT") {
				t.Errorf("エラーに変数名が無い: %v", err)
			}
		}
	})

	// dispatch deadline を伸ばせば、それに合わせて pipeline も伸ばせること。
	// ここが定数に戻ると、長いレビューは再ビルドなしには救えなくなります。
	t.Run("dispatch deadline を伸ばせば pipeline も伸ばせる", func(t *testing.T) {
		t.Parallel()
		c := validBase()
		c.Tasks.DispatchDeadline = 30 * time.Minute
		c.Pipeline.Timeout = 25 * time.Minute
		if err := c.ValidateEssentialConfig(); err != nil {
			t.Errorf("伸ばした組み合わせで失敗した: %v", err)
		}
	})

	t.Run("0 以下は無制限として通す", func(t *testing.T) {
		t.Parallel()
		c := validBase()
		c.Pipeline.Timeout = 0
		if err := c.ValidateEssentialConfig(); err != nil {
			t.Errorf("0（無制限）で失敗した: %v", err)
		}
	})

	t.Run("dispatch deadline が 0 以下なら落とす", func(t *testing.T) {
		t.Parallel()
		c := validBase()
		c.Tasks.DispatchDeadline = 0
		err := c.ValidateEssentialConfig()
		if err == nil {
			t.Fatal("0 が通ってしまった")
		}
		if !strings.Contains(err.Error(), "TASK_DISPATCH_DEADLINE") {
			t.Errorf("エラーに変数名が無い: %v", err)
		}
	})
}

// 環境変数を触るため t.Parallel() は使わない（t.Setenv と併用できない）。
func TestPipelineTimeoutFromEnv(t *testing.T) {
	t.Run("書式エラーは既定値へ黙って落とさず起動時に落とす", func(t *testing.T) {
		t.Setenv("SERVER_ROLE", "both")
		t.Setenv("PIPELINE_TIMEOUT", "25min") // Go の Duration では不正

		if _, err := LoadConfig(); err == nil {
			t.Fatal("不正な書式が素通りした")
		}
	})

	t.Run("正しい書式は読める", func(t *testing.T) {
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

	t.Run("未設定なら既定値", func(t *testing.T) {
		t.Setenv("SERVER_ROLE", "both")
		t.Setenv("PIPELINE_TIMEOUT", "")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}
		if cfg.Pipeline.Timeout != DefaultPipelineTimeout {
			t.Errorf("Pipeline.Timeout = %s, want %s", cfg.Pipeline.Timeout, DefaultPipelineTimeout)
		}
	})

	t.Run("明示の 0 は無制限として読める", func(t *testing.T) {
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

// 定数と envDefault が同じ値であること。片方だけ動かすと、コードが言う既定値と
// 実際に入る値が食い違います。
func TestDefaultsMatchEnvDefaults(t *testing.T) {
	t.Setenv("SERVER_ROLE", "both")
	for _, key := range []string{"PIPELINE_TIMEOUT", "TASK_DISPATCH_DEADLINE", "HTTP_TIMEOUT"} {
		unsetEnvForTest(t, key)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Pipeline.Timeout != DefaultPipelineTimeout {
		t.Errorf("PIPELINE_TIMEOUT の既定 = %s, want %s", cfg.Pipeline.Timeout, DefaultPipelineTimeout)
	}
	// TASK_DISPATCH_DEADLINE に既定値は持ちません（出どころはデプロイ設定 1 箇所）。
	if cfg.Tasks.DispatchDeadline != 0 {
		t.Errorf("TASK_DISPATCH_DEADLINE = %s, 既定値を持たせないでください", cfg.Tasks.DispatchDeadline)
	}
	if cfg.HTTP.Timeout != DefaultHTTPTimeout {
		t.Errorf("HTTP_TIMEOUT の既定 = %s, want %s", cfg.HTTP.Timeout, DefaultHTTPTimeout)
	}
	if cfg.Server.ShutdownTimeout != DefaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s, want %s", cfg.Server.ShutdownTimeout, DefaultShutdownTimeout)
	}
}

// バケットは名前であって URI ではありません。gs:// 付きで渡されても URI を
// 二重に組み立てないこと。
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

// 打ち切りは env が無ければ起動時に落ちること。
//
// 三段のタイムアウトはデプロイ先の事情で決まるので、出どころは Terraform 1 箇所に
// 閉じます。アプリが既定値を持つと同じ数字が 2 箇所に現れ、設定漏れが
// 「誰も選んでいない値」で動いてしまいます。
func TestDispatchDeadlineIsRequired(t *testing.T) {
	t.Parallel()

	cfg := validBase()
	cfg.Tasks.DispatchDeadline = 0

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("未設定が素通りしました")
	}
	if !strings.Contains(err.Error(), "TASK_DISPATCH_DEADLINE") {
		t.Errorf("エラーに変数名がありません: %v", err)
	}
}

// Cloud Tasks の上限を超える値は投入時に拒否されるため、起動時に落とします。
func TestDispatchDeadlineRejectsAbovePlatformMax(t *testing.T) {
	t.Parallel()

	cfg := validBase()
	cfg.Tasks.DispatchDeadline = MaxTaskDispatchDeadline + time.Minute
	cfg.Pipeline.Timeout = time.Minute

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("上限超えが素通りしました")
	}
	if !strings.Contains(err.Error(), "上限") {
		t.Errorf("エラーが上限超えだと分かりません: %v", err)
	}
}
