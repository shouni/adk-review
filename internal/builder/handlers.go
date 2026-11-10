package builder

import (
	"fmt"

	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/adk-review/internal/app"
	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/adk-review/internal/server/handlers"
)

const defaultSessionName = "adk-review-session"

// AppHandlers holds every HTTP handler built.
//
// Handlers for a role this process does not serve stay nil. The router skips registering on
// nil, so adding a role needs no routing change (the same shape as the sibling apps).
type AppHandlers struct {
	Auth   *session.Handler
	Web    *handlers.Handler
	Worker *worker.Handler[domain.ReviewRequest]
	// TaskAuth verifies the OIDC token from Cloud Tasks. Unlike Auth it needs no OAuth
	// settings, so it is kept as a separate piece that only verifies.
	TaskAuth *oidc.Verifier
	// M2M verifies OIDC Bearer tokens from other services (the MCP gateway). It is always
	// configured when serving the web role (unset fails startup; see newM2MVerifier).
	M2M *oidc.Verifier
}

// Validate checks that each role's handlers are complete.
//
// Started incomplete, the router just skips registering those routes, so the deploy succeeds,
// /health passes, and only the submission path is broken. Cloud Tasks gets a 404 and, with
// max_attempts = 1, drops the task, so the review request is silently lost. Startup fails
// before the router can return a 404.
func (h *AppHandlers) Validate() error {
	if (h.Auth == nil) != (h.Web == nil) {
		return fmt.Errorf("web handlers are incomplete (auth: %t, web: %t)", h.Auth != nil, h.Web != nil)
	}
	if (h.TaskAuth == nil) != (h.Worker == nil) {
		return fmt.Errorf("worker handlers are incomplete (task_auth: %t, worker: %t)", h.TaskAuth != nil, h.Worker != nil)
	}
	if h.Auth == nil && h.Worker == nil {
		return fmt.Errorf("no handler was built for any role")
	}
	return nil
}

// BuildHandlers injects the dependencies and builds only the handlers of the roles served.
func BuildHandlers(appCtx *app.Container) (*AppHandlers, error) {
	appHandlers := &AppHandlers{}
	role := appCtx.Config.Server.Role

	if role.ServesWeb() {
		authHandler, err := createAuthHandler(appCtx.Config, appCtx.SessionStore)
		if err != nil {
			return nil, err
		}
		appHandlers.Auth = authHandler

		webHandler, err := handlers.NewHandler(handlers.Deps{
			Config:       appCtx.Config,
			TaskEnqueuer: appCtx.TaskEnqueuer,
			Layout:       appCtx.Layout,
			StatusStore:  appCtx.StatusStore,
			History:      appCtx.History,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialise the WebHandler: %w", err)
		}
		appHandlers.Web = webHandler

		m2m, err := newM2MVerifier(appCtx.Config.Server.ServiceURL, appCtx.Config.Auth.AllowedM2MServiceAccounts)
		if err != nil {
			return nil, err
		}
		appHandlers.M2M = m2m
	}

	if role.ServesWorker() {
		// Build the Worker handler and the Cloud Tasks OIDC verifier guarding its entrance.
		// Without both the audience and the issuing service accounts, verification always
		// fails (fail-closed), so the configuration is checked at startup.
		appHandlers.Worker = worker.NewHandler[domain.ReviewRequest](appCtx.Pipeline)
		taskAuth, err := oidc.New(appCtx.Config.Tasks.TaskAudienceURL, appCtx.Config.Tasks.AllowedServiceAccounts)
		if err != nil {
			return nil, fmt.Errorf("cannot configure Cloud Tasks OIDC verification: TASK_AUDIENCE_URL and ALLOWED_TASK_SERVICE_ACCOUNTS are required: %w", err)
		}
		appHandlers.TaskAuth = taskAuth
	}

	if err := appHandlers.Validate(); err != nil {
		return nil, err
	}

	return appHandlers, nil
}

// createAuthHandler initialises and returns the auth handler from the given Config.
//
// The caller passes the session store. Having no default is gcp-kit's decision: silently
// falling back to an in-process implementation would log users out every time the Cloud Run
// instance changes, which goes unnoticed in development with a single instance.
func createAuthHandler(cfg *config.Config, store session.Store) (*session.Handler, error) {
	return session.New(session.Config{
		ClientID:       cfg.Auth.GoogleClientID,
		ClientSecret:   cfg.Auth.GoogleClientSecret,
		ServiceURL:     cfg.Server.ServiceURL,
		SessionName:    defaultSessionName,
		Store:          store,
		AllowedEmails:  cfg.Auth.AllowedEmails,
		AllowedDomains: cfg.Auth.AllowedDomains,
	})
}

// newM2MVerifier configures the OIDC verifier for M2M (server-to-server) calls.
//
// Unset is not read as "deliberately disabled"; it fails startup, because auth.Protected cannot
// disable M2M. With the allowlist or the audience missing the path stays alive, verification
// always fails and falls back to session authentication. A missing setting therefore shows up
// only as "the browser works but the MCP gateway alone receives the login page's HTML". With no
// way to tell deliberate disabling from a missing setting, empty can only mean the latter, so
// it is rejected at startup, like TaskVerifier.
//
// This used to be the one place that "stays nil (= passes through) when unset", while all 4
// sibling apps failed at startup; adk-review alone differed.
//
// Whether it can be configured is asked of the verifier itself rather than the config, because
// gcp-kit knows which settings are needed. Checking only for an empty allowlist in the config
// would miss a missing audience (SERVICE_URL) and would not follow when the kit adds requirements.
func newM2MVerifier(serviceURL string, allowedServiceAccounts []string) (*oidc.Verifier, error) {
	m2m, err := oidc.New(serviceURL, allowedServiceAccounts)
	if err != nil {
		return nil, fmt.Errorf("cannot configure m2m OIDC verification (SERVICE_URL and ALLOWED_M2M_SERVICE_ACCOUNTS): %w", err)
	}
	return m2m, nil
}
