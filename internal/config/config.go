// Package config は、環境変数からアプリケーション設定を読み込み・検証します。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/shouni/gcp-kit/serverrole"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/strlist"
)

const (
	// DefaultHTTPTimeout は外部 HTTP 通信のタイムアウトの既定値です。
	// HTTPConfig.Timeout の envDefault と同じ値で、ズレはテストが検知します。
	DefaultHTTPTimeout = 30 * time.Second

	// DefaultShutdownTimeout は SIGTERM 後にサーバーの停止を待つ上限の既定値です。
	// Cloud Run が SIGKILL するまでの猶予より長く取っても待ち切れないため、
	// 兄弟アプリと同じく短めに置きます。
	DefaultShutdownTimeout = 15 * time.Second
)

// ServerConfig は HTTP サーバーの設定です。
type ServerConfig struct {
	// ServiceURL はこのサービス自身の URL です (例: https://myapp.run.app)。
	//
	// 既定値は持ちません。localhost を既定にすると、本番で設定を入れ忘れても
	// 「開発用ホスト名なので HTTPS 検査を免除」という経路で起動が成功してしまい、
	// OAuth のリダイレクト先とタスク投入先だけが静かに localhost を向きます。
	ServiceURL string `env:"SERVICE_URL"`
	Port       string `env:"PORT" envDefault:"8080"`
	// Role はこのプロセスが担う役割（web / worker / both）です。兄弟アプリと
	// 同じく明示が必須で、未設定・未知の値は起動時（LoadConfig）にエラーになります。
	// 面ごとに別サービスとしてデプロイし、both はローカル開発でのみ使います。
	Role serverrole.Role `env:"SERVER_ROLE"`
	// ShutdownTimeout は SIGTERM 後に停止を待つ上限です。env からは読まず、
	// normalize が既定値で埋めます。
	ShutdownTimeout time.Duration `env:"-"`
}

// TasksConfig は Cloud Tasks キューの設定と、受信時の OIDC 検証の設定です。
// Cloud Tasks に閉じた設定であり、GCP 一般の設定でも HTTP サーバーの設定でもないため、
// 兄弟アプリと同じくここに集約します。
type TasksConfig struct {
	QueueID string `env:"CLOUD_TASKS_QUEUE_ID"`
	// WorkerURL は、タスクの投入先（worker サービス）の URL です。web / worker を
	// 別サービスに分けたため、投入先は自分自身とは限りません。未設定なら SERVICE_URL
	// （both で動かすローカル開発の形）へ落ちます。
	WorkerURL       string `env:"WORKER_URL"`
	TaskAudienceURL string `env:"TASK_AUDIENCE_URL"` // OIDC トークンの検証に使用する Audience URL。未設定なら SERVICE_URL へ落ちます。
	// CallerServiceAccountEmail は、投入するタスクの oidcToken に指定する caller SA です。
	// トークンを生成して付与するのは Cloud Tasks であり、このプロセスではありません。
	// 投入側（web 面）だけの設定です。
	CallerServiceAccountEmail string `env:"TASK_CALLER_SERVICE_ACCOUNT_EMAIL"`
	// DispatchDeadline は、投入するタスクに載せる応答待ちの上限です。
	//
	// 「待つ時間」ではなく **ワーカーの実行時間の実効上限** です。これを超えると
	// ワーカーがまだ処理中でも Cloud Tasks が待受を打ち切り、review-queue は
	// max_attempts = 1 なので再試行も来ません。Cloud Run の timeout をいくら伸ばしても
	// この上限は動きません。定数ではなく env なのは、エージェントレビューの所要時間が
	// リポジトリの大きさで変わり、再ビルドなしで伸ばせる必要があるためです。
	//
	// **既定値は持ちません。** 三段のタイムアウトはデプロイ先の事情で決まる値なので、
	// 出どころは Terraform 1 箇所に閉じます。アプリが既定を持つと同じ数字が 2 箇所に
	// 現れ、設定漏れが「誰も選んでいない値」で動いてしまいます。
	DispatchDeadline time.Duration `env:"TASK_DISPATCH_DEADLINE"`
	// AllowedServiceAccounts は、worker が受け付けるトークンの発行元 SA の許可リストです。
	// web と worker で実行 SA を分けているため、ここには「他人（web 面）の SA」が並びます。
	// 発行者と許可リストを 1 つの変数で兼ねると、同じ値でも役割ごとに意味が反転して
	// 読めなくなるため分けています（インフラ管理リポジトリの規約「Cloud Tasks の OIDC」）。
	AllowedServiceAccounts []string `env:"ALLOWED_TASK_SERVICE_ACCOUNTS"`
}

// GCPConfig は Google Cloud Platform の設定です。
type GCPConfig struct {
	// ProjectID に既定値は持ちません。プレースホルダを置くと設定漏れのまま起動が成功し、
	// 失敗するのは利用者がフォームを送ったあと（Cloud Tasks 投入や Vertex AI 呼び出し）
	// になります。しかもエラーは SDK 由来の permission denied で、設定漏れだと読めません。
	ProjectID string `env:"GCP_PROJECT_ID"`
	// LocationID は Cloud Tasks のキューが存在するリージョンです。Gemini の
	// ロケーションとは別物で、そちらは adapters.geminiLocationID に固定してあります。
	LocationID string `env:"GCP_LOCATION_ID"`
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
	//
	// **既定値は持ちません。** TASK_DISPATCH_DEADLINE と同じく三段のタイムアウトの一部で、
	// 出どころはデプロイ設定（Terraform）1 箇所に閉じます。既定を持つと同じ数字が
	// 2 箇所に現れ、設定漏れが「誰も選んでいない値」で動いてしまいます。
	// 渡されるのは worker 面だけなので、必須なのも worker 面だけです。
	Timeout time.Duration `env:"PIPELINE_TIMEOUT"`
}

// StorageConfig はストレージの設定です。
type StorageConfig struct {
	// GCSBucket は成果物の置き場です。ProjectID と同じ理由で既定値は持ちません。
	// プレースホルダのままだと、Gemini 呼び出しを終えた最後の保存で初めて落ちます。
	//
	// **バケット「名」であって URI ではありません。** コンソールから `gs://bucket/` の形で
	// 貼られることがあるため、normalize で接頭辞と末尾スラッシュを落とします。
	GCSBucket string `env:"GCS_REVIEW_BUCKET"`
}

// HTTPConfig は外部 HTTP 通信の設定です。
type HTTPConfig struct {
	// Timeout は Slack 通知など外部 HTTP 呼び出しの上限です。
	// 遅い相手に当たったときに再ビルドなしで伸ばせるよう env にしてあります。
	Timeout time.Duration `env:"HTTP_TIMEOUT" envDefault:"30s"`
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
	// AllowedM2MServiceAccounts は、JSON API をサーバー間通信（OIDC Bearer）で呼べる
	// サービスアカウントです。**空なら M2M は無効**で、web 面は人間の OAuth だけで動きます
	// （web 面は画面が使えれば成立するため、ワーカー面のように必須にはしません）。
	//
	// Cloud Tasks の ALLOWED_TASK_SERVICE_ACCOUNTS とは別の変数です。役割が
	// 「他サービスからの呼び出し元」と「タスクの発行元」で反転するため、
	// 同じ値になる場合でも兼ねさせません。
	AllowedM2MServiceAccounts []string `env:"ALLOWED_M2M_SERVICE_ACCOUNTS"`
}

// NotificationConfig は通知の設定です。
type NotificationConfig struct {
	SlackWebhookURL string `env:"SLACK_WEBHOOK_URL"`
}

// Config は環境変数からアプリケーション設定を読み込む構造体です。
// 区分けは兄弟アプリと同じ形に揃えています。
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
	HTTP         HTTPConfig
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
	c.Server.ShutdownTimeout = DefaultShutdownTimeout

	c.Tasks.WorkerURL = strings.TrimSpace(c.Tasks.WorkerURL)
	if c.Tasks.WorkerURL == "" {
		c.Tasks.WorkerURL = c.Server.ServiceURL
	}
	c.Tasks.TaskAudienceURL = strings.TrimSpace(c.Tasks.TaskAudienceURL)
	if c.Tasks.TaskAudienceURL == "" {
		c.Tasks.TaskAudienceURL = c.Server.ServiceURL
	}
	c.Tasks.CallerServiceAccountEmail = strings.TrimSpace(c.Tasks.CallerServiceAccountEmail)
	c.Tasks.AllowedServiceAccounts = strlist.Normalize(c.Tasks.AllowedServiceAccounts)

	// env はカンマで分割するだけなので、前後の空白と重複はここで落とします。
	c.AI.GeminiModels = strlist.Normalize(c.AI.GeminiModels)

	c.Auth.AllowedEmails = strlist.Normalize(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = strlist.Normalize(c.Auth.AllowedDomains)
	c.Auth.AllowedM2MServiceAccounts = strlist.Normalize(c.Auth.AllowedM2MServiceAccounts)

	c.GCP.ProjectID = strings.TrimSpace(c.GCP.ProjectID)
	c.GCP.LocationID = strings.TrimSpace(c.GCP.LocationID)
	c.Git.SSHKeyPath = strings.TrimSpace(c.Git.SSHKeyPath)
	c.Notification.SlackWebhookURL = strings.TrimSpace(c.Notification.SlackWebhookURL)
	c.Storage.GCSBucket = remoteio.NormalizeBucketName(c.Storage.GCSBucket)

	return nil
}
