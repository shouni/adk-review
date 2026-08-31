// Package server は、HTTPルーティングとミドルウェアを構成します。
package server

import (
	"bytes"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/gcp-kit/cloudrun"
	"github.com/shouni/go-serve-kit/secureheaders"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/builder"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/gcp-kit/auth"
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
		w.Header().Set("Cache-Control", cacheControlFor(r.URL.Path))
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
	// 画面は日本語 UTF-8（1 文字 3 バイト）なので圧縮がよく効きます。静的ファイルも
	// 同じ経路に乗ります（vendor は immutable なので再圧縮は稀です）。
	r.Use(middleware.Compress(compressionLevel))
	r.Use(secureheaders.Middleware(secureheaders.Config{
		// Bootstrap の JS が遷移中にインラインスタイルを当てるため。
		AllowInlineStyle: true,
	}))
}

// setupRoutes は、各コンポーネントのハンドラーをルーティングに登録します。
func setupRoutes(r chi.Router, h *builder.AppHandlers) {
	// --- 1. 公開ルート (ヘルスチェック) ---
	// "/healthz" は Cloud Run のデフォルトドメイン (*.run.app) 側で予約パス的に扱われ、
	// コンテナまでリクエストが届かず GFE の汎用 404 に置き換えられるため使わない。
	// パスの選択理由（"/healthz" を使わない）は cloudrun.HealthPath を参照。
	r.Get(cloudrun.HealthPath, cloudrun.Health)
	setupStaticRoutes(r)

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

		// ブラウザはセッション + CSRF、他サービス（ap-mcp）は OIDC Bearer で通ります。
		// 合成をライブラリに任せるのは、経路ごとに自前で組むと片方だけ強化されて
		// ドリフトするためです。Bearer 経路が CSRF を通らないのは、CSRF が
		// クッキーの自動送出を悪用する攻撃への対策で、明示的にトークンを付ける
		// サーバー間呼び出しには当てはまらないからです（絞りは許可リストが担います）。
		//
		// セッション経路では Authenticate が CSRF の検証と発行もまとめて行います。
		r.Use(auth.Protected(h.M2M, h.Auth))

		// ヘッダーを持たないリクエスト（Sec-Fetch-Site も Origin も無い）は
		// 非ブラウザとみなされて通ります。M2M クライアントはここを素通りします。
		r.Use(crossOriginProtection.Handler)

		r.Get("/", h.Web.HandleReviewForm)
		r.Post("/submit_review", h.Web.HandleReviewSubmit)
		r.Get("/modes", h.Web.HandleModes)
		r.Get("/jobs/{jobID}", h.Web.HandleJobStatus)
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

		r.Use(auth.Require(h.TaskAuth))

		if h.Worker != nil {
			r.Post(domain.WorkerTaskPath, h.Worker.ProcessTask)
		}
	})
}

// crossOriginErrorHandler は、越境送信として弾いたリクエストに返すハンドラーです。
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

// writeCrossOriginErrorResponse は、越境送信を弾いたことを 403 で返します。
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

// compressionLevel は gzip の圧縮レベルです。
const compressionLevel = 5

// vendorPathPrefix より下は第三者製の配布物で、パスにバージョンが入っています
// （assets/static/vendor/bootstrap-5.3.8 など）。更新すれば必ず別の URL になるので、
// 再検証させる理由がありません。
const vendorPathPrefix = "/static/vendor/"

const (
	// ownAssetCacheControl は自前の CSS / JS 用です。URL を変えずに中身が変わるため短命にします。
	ownAssetCacheControl = "public, max-age=300, must-revalidate"
	// vendorCacheControl は vendorPathPrefix 配下用です。
	vendorCacheControl = "public, max-age=31536000, immutable"
)

// cacheControlFor は、静的ファイルのパスに応じた Cache-Control を返します。
//
// //go:embed した FileServer は Last-Modified も ETag も出せない（embed の ModTime が
// ゼロ値のため net/http が両方を省く）ので、期限が切れた時点で必ず全体を取り直します。
// バージョン付きの vendor を分けているのは、その再取得を無くすためです。
func cacheControlFor(path string) string {
	if strings.HasPrefix(path, vendorPathPrefix) {
		return vendorCacheControl
	}
	return ownAssetCacheControl
}
