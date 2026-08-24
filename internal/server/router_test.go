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

	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/adk-review/internal/builder"
	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/adk-review/internal/server/handlers"
)

type noopTaskEnqueuer struct{}

func (noopTaskEnqueuer) Enqueue(context.Context, domain.ReviewRequest) error { return nil }
func (noopTaskEnqueuer) Close() error                                        { return nil }

type noopPipeline struct{}

func (noopPipeline) Execute(context.Context, domain.ReviewRequest) error { return nil }

// newTestAuthHandler は、テスト用の auth.Handler を返します。
// OAuth の実際のやり取りは行わず、セッション判定と CSRF 検証だけを対象にします。
func newTestAuthHandler(t *testing.T) *auth.Handler {
	t.Helper()

	h, err := auth.NewHandler(auth.Config{
		ClientID:          "client-id",
		ClientSecret:      "client-secret",
		RedirectURL:       "https://service.example.com/auth/callback",
		SessionAuthKey:    "1234567890abcdef",
		SessionEncryptKey: "1234567890123456",
		SessionName:       "test-session",
		IsSecureCookie:    true,
		AllowedEmails:     []string{"tester@example.com"},
	})
	if err != nil {
		t.Fatalf("auth.NewHandler() error = %v", err)
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
			SessionSecret:      "1234567890abcdef",
			SessionEncryptKey:  "1234567890123456",
			AllowedEmails:      []string{"tester@example.com"},
		},
	}

	authHandler, err := auth.NewHandler(auth.Config{
		ClientID:          cfg.Auth.GoogleClientID,
		ClientSecret:      cfg.Auth.GoogleClientSecret,
		RedirectURL:       cfg.Server.ServiceURL + "/auth/callback",
		SessionAuthKey:    cfg.Auth.SessionSecret,
		SessionEncryptKey: cfg.Auth.SessionEncryptKey,
		SessionName:       "test-session",
		IsSecureCookie:    true,
		AllowedEmails:     cfg.Auth.AllowedEmails,
	})
	if err != nil {
		t.Fatalf("failed to create auth handler: %v", err)
	}

	webHandler, err := handlers.NewHandler(handlers.Deps{Config: cfg, TaskEnqueuer: noopTaskEnqueuer{}})
	if err != nil {
		t.Fatalf("failed to create web handler: %v", err)
	}

	workerHandler := worker.NewHandler[domain.ReviewRequest](noopPipeline{})

	// audience と許可サービスアカウントの両方が揃わないと検証は常に失敗します。
	appHandlers := &builder.AppHandlers{
		Auth:     authHandler,
		Web:      webHandler,
		Worker:   workerHandler,
		TaskAuth: auth.NewTaskVerifier(cfg.Tasks.TaskAudienceURL, cfg.Tasks.AllowedServiceAccounts),
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
			path:         "/tasks/execute_review",
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

// GET リクエストではセッションに CSRF トークンが無ければ自動生成され、
// context 経由でテンプレートに渡ること。
//
// これが壊れるとフォームにトークンが埋まらず、submit が全部弾かれます。
// ミドルウェアは gcp-kit の実装ですが、handlers 側が同じキーで読めているか
// （context.go の委譲が効いているか）はこちらで確かめる必要があります。
func TestCSRFAutoGenPopulatesContextOnGet(t *testing.T) {
	t.Parallel()

	var token string
	handler := newTestAuthHandler(t).CSRFContextMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		token = handlers.CSRFTokenFromContext(r.Context())
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if token == "" {
		t.Fatal("GET リクエストで CSRF トークンが自動生成されていない")
	}
}

// POST では CSRF トークンを自動生成しないこと。
// 生成してしまうと、トークンを持たないリクエストに正当なトークンを与えることになり、
// CSRF 検証が意味をなさなくなります。
func TestCSRFAutoGenSkipsPost(t *testing.T) {
	t.Parallel()

	var token string
	handler := newTestAuthHandler(t).CSRFContextMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		token = handlers.CSRFTokenFromContext(r.Context())
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/submit_review", nil))

	if token != "" {
		t.Fatalf("POST で CSRF トークンが自動生成されている: %q", token)
	}
}

// 認証済みのフォーム描画で、context のトークンが実際に埋め込まれること。
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

	handler := newTestAuthHandler(t).CSRFContextMiddleware(http.HandlerFunc(webHandler.HandleReviewForm))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !regexp.MustCompile(`name="csrf_token" value="[^"]+"`).MatchString(w.Body.String()) {
		t.Fatalf("フォームに CSRF トークンが埋まっていません: %s", w.Body.String())
	}
}

// 静的ファイルは認証の外側で配信されること。
// 認証の内側に入れると、ログイン画面でスタイルが当たりません。
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
				t.Error("Cache-Control が設定されていません")
			}
		})
	}
}

// 削除は認証の内側にあること。未認証で消せてはいけません。
func TestDeleteRequiresAuth(t *testing.T) {
	t.Parallel()

	r := newRouterForTest(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/history/20260810-213000-a1b2c3d4", nil))

	if w.Code == http.StatusNoContent {
		t.Fatal("未認証の削除が通っています")
	}
}

// バージョン付きの vendor と、URL が変わらない自前アセットで Cache-Control を分けること。
func TestStaticCacheControlSeparatesVendorFromOwnAssets(t *testing.T) {
	t.Parallel()

	router := newRouterForTest(t)

	tests := []struct {
		target string
		want   string
	}{
		{"/static/vendor/bootstrap-5.3.8/bootstrap.min.css", vendorCacheControl},
		{"/static/css/app.css", ownAssetCacheControl},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.target, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("%s = %d, want 200", tt.target, rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}

// CSP が全レスポンスに付き、script-src が緩められていないこと。
func TestResponsesCarryContentSecurityPolicy(t *testing.T) {
	t.Parallel()

	router := newRouterForTest(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	policy := rec.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("Content-Security-Policy が付いていない")
	}
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("CSP に %q が無い: %s", want, policy)
		}
	}
	// script-src の 'unsafe-inline' は、review_form.html からインラインスクリプトを
	// 外した意味を消します（assets の TestTemplatesHaveNoInlineScripts が対です）。
	scriptSrc := cspDirective(policy, "script-src")
	if scriptSrc == "" {
		t.Fatalf("script-src が無い: %s", policy)
	}
	if strings.Contains(scriptSrc, "unsafe-inline") || strings.Contains(scriptSrc, "unsafe-eval") {
		t.Errorf("script-src が緩められています: %s", scriptSrc)
	}
}

// cspDirective は CSP から 1 ディレクティブ分を取り出します。無ければ空文字を返します。
func cspDirective(policy, name string) string {
	for _, directive := range strings.Split(policy, ";") {
		directive = strings.TrimSpace(directive)
		if after, ok := strings.CutPrefix(directive, name+" "); ok {
			return after
		}
	}
	return ""
}

// 圧縮が効いていること。画面は日本語 UTF-8（1 文字 3 バイト）でよく縮むのに、
// これまで無圧縮で配信していました。
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
		t.Fatalf("解凍できない: %v", err)
	}
	if !strings.Contains(string(body), "Bootstrap") {
		t.Error("解凍した中身が Bootstrap の CSS でない")
	}
	if len(body) <= rec.Body.Len() {
		t.Errorf("圧縮後 %d バイトが元の %d バイトを下回っていない", rec.Body.Len(), len(body))
	}
}

// CSP 以外の防御ヘッダーも全レスポンスに付くこと。どれも 1 行で入る割に、
// 抜けても画面は正常に見えるため気付けません。
func TestResponsesCarrySecurityHeaders(t *testing.T) {
	router := newRouterForTest(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": hstsMaxAge,
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	// autoplay は塞ぎません（メディア再生が壊れます）。
	policy := rec.Header().Get("Permissions-Policy")
	if policy == "" {
		t.Error("Permissions-Policy が付いていない")
	}
	if strings.Contains(policy, "autoplay") {
		t.Errorf("Permissions-Policy が autoplay を塞いでいます: %s", policy)
	}
}
