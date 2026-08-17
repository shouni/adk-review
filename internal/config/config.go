// Package config は、環境変数からアプリケーション設定を読み込み・検証します。
package config

import (
	"time"

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
	DefaultPipelineTimeout = 5 * time.Minute
)

// Config は環境変数からアプリケーション設定を読み込む構造体です。
type Config struct {
	// Role はこのプロセスが担う役割（web / worker / both）です。兄弟アプリ（ap-*）と
	// 同じく明示が必須で、未設定・未知の値は起動時エラーになります。旧 git-gemini-web は
	// 1 サービスが両面を兼ねていましたが、本アプリは面ごとに別サービスとしてデプロイします。
	Role serverrole.Role
	// roleErr は SERVER_ROLE の解析に失敗したときの理由です。LoadConfig はエラーを
	// 返さない契約なのでここへ持ち越し、ValidateEssentialConfig が起動時に落とします。
	roleErr error

	ServiceURL string // このサービス自身の URL (例: https://myapp.run.app)
	// WorkerURL は、タスクの投入先（worker サービス）の URL です。web / worker を
	// 別サービスに分けたため、投入先は自分自身とは限りません。未設定なら SERVICE_URL
	// （both で動かすローカル開発の形）へ落ちます。
	WorkerURL           string
	Port                string
	ProjectID           string
	LocationID          string
	QueueID             string
	TaskAudienceURL     string // OIDC トークンの検証に使用する Audience URL
	ServiceAccountEmail string
	GCSBucket           string
	SlackWebhookURL     string
	GeminiAPIKey        string

	// AgentMaxToolCalls は、エージェントレビュー 1 件あたりのツール呼び出し回数上限です。
	// 0 なら adkagent 側の既定値を使います。
	AgentMaxToolCalls int
	// agentMaxToolCallsErr は AGENT_MAX_TOOL_CALLS の解析に失敗したときの理由です。
	agentMaxToolCallsErr error

	// GeminiModels は GEMINI_MODELS（カンマ区切り）で指定するモデル名の一覧です。
	GeminiModels []string

	SSHKeyPath string

	// PipelineTimeout はレビュー 1 件（clone〜AI〜公開）の実行時間の上限です。
	// 0 以下は無制限を意味します。詳細は DefaultPipelineTimeout のコメント。
	PipelineTimeout time.Duration
	// pipelineTimeoutErr は PIPELINE_TIMEOUT の解析に失敗したときの理由です。
	// LoadConfig はエラーを返さない契約なのでここへ持ち越し、
	// ValidateEssentialConfig が起動時に落とします（黙って既定値へ落ちない）。
	pipelineTimeoutErr error

	// OAuth & Session Settings
	GoogleClientID     string
	GoogleClientSecret string
	// SessionSecret はセッションデータのHMAC署名用シークレットキーです。
	SessionSecret string
	// SessionEncryptKey はセッションデータのAES暗号化用シークレットキーです。 16, 24, 32 バイトのいずれかである必要があります。
	SessionEncryptKey string

	// Auth Settings
	AllowedEmails  []string
	AllowedDomains []string
}

// LoadConfig は環境変数から設定を読み込みます。
func LoadConfig() *Config {
	serviceURL := getEnv("SERVICE_URL", "http://localhost:8080")
	allowedEmails := getEnv("ALLOWED_EMAILS", "")
	allowedDomains := getEnv("ALLOWED_DOMAINS", "")
	pipelineTimeout, pipelineTimeoutErr := parseDurationEnv("PIPELINE_TIMEOUT", DefaultPipelineTimeout)
	role, roleErr := serverrole.Parse(getEnv("SERVER_ROLE", ""))
	agentMaxToolCalls, agentMaxToolCallsErr := parseIntEnv("AGENT_MAX_TOOL_CALLS", 0)

	return &Config{
		Role:    role,
		roleErr: roleErr,

		ServiceURL: serviceURL,
		WorkerURL:  getEnv("WORKER_URL", serviceURL),

		AgentMaxToolCalls:    agentMaxToolCalls,
		agentMaxToolCallsErr: agentMaxToolCallsErr,

		Port:                getEnv("PORT", "8080"),
		ProjectID:           getEnv("GCP_PROJECT_ID", "your-gcp-project"),
		LocationID:          getEnv("GCP_LOCATION_ID", "asia-northeast1"),
		QueueID:             getEnv("CLOUD_TASKS_QUEUE_ID", "review-queue"),
		TaskAudienceURL:     getEnv("TASK_AUDIENCE_URL", serviceURL),
		ServiceAccountEmail: getEnv("SERVICE_ACCOUNT_EMAIL", ""),
		GCSBucket:           getEnv("GCS_REVIEW_BUCKET", "your-review-archive-bucket"),
		SlackWebhookURL:     getEnv("SLACK_WEBHOOK_URL", ""),
		GeminiAPIKey:        getEnv("GEMINI_API_KEY", ""),
		GeminiModels:        parseCommaSeparatedList(getEnv("GEMINI_MODELS", "")),
		SSHKeyPath:          getEnv("SSH_KEY_PATH", "~/.ssh/id_rsa"),
		PipelineTimeout:     pipelineTimeout,
		pipelineTimeoutErr:  pipelineTimeoutErr,

		// OAuth & Session
		GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
		SessionSecret:      getEnv("SESSION_SECRET", ""),
		SessionEncryptKey:  getEnv("SESSION_ENCRYPT_KEY", ""),

		AllowedEmails:  parseCommaSeparatedList(allowedEmails),
		AllowedDomains: parseCommaSeparatedList(allowedDomains),
	}
}
