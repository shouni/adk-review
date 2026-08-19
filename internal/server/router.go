// Package server は、HTTPルーティングとミドルウェアを構成します。
package server

import (
	"bytes"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/cloudlog"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/builder"
	"github.com/shouni/adk-review/internal/domain"
)

const (
	layoutTemplatePath           = "templates/layout.html"
	crossOriginErrorTemplatePath = "templates/cross_origin_error.html"
)

// NewRouter は、ミドルウェアとルーティングを統合した http.Handler を構築します。
//
// projectID は Cloud Logging のトレース相関にのみ使用し、空なら相関を行いません。
func NewRouter(h *builder.AppHandlers, projectID string) http.Handler {
	r := chi.NewRouter()
	setupCommonMiddleware(r, projectID)
	setupStaticRoutes(r)
	setupRoutes(r, h)

	return r
}

// setupStaticRoutes は、埋め込んだ静的ファイルを /static/ で配信します。
//
// 認証の外側に置きます。CSS/JS に秘密は含まれず、認証の内側に入れるとログイン画面で
// スタイルが当たらなくなるためです。
func setupStaticRoutes(r chi.Router) {
	staticFS, err := fs.Sub(assets.StaticFiles, "static")
	if err != nil {
		slog.Error("static assets are unavailable", "error", err)
		return
	}

	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
		fileServer.ServeHTTP(w, r)
	}))
}

// setupCommonMiddleware は、標準的なミドルウェアを構成します。
func setupCommonMiddleware(r *chi.Mux, projectID string) {
	// トレース相関はログ出力より先に効かせる必要があるため最初に登録します。
	// これが無いと Cloud Run のリクエストログとアプリログが親子で紐付かず、
	// web → Cloud Tasks → worker と 2 サービスにまたがる 1 レビューを追えません。
	r.Use(cloudlog.TraceMiddleware(projectID))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)
}

// setupRoutes は、各コンポーネントのハンドラーをルーティングに登録します。
func setupRoutes(r chi.Router, h *builder.AppHandlers) {
	// --- 1. 公開ルート (ヘルスチェック) ---
	// "/healthz" は Cloud Run のデフォルトドメイン (*.run.app) 側で予約パス的に扱われ、
	// コンテナまでリクエストが届かず GFE の汎用 404 に置き換えられるため使わない。
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	if h == nil {
		slog.Warn("AppHandlers is nil, skipping application routes registration")
		return
	}

	// 同一オリジンのブラウザ送信だけを許可する。
	crossOriginProtection := http.NewCrossOriginProtection()
	crossOriginProtection.SetDenyHandler(crossOriginErrorHandler())

	// A. 公開ルート（認証もCSRFも不要なログイン周り）。worker 面では Auth ごと無いため
	// 登録しません。
	if h.Auth != nil {
		r.Route("/auth", func(r chi.Router) {
			r.Get("/login", h.Auth.Login)
			r.Get("/callback", h.Auth.Callback)
		})
	}

	// B. 認証が必要なルート (Web UI)
	r.Group(func(r chi.Router) {
		if h.Auth == nil {
			if h.Web != nil {
				slog.Error("Auth handler is nil, skipping protected web routes")
			}
			return
		}

		r.Use(h.Auth.Middleware)

		// GET でセッションに CSRF トークンが無ければ自動生成し、context へ載せます。
		// POST では生成しません（生成すると、トークンを持たないリクエストに正当な
		// トークンを与えることになり、CSRF 検証が意味をなさなくなります）。
		r.Use(h.Auth.CSRFContextMiddleware)

		r.Use(crossOriginProtection.Handler)

		r.Get("/", h.Web.HandleReviewForm)
		r.Post("/submit_review", h.Web.HandleReviewSubmit)
		r.Get("/history", h.Web.HandleHistory)
		r.Get("/history/{jobID}", h.Web.HandleReviewDetail)
		r.Delete("/history/{jobID}", h.Web.HandleReviewDelete)
	})

	// C. ワーカー専用ルート (OIDC認証)
	r.Group(func(r chi.Router) {
		if h.TaskAuth == nil {
			if h.Worker != nil {
				slog.Error("Task verifier is nil, skipping worker routes")
			}
			return
		}

		r.Use(h.TaskAuth.Middleware)

		if h.Worker != nil {
			r.Post(domain.TaskExecuteReviewPath, h.Worker.ProcessTask)
		}
	})
}

// crossOriginErrorHandler returns a handler for requests blocked by cross-origin protection.
func crossOriginErrorHandler() http.Handler {
	tmpl := template.Must(template.ParseFS(
		assets.Templates,
		layoutTemplatePath,
		crossOriginErrorTemplatePath,
	))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCrossOriginErrorResponse(w, r, tmpl)
	})
}

// writeCrossOriginErrorResponse renders a forbidden response for blocked cross-origin requests.
func writeCrossOriginErrorResponse(w http.ResponseWriter, r *http.Request, tmpl *template.Template) {
	slog.WarnContext(r.Context(), "cross-origin request blocked", "method", r.Method, "path", r.URL.Path)

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", nil); err != nil {
		slog.ErrorContext(r.Context(), "failed to render cross-origin error template", "error", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("送信元を確認できなかったため、リクエストをブロックしました。ページを開き直して再送信してください。"))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = buf.WriteTo(w)
}
