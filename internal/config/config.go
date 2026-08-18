// Package config は、環境変数からアプリケーション設定を読み込み・検証します。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/shouni/gcp-kit/serverrole"
)

const (
	// DefaultHTTPTimeout は外部HTTP通信のデフォルトタイムアウトです。
	DefaultHTTPTimeout = 30 * time.Second

	// TaskDispatchDeadline は Cloud Tasks がワーカーの応答を待つ上限です。
	// 未指定だと既定 10 分が効き、Cloud Run の timeout を伸ばしても効きません。
	TaskDispatchDeadline = 10 * time.Minute

	// DefaultPipelineTimeout はレビュー 1 件の実行時間の上限の既定値です。
	// 実測 3.9〜9.2 秒に対する余裕として 5m。他アプリの 25m より短いのは意図的です。
	// PipelineConfig.Timeout の envDefault と同じ値で、ズレはテストが検知します。
	DefaultPipelineTimeout = 5 * time.Minute
)

// ServerConfig は HTTP サーバーの設定です。
type ServerConfig struct {
	ServiceURL string `env:"SERVICE_URL" envDefault:"http://localhost:8080"` // このサービス自身の URL (例: https://myapp.run.app)
	Port       string `env:"PORT" envDefault:"8080"`
	// Role はこのプロセスが担う役割（web / worker / both）です。兄弟アプリ（ap-*）と
	// 同じく明示が必須で、未設定・未知の値は起動時（LoadConfig）にエラーになります。
	// 面ごとに別サービスとしてデプロイし、both はローカル開発でのみ使います。
	Role serverrole.Role `env:"SERVER_ROLE"`
}

// TasksConfig は Cloud Tasks キューの設定と、受信時の OIDC 検証の設定です。
// Cloud Tasks に閉じた設定であり、GCP 一般の設定でも HTTP サーバーの設定でもないため、
// 兄弟アプリと同じくここに集約します。
type TasksConfig struct {
	QueueID string `env:"CLOUD_TASKS_QUEUE_ID" envDefault:"review-queue"`
	// WorkerURL は、タスクの投入先（worker サービス）の URL です。web / worker を
	// 別サービスに分けたため、投入先は自分自身とは限りません。未設定なら SERVICE_URL
	// （both で動かすローカル開発の形）へ落ちます。
	WorkerURL       string `env:"WORKER_URL"`
	TaskAudienceURL string `env:"TASK_AUDIENCE_URL"` // OIDC トークンの検証に使用する Audience URL。未設定なら SERVICE_URL へ落ちます。
	// CallerServiceAccountEmail は、投入するタスクの oidcToken に指定する caller SA です。
	// トークンを生成して付与するのは Cloud Tasks であり、このプロセスではありません。
	// 投入側（web 面）だけの設定です。
	CallerServiceAccountEmail string `env:"TASK_CALLER_SERVICE_ACCOUNT_EMAIL"`
	// AllowedServiceAccounts は、worker が受け付けるトークンの発行元 SA の許可リストです。
	// web と worker で実行 SA を分けているため、ここには「他人（web 面）の SA」が並びます。
	// 発行者と許可リストを 1 つの変数で兼ねると、同じ値でも役割ごとに意味が反転して
	// 読めなくなるため分けています（ap-infra docs/conventions.md「Cloud Tasks の OIDC」）。
	AllowedServiceAccounts []string `env:"ALLOWED_TASK_SERVICE_ACCOUNTS"`
}

// GCPConfig は Google Cloud Platform の設定です。
type GCPConfig struct {
	ProjectID string `env:"GCP_PROJECT_ID" envDefault:"your-gcp-project"`
	// LocationID は Cloud Tasks のキューが存在するリージョンです。Gemini の
	// ロケーションとは別物で、そちらは adapters.geminiLocationID に固定してあります。
	LocationID string `env:"GCP_LOCATION_ID" envDefault:"asia-northeast1"`
}

// AIConfig は AI モデルの設定です。
type AIConfig struct {
	// GeminiModels は GEMINI_MODELS（カンマ区切り）で指定するモデル名の一覧です。
	GeminiModels []string `env:"GEMINI_MODELS"`
	// AgentMaxToolCalls は、エージェントレビュー 1 件あたりのツール呼び出し回数上限です。
	// 0 なら adkagent 側の既定値を使います。書式が不正なら LoadConfig が起動時に落とします。
	AgentMaxToolCalls int `env:"AGENT_MAX_TOOL_CALLS"`
}

// GitConfig はレビュー対象リポジトリの取得に関する設定です。
type GitConfig struct {
	SSHKeyPath string `env:"SSH_KEY_PATH" envDefault:"~/.ssh/id_rsa"`
}

// PipelineConfig はレビュー 1 件の実行に関する設定です。
type PipelineConfig struct {
	// Timeout はレビュー 1 件（clone〜AI〜公開）の実行時間の上限です。
	// 0 以下は無制限を意味します（ローカルでの長時間デバッグ用の逃げ道）。
	// 未設定と 0 を区別する必要があるため、既定値は normalize ではなく envDefault で
	// 与えます。書式が不正なら黙って既定値へ落とさず、LoadConfig が起動時に落とします。
	Timeout time.Duration `env:"PIPELINE_TIMEOUT" envDefault:"5m"`
}

// StorageConfig はストレージの設定です。
type StorageConfig struct {
	GCSBucket string `env:"GCS_REVIEW_BUCKET" envDefault:"your-review-archive-bucket"`
}

// AuthConfig は認証と認可の設定です。Web 面だけが読みます。
type AuthConfig struct {
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	// SessionSecret はセッションデータのHMAC署名用シークレットキーです。
	SessionSecret string `env:"SESSION_SECRET"`
	// SessionEncryptKey はセッションデータのAES暗号化用シークレットキーです。 16, 24, 32 バイトのいずれかである必要があります。
	SessionEncryptKey string   `env:"SESSION_ENCRYPT_KEY"`
	AllowedEmails     []string `env:"ALLOWED_EMAILS"`
	AllowedDomains    []string `env:"ALLOWED_DOMAINS"`
}

// NotificationConfig は通知の設定です。
type NotificationConfig struct {
	SlackWebhookURL string `env:"SLACK_WEBHOOK_URL"`
}

// Config は環境変数からアプリケーション設定を読み込む構造体です。
// 区分けは兄弟アプリ（ap-voice など）と同じ形に揃えています。
type Config struct {
	Server       ServerConfig
	Tasks        TasksConfig
	GCP          GCPConfig
	AI           AIConfig
	Git          GitConfig
	Pipeline     PipelineConfig
	Storage      StorageConfig
	Auth         AuthConfig
	Notification NotificationConfig
}

// LoadConfig は環境変数から設定を読み込みます。
// 書式が不正な値（Duration や整数など）は黙って既定値へ落とさず、ここでエラーになります。
func LoadConfig() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("環境変数の読み込みに失敗しました: %w", err)
	}

	if err := cfg.normalize(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// normalize は、読み込んだ値の表記ゆれと、他の変数に依存する既定値を整えます。
func (c *Config) normalize() error {
	// 環境変数名はアプリ側の関心事なので、キットのエラーへここで文脈を足します。
	role, err := serverrole.Parse(string(c.Server.Role))
	if err != nil {
		return fmt.Errorf("SERVER_ROLE: %w", err)
	}
	c.Server.Role = role

	c.Server.ServiceURL = strings.TrimSpace(c.Server.ServiceURL)

	c.Tasks.WorkerURL = strings.TrimSpace(c.Tasks.WorkerURL)
	if c.Tasks.WorkerURL == "" {
		c.Tasks.WorkerURL = c.Server.ServiceURL
	}
	c.Tasks.TaskAudienceURL = strings.TrimSpace(c.Tasks.TaskAudienceURL)
	if c.Tasks.TaskAudienceURL == "" {
		c.Tasks.TaskAudienceURL = c.Server.ServiceURL
	}
	c.Tasks.CallerServiceAccountEmail = strings.TrimSpace(c.Tasks.CallerServiceAccountEmail)
	c.Tasks.AllowedServiceAccounts = normalizeList(c.Tasks.AllowedServiceAccounts)

	// env はカンマで分割するだけなので、前後の空白と重複はここで落とします。
	c.AI.GeminiModels = normalizeList(c.AI.GeminiModels)

	c.Auth.AllowedEmails = normalizeList(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = normalizeList(c.Auth.AllowedDomains)

	return nil
}

// normalizeList は env が分割しただけのカンマ区切り値を整えます。
// 前後の空白を落とし、空要素と重複を捨て、順序は保ちます。
// 既定値で埋めることはせず、空なら空のまま返します。
func normalizeList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		normalized = append(normalized, v)
	}
	return normalized
}
