package config

import (
	"fmt"

	"github.com/shouni/netarmor/securenet"
)

// IsSecureServiceURL reports whether the configured ServiceURL uses a secure scheme (HTTPS and the like).
func (c *Config) IsSecureServiceURL() bool {
	return securenet.IsSecureServiceURL(c.Server.ServiceURL)
}

// ValidateEssentialConfig validates the settings.
//
// Format checks (parsing Durations, SERVER_ROLE and so on) are done by LoadConfig, so only
// combinations of values and missing values are checked here. Common items unrelated to the
// role come first, and per-role settings are required only by the process serving that role.
// Requiring OAuth settings on the worker would defeat the point of separate services per role
// (keeping the worker's env minimal).
//
// The point of failing is to stop startup. Let through, a missing setting would surface only
// after a user submits a review, and at the save step after the AI call at that.
func (c *Config) ValidateEssentialConfig() error {
	if c.Server.ServiceURL == "" {
		return fmt.Errorf("SERVICE_URL is not set (it builds the OAuth redirect and the history links)")
	}
	if !c.IsSecureServiceURL() {
		return fmt.Errorf("SERVICE_URL ('%s') must be HTTPS in production", c.Server.ServiceURL)
	}

	if len(c.AI.GeminiModels) == 0 {
		return fmt.Errorf("GEMINI_MODELS is not set (with several, comma-separated, the first is the default and all become choices on the form)")
	}

	// Gemini is called through Vertex AI, so both roles need it.
	if c.GCP.ProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID is not set (Gemini is called through Vertex AI)")
	}

	// Both roles need it: web for the history list, detail and deletion, worker for saving artifacts.
	if c.Storage.GCSBucket == "" {
		return fmt.Errorf("GCS_REVIEW_BUCKET is not set")
	}

	if c.Tasks.DispatchDeadline <= 0 {
		return fmt.Errorf("TASK_DISPATCH_DEADLINE is not set (the deployment settings decide the three-tier timeout, e.g. 10m)")
	}

	if c.Server.Role.ServesWeb() {
		if err := c.validateWebConfig(); err != nil {
			return err
		}
	}

	if c.Server.Role.ServesWorker() {
		if err := c.validateWorkerConfig(); err != nil {
			return err
		}
		if err := c.validateTimeouts(); err != nil {
			return err
		}
	}

	return nil
}

// validateWebConfig validates the web role's settings (OAuth, sessions, task submission).
func (c *Config) validateWebConfig() error {
	// Only the web role submits tasks, so the queue name and the destination are web requirements.
	if c.Tasks.QueueID == "" {
		return fmt.Errorf("CLOUD_TASKS_QUEUE_ID is not set")
	}
	if c.Tasks.WorkerURL == "" {
		return fmt.Errorf("WORKER_URL is not set")
	}

	// LocationID is the queue's region. Only the task submission in builder/task.go uses it;
	// Gemini is fixed to adapters.geminiLocationID ("global"), so the worker does not need it.
	// The infrastructure repository passes it to both roles, but the app keeps no default (if a
	// placeholder let startup through, the failure would come at task submission).
	if c.GCP.LocationID == "" {
		return fmt.Errorf("GCP_LOCATION_ID is not set (the region of the Cloud Tasks queue, e.g. asia-northeast1)")
	}
	// The caller SA is a requirement of the submitting side = the web role. The allowlist the
	// worker accepts is given separately in ALLOWED_TASK_SERVICE_ACCOUNTS.
	if c.Tasks.CallerServiceAccountEmail == "" {
		return fmt.Errorf("TASK_CALLER_SERVICE_ACCOUNT_EMAIL is not set")
	}

	if c.Auth.GoogleClientID == "" || c.Auth.GoogleClientSecret == "" {
		return fmt.Errorf("google OAuth settings (ClientID, ClientSecret) are missing")
	}

	if len(c.Auth.AllowedEmails) == 0 && len(c.Auth.AllowedDomains) == 0 {
		return fmt.Errorf("no allowed email address or domain is set (the allowlist is empty)")
	}

	return nil
}

// validateWorkerConfig validates the worker role's settings (receiving Cloud Tasks).
func (c *Config) validateWorkerConfig() error {
	if c.Tasks.TaskAudienceURL == "" {
		return fmt.Errorf("TASK_AUDIENCE_URL is not set; Cloud Tasks OIDC verification requires it")
	}
	// Empty, the verifier fails closed and every task keeps failing.
	if len(c.Tasks.AllowedServiceAccounts) == 0 {
		return fmt.Errorf("no caller SA is allowed; set ALLOWED_TASK_SERVICE_ACCOUNTS")
	}

	// A negative value is an error rather than silently becoming unlimited (0). The library
	// ignores 0 and below, so input meant to tighten the limit would run with "no limit". A
	// failure over the diff's size then comes after the model call, so the most expensive
	// failure would become the default again.
	if c.Pipeline.MaxDiffBytes < 0 {
		return fmt.Errorf("MAX_DIFF_BYTES cannot be negative (%d); use 0 for unlimited", c.Pipeline.MaxDiffBytes)
	}
	return nil
}

// validateTimeouts checks the ordering of the three-tier timeout at startup.
//
// ★ This package is the only place that sees both PIPELINE_TIMEOUT and the dispatch deadline,
// so the invariant's guard lives here. Inverted, it breaks in the least noticeable way:
// "Cloud Tasks cuts first -> neither a failure report nor a Slack notice is left".
//
// The third tier (Cloud Run's timeout) is invisible to the app, so its relation is covered by
// a precondition in the infrastructure repository.
func (c *Config) validateTimeouts() error {
	// Unlimited is not allowed. If Cloud Tasks cuts first, neither a failure report nor a Slack
	// notice is left and review-queue does not retry with max_attempts = 1, so the review
	// vanishes while queued (the same decision as the sibling apps).
	if c.Pipeline.Timeout <= 0 {
		return fmt.Errorf("PIPELINE_TIMEOUT is not set (it cannot be unlimited on the worker; set a value shorter than TASK_DISPATCH_DEADLINE)")
	}

	if c.Pipeline.Timeout >= c.Tasks.DispatchDeadline {
		return fmt.Errorf(
			"PIPELINE_TIMEOUT (%s) must be shorter than TASK_DISPATCH_DEADLINE (%s); "+
				"otherwise Cloud Tasks cuts the request first, leaving neither a failure report nor a Slack notice, "+
				"and review-queue has max_attempts = 1, so there is no retry either",
			c.Pipeline.Timeout, c.Tasks.DispatchDeadline)
	}

	return nil
}
