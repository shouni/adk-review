package server

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/adk-review/internal/builder"
	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/adk-review/internal/server/handlers"
	"github.com/shouni/gcp-kit/auth"
)

type noopTaskEnqueuer struct{}

func (noopTaskEnqueuer) Enqueue(context.Context, domain.ReviewRequest) error { return nil }
func (noopTaskEnqueuer) Close() error                                        { return nil }

type noopPipeline struct{}

func (noopPipeline) Execute(context.Context, domain.ReviewRequest) error { return nil }

// newTestAuthHandler returns an auth.Handler for tests.
// It performs no real OAuth exchange and covers only session checks and CSRF verification.
func newTestAuthHandler(t *testing.T) *session.Handler {
	t.Helper()

	h, err := session.New(session.Config{
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		ServiceURL:    "https://service.example.com",
		Store:         session.NewMemoryStore(),
		SessionName:   "test-session",
		AllowedEmails: []string{"tester@example.com"},
	})
	if err != nil {
		t.Fatalf("session.New() error = %v", err)
	}
	return h
}

func newRouterForTest(t *testing.T) http.Handler {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{ServiceURL: "https://service.example.com"},
		Tasks: config.TasksConfig{
			TaskAudienceURL:        "https://service.example.com",
			AllowedServiceAccounts: []string{"tasks@example.iam.gserviceaccount.com"},
		},
		Auth: config.AuthConfig{
			GoogleClientID:     "client-id",
			GoogleClientSecret: "client-secret",
			AllowedEmails:      []string{"tester@example.com"},
		},
	}

	authHandler, err := session.New(session.Config{
		ClientID:      cfg.Auth.GoogleClientID,
		ClientSecret:  cfg.Auth.GoogleClientSecret,
		ServiceURL:    cfg.Server.ServiceURL,
		Store:         session.NewMemoryStore(),
		SessionName:   "test-session",
		AllowedEmails: cfg.Auth.AllowedEmails,
	})
	if err != nil {
		t.Fatalf("failed to create auth handler: %v", err)
	}

	webHandler, err := handlers.NewHandler(handlers.Deps{Config: cfg, TaskEnqueuer: noopTaskEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create web handler: %v", err)
	}

	workerHandler := worker.NewHandler[domain.ReviewRequest](noopPipeline{})

	// Without both the audience and the allowed service accounts, verification always fails.
	appHandlers := &builder.AppHandlers{
		Auth:     authHandler,
		Web:      webHandler,
		Worker:   workerHandler,
		TaskAuth: mustOIDC(t, cfg.Tasks.TaskAudienceURL, cfg.Tasks.AllowedServiceAccounts),
	}
	return NewRouter(appHandlers, "")
}

func TestNewRouter_RouteReachabilityAndGuards(t *testing.T) {
	r := newRouterForTest(t)

	tests := []struct {
		name         string
		method       string
		path         string
		expectedCode int
	}{
		{
			name:         "auth login is reachable",
			method:       http.MethodGet,
			path:         "/auth/login",
			expectedCode: http.StatusTemporaryRedirect,
		},
		{
			name:         "root requires auth",
			method:       http.MethodGet,
			path:         "/",
			expectedCode: http.StatusFound,
		},
		{
			name:         "worker route requires oidc",
			method:       http.MethodPost,
			path:         domain.WorkerTaskPath,
			expectedCode: http.StatusUnauthorized,
		},
		{
			name:         "unknown route is 404",
			method:       http.MethodGet,
			path:         "/not-found",
			expectedCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.expectedCode {
				t.Fatalf("unexpected status for %s %s: got %d, want %d", tt.method, tt.path, w.Code, tt.expectedCode)
			}
		})
	}
}

// On a GET request, a CSRF token is generated when the session has none and reaches the
// template through the context.
//
// If this breaks, the form gets no token and every submit is rejected. The middleware is
// gcp-kit's, but whether the handlers read it with the same key (whether the delegation in
// context.go works) has to be checked here.
func TestCSRFAutoGenPopulatesContextOnGet(t *testing.T) {
	t.Parallel()

	var token string
	authHandler := newTestAuthHandler(t)
	handler := auth.Require(authHandler)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		token = handlers.CSRFTokenFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(loggedInCookie(t, authHandler))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if token == "" {
		t.Fatal("no CSRF token was generated on a GET request")
	}
}

// A POST does not generate a CSRF token.
// Generating one would hand a valid token to a request that had none, making CSRF verification
// meaningless.
func TestCSRFAutoGenSkipsPost(t *testing.T) {
	t.Parallel()

	var token string
	authHandler := newTestAuthHandler(t)
	handler := auth.Require(authHandler)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		token = handlers.CSRFTokenFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/jobs", nil)
	req.AddCookie(loggedInCookie(t, authHandler))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if token != "" {
		t.Fatalf("a CSRF token was generated on POST: %q", token)
	}
}

// When an authenticated form is rendered, the context's token is actually embedded.
func TestFormRendersCSRFTokenFromMiddleware(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Server: config.ServerConfig{ServiceURL: "https://service.example.com"},
		AI:     config.AIConfig{GeminiModels: []string{"gemini-2.5-flash"}},
	}
	webHandler, err := handlers.NewHandler(handlers.Deps{
		Config: cfg, TaskEnqueuer: noopTaskEnqueuer{},
	})
	if err != nil {
		t.Fatalf("failed to create web handler: %v", err)
	}

	authHandler := newTestAuthHandler(t)
	handler := auth.Require(authHandler)(http.HandlerFunc(webHandler.HandleForm))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(loggedInCookie(t, authHandler))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !regexp.MustCompile(`name="csrf_token" value="[^"]+"`).MatchString(w.Body.String()) {
		t.Fatalf("the form has no CSRF token: %s", w.Body.String())
	}
}

// Static files are served outside authentication.
// Inside it, the login page would get no styles.
func TestStaticFilesNeedNoAuth(t *testing.T) {
	t.Parallel()

	r := newRouterForTest(t)

	for _, path := range []string{"/static/css/app.css", "/static/js/app.js"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if w.Header().Get("Cache-Control") == "" {
				t.Error("Cache-Control is not set")
			}
		})
	}
}

// Deletion sits inside authentication. It must not work unauthenticated.
func TestDeleteRequiresAuth(t *testing.T) {
	t.Parallel()

	r := newRouterForTest(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/jobs/20260810-213000-a1b2c3d4", nil))

	if w.Code == http.StatusNoContent {
		t.Fatal("an unauthenticated deletion passed")
	}
}

// Every response carries the CSP, and script-src is not loosened.
func TestResponsesCarryContentSecurityPolicy(t *testing.T) {
	t.Parallel()

	router := newRouterForTest(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	policy := rec.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("Content-Security-Policy is missing")
	}
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("the CSP lacks %q: %s", want, policy)
		}
	}
	// 'unsafe-inline' in script-src would undo the point of moving the inline script out of
	// review_form.html (its counterpart is TestTemplatesHaveNoInlineScripts in assets).
	scriptSrc := cspDirective(policy, "script-src")
	if scriptSrc == "" {
		t.Fatalf("script-src is missing: %s", policy)
	}
	if strings.Contains(scriptSrc, "unsafe-inline") || strings.Contains(scriptSrc, "unsafe-eval") {
		t.Errorf("script-src is loosened: %s", scriptSrc)
	}
}

// cspDirective extracts one directive from the CSP. It returns an empty string when absent.
func cspDirective(policy, name string) string {
	for directive := range strings.SplitSeq(policy, ";") {
		directive = strings.TrimSpace(directive)
		if after, ok := strings.CutPrefix(directive, name+" "); ok {
			return after
		}
	}
	return ""
}

// Compression applies. The screens are Japanese UTF-8 (3 bytes per character) and shrink well,
// yet they used to be served uncompressed.
func TestCompressibleResponsesAreCompressed(t *testing.T) {
	t.Parallel()

	router := newRouterForTest(t)
	req := httptest.NewRequest(http.MethodGet, "/static/vendor/bootstrap-5.3.8/bootstrap.min.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}

	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer func() { _ = reader.Close() }()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("cannot decompress: %v", err)
	}
	if !strings.Contains(string(body), "Bootstrap") {
		t.Error("the decompressed content is not Bootstrap's CSS")
	}
	if len(body) <= rec.Body.Len() {
		t.Errorf("compressed %d bytes is not below the original %d bytes", rec.Body.Len(), len(body))
	}
}

// Every response carries the other protective headers besides the CSP too. Each takes one line,
// yet a missing one goes unnoticed because the screens look normal.
func TestResponsesCarrySecurityHeaders(t *testing.T) {
	router := newRouterForTest(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	// autoplay is not blocked (media playback would break).
	policy := rec.Header().Get("Permissions-Policy")
	if policy == "" {
		t.Error("Permissions-Policy is missing")
	}
	if strings.Contains(policy, "autoplay") {
		t.Errorf("Permissions-Policy blocks autoplay: %s", policy)
	}
}

// loggedInCookie creates a logged-in session with h and returns its cookie.
//
// CSRF tokens are issued after authentication, so the logged-in state is created without going
// through the OAuth callback. h itself bakes the cookie. A hand-built store could not read it,
// because the session data lives on h's side.
func loggedInCookie(t *testing.T, h *session.Handler) *http.Cookie {
	t.Helper()

	rec := httptest.NewRecorder()
	if err := h.IssueSession(rec, httptest.NewRequest(http.MethodGet, "/", nil), "tester@example.com"); err != nil {
		t.Fatalf("IssueSession() error = %v", err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie was created")
	}
	return cookies[0]
}

// mustOIDC creates a configured verifier for tests (New returns an error when settings are missing).
func mustOIDC(t *testing.T, audience string, allowed []string) *oidc.Verifier {
	t.Helper()
	v, err := oidc.New(audience, allowed)
	if err != nil {
		t.Fatalf("oidc.New() error = %v", err)
	}
	return v
}
