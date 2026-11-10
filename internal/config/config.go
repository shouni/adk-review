// Package config loads and validates the application settings from environment variables.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-serve-kit/serverrole"
	"github.com/shouni/go-utils/strlist"
)

const (
	// DefaultHTTPTimeout is the default timeout for external HTTP calls.
	// It equals HTTPConfig.Timeout's envDefault; a test catches any drift.
	DefaultHTTPTimeout = 10 * time.Second

	// DefaultMaxDiffBytes is the default limit on the diff sent to the AI (320 KiB).
	// It equals PipelineConfig.MaxDiffBytes's envDefault; a test catches any drift.
	DefaultMaxDiffBytes = 320 * 1024
)

// ServerConfig is the HTTP server's settings.
type ServerConfig struct {
	// ServiceURL is this service's own URL (e.g. https://myapp.run.app).
	//
	// It has no default. With localhost as the default, a setting forgotten in production would
	// still start successfully through "development host name, exempt from the HTTPS check",
	// and only the OAuth redirect and the task destination would quietly point at localhost.
	ServiceURL string `env:"SERVICE_URL"`
	Port       string `env:"PORT" envDefault:"8080"`
	// Role is the role this process serves (web / worker / both). As in the sibling apps it
	// must be explicit; unset or unknown values are an error at startup (LoadConfig).
	// Each role is deployed as its own service; both is for local development only.
	Role serverrole.Role `env:"SERVER_ROLE"`
}

// TasksConfig is the Cloud Tasks queue settings and the OIDC verification on receipt.
// These belong to Cloud Tasks alone, neither general GCP nor HTTP server settings, so they are
// gathered here as in the sibling apps.
type TasksConfig struct {
	QueueID string `env:"CLOUD_TASKS_QUEUE_ID"`
	// WorkerURL is the URL tasks are sent to (the worker service). With web / worker split into
	// separate services, the destination is not necessarily this process. Unset, it falls back
	// to SERVICE_URL (the shape of local development under both).
	WorkerURL       string `env:"WORKER_URL"`
	TaskAudienceURL string `env:"TASK_AUDIENCE_URL"` // The audience URL used to verify OIDC tokens. Unset, it falls back to SERVICE_URL.
	// CallerServiceAccountEmail is the caller SA named in the oidcToken of submitted tasks.
	// Cloud Tasks, not this process, generates and attaches the token.
	// It is a setting of the submitting side (web) only.
	CallerServiceAccountEmail string `env:"TASK_CALLER_SERVICE_ACCOUNT_EMAIL"`
	// DispatchDeadline is the response wait limit set on submitted tasks.
	//
	// It is not "time spent waiting" but the effective limit on the worker's run time. Past it,
	// Cloud Tasks stops waiting even while the worker is still working, and with review-queue's
	// max_attempts = 1 no retry comes. Raising Cloud Run's timeout does not move this limit. It is
	// an env var rather than a constant because an agent review's duration varies with the
	// repository's size, and it must be raisable without a rebuild.
	//
	// It has no default. The three-tier timeout is decided by the deployment, so its single
	// source is Terraform. A default in the app would put the same number in two places, and
	// a missing setting would run on "a value nobody chose".
	DispatchDeadline time.Duration `env:"TASK_DISPATCH_DEADLINE"`
	// AllowedServiceAccounts is the allowlist of SAs whose tokens the worker accepts.
	// web and worker run as separate SAs, so this lists "someone else's (the web role's) SA".
	// The issuer and the allowlist are separate variables because one variable serving both
	// would invert its meaning per role even with the same value, making it unreadable (the
	// infrastructure repository's "Cloud Tasks OIDC" convention).
	AllowedServiceAccounts []string `env:"ALLOWED_TASK_SERVICE_ACCOUNTS"`
}

// GCPConfig is the Google Cloud Platform settings.
type GCPConfig struct {
	// ProjectID has no default. With a placeholder, startup succeeds with the setting missing
	// and the failure comes after a user submits the form (at the Cloud Tasks submission or the
	// Vertex AI call). The error is then the SDK's permission denied, which does not read as a
	// missing setting.
	ProjectID string `env:"GCP_PROJECT_ID"`
	// LocationID is the region the Cloud Tasks queue lives in. It is not Gemini's location,
	// which is fixed in adapters.geminiLocationID.
	LocationID string `env:"GCP_LOCATION_ID"`
}

// AIConfig is the AI model settings.
type AIConfig struct {
	// GeminiModels is the list of model names given in GEMINI_MODELS (comma-separated).
	GeminiModels []string `env:"GEMINI_MODELS"`
	// AgentMaxToolCalls is the limit on tool calls per agent review.
	// 0 uses adkagent's default. An invalid format fails startup in LoadConfig.
	AgentMaxToolCalls int `env:"AGENT_MAX_TOOL_CALLS"`
}

// GitConfig is the settings for fetching the reviewed repository.
type GitConfig struct {
	SSHKeyPath string `env:"SSH_KEY_PATH" envDefault:"~/.ssh/id_rsa"`
}

// PipelineConfig is the settings for running one review.
type PipelineConfig struct {
	// Timeout is the run time limit of one review (clone to AI to publish). It is part of the
	// three-tier timeout, so it has no default and its single source is Terraform (CLAUDE.md).
	// Only the worker role receives it.
	Timeout time.Duration `env:"PIPELINE_TIMEOUT"`

	// MaxDiffBytes is the limit on the diff sent to the AI (bytes). Beyond it the review fails
	// without running. 0 means unlimited.
	//
	// This one has a default. It has no counterpart on the infrastructure side, and unlimited
	// would make the most expensive failure, "fail on truncated output after the model call",
	// the default. 320 KiB comes from measurements (the largest successful diff was 304 KiB, one
	// that failed at the output limit was 480 KiB).
	MaxDiffBytes int `env:"MAX_DIFF_BYTES" envDefault:"327680"`
}

// StorageConfig is the storage settings.
type StorageConfig struct {
	// GCSBucket is where artifacts are stored. It has no default, for the same reason as ProjectID.
	// With a placeholder it would first fail at the final save, after the Gemini call.
	//
	// It is a bucket name, not a URI. It is sometimes pasted from the console as `gs://bucket/`,
	// so normalize drops the prefix and the trailing slash.
	GCSBucket string `env:"GCS_REVIEW_BUCKET"`
}

// HTTPConfig is the settings for external HTTP calls.
type HTTPConfig struct {
	// Timeout is the limit on external HTTP calls such as Slack notifications.
	// It is an env var so it can be raised without a rebuild when a peer is slow.
	Timeout time.Duration `env:"HTTP_TIMEOUT" envDefault:"10s"`
}

// AuthConfig is the authentication and authorisation settings. Only the web role reads it.
type AuthConfig struct {
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	// SessionDatabase is the Firestore database that holds sessions.
	//
	// It is separate from the one for job status. A database name is an identifier that cannot be
	// changed later, so sharing one would leave the name wrong for one of the two uses.
	SessionDatabase string `env:"SESSION_FIRESTORE_DATABASE" envDefault:"sessions"`
	// SessionCollection is the collection that holds sessions.
	SessionCollection string   `env:"SESSION_FIRESTORE_COLLECTION" envDefault:"sessions"`
	AllowedEmails     []string `env:"ALLOWED_EMAILS"`
	AllowedDomains    []string `env:"ALLOWED_DOMAINS"`
	// AllowedM2MServiceAccounts are the service accounts that may call the JSON API server to server
	// (OIDC Bearer). Required on the web role; empty fails startup (builder.newM2MVerifier ->
	// oidc.New rejects an empty allowlist). The M2M path cannot be disabled; unset, verification
	// always fails and falls back to session authentication, so a missing setting shows up only
	// as "the MCP gateway alone receives the login page". It cannot be told from deliberate
	// disabling, so empty is treated as a missing setting.
	//
	// It is a separate variable from Cloud Tasks' ALLOWED_TASK_SERVICE_ACCOUNTS. The roles are
	// inverted ("a caller from another service" vs "the issuer of tasks"), so they are never
	// shared even when the values match.
	AllowedM2MServiceAccounts []string `env:"ALLOWED_M2M_SERVICE_ACCOUNTS"`
}

// NotificationConfig is the notification settings.
type NotificationConfig struct {
	SlackWebhookURL string `env:"SLACK_WEBHOOK_URL"`
}

// Config is the application settings loaded from environment variables.
// Its sections follow the same shape as the sibling apps.
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

// LoadConfig loads the settings from environment variables.
// A value with an invalid format (Duration, integer and so on) is an error here rather than
// silently falling back to the default.
func LoadConfig() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("failed to read the environment variables: %w", err)
	}

	if err := cfg.normalize(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// normalize tidies variant spellings of the loaded values and the defaults that depend on other variables.
func (c *Config) normalize() error {
	// Environment variable names are the app's concern, so the context is added to the kit's error here.
	role, err := serverrole.Parse(string(c.Server.Role))
	if err != nil {
		return fmt.Errorf("SERVER_ROLE: %w", err)
	}
	c.Server.Role = role

	c.Server.ServiceURL = strings.TrimSpace(c.Server.ServiceURL)

	workerURL, err := normalizeWorkerURL(c.Server.Role, c.Tasks.WorkerURL, c.Server.ServiceURL)
	if err != nil {
		return err
	}
	c.Tasks.WorkerURL = workerURL
	c.Tasks.TaskAudienceURL = strings.TrimSpace(c.Tasks.TaskAudienceURL)
	if c.Tasks.TaskAudienceURL == "" {
		c.Tasks.TaskAudienceURL = c.Server.ServiceURL
	}
	c.Tasks.CallerServiceAccountEmail = strings.TrimSpace(c.Tasks.CallerServiceAccountEmail)
	c.Tasks.AllowedServiceAccounts = strlist.Normalize(c.Tasks.AllowedServiceAccounts)

	// env only splits on commas, so surrounding spaces and duplicates are dropped here.
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

// normalizeWorkerURL tidies the worker service URL tasks are delivered to. It returns only
// the service URL; the task path is appended just before submission (internal/builder).
//
// Only a process that serves the worker role derives it from SERVICE_URL. On a split deployment
// SERVICE_URL is fixed to the public side, so deriving it on a web-only process would 404 every task.
func normalizeWorkerURL(role serverrole.Role, workerURL string, serviceURL string) (string, error) {
	workerURL = strings.TrimSpace(workerURL)
	if workerURL != "" {
		if _, err := url.Parse(workerURL); err != nil {
			return "", fmt.Errorf("invalid worker URL %q: %w", workerURL, err)
		}
		return workerURL, nil
	}
	if !role.ServesWorker() {
		return "", nil
	}

	serviceURL = strings.TrimSpace(serviceURL)
	if serviceURL == "" {
		return "", nil
	}
	if _, err := url.Parse(serviceURL); err != nil {
		return "", fmt.Errorf("invalid service URL %q: %w", serviceURL, err)
	}
	return serviceURL, nil
}
