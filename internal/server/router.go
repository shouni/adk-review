// Package server は、HTTPルーティングとミドルウェアを構成します。
package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/gcp-kit/cloudrun"
	"github.com/shouni/go-serve-kit/secureheaders"
	"github.com/shouni/go-serve-kit/staticfiles"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/builder"
	"github.com/shouni/adk-review/internal/domain"
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

// setupStaticRoutes は、埋め込み済みの静的ファイルを /static/* で配信します。
// Cache-Control の判断（自前は短命、vendor は不変）とディレクトリ一覧の抑止は
// go-serve-kit の staticfiles が持ちます。
//
// 認証の外側に置きます。スタイルシートにログインを求める理由が無く、
// 未認証で表示されるログイン画面からも参照されるためです。
func setupStaticRoutes(r chi.Router) {
	files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
	if err != nil {
		// 埋め込んだ定数の取り違えなので、リクエストを受ける前に止めます。
		panic(fmt.Sprintf("static assets: %v", err))
	}
	r.Handle("/static/*", files)
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

	// A. 公開ルート（認証もCSRFも不要なログイン周り）。worker 面では Auth ごと無いため
	// 登録しません。
	if h.Auth != nil {
		r.Handle("/auth/*", h.Auth.Routes()) // login / callback / logout
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

		r.Get("/", h.Web.HandleForm)
		r.Get("/modes", h.Web.HandleModes)

		// ジョブが唯一の主リソースです。投入から削除まで同じ /jobs/{jobID} で指し、
		// 履歴は完了したジョブの一覧ビューです（public-docs の URL 命名規約）。
		r.Route("/jobs", func(r chi.Router) {
			r.Post("/", h.Web.HandleJobCreate)
			r.Get("/", h.Web.HandleJobList)
			r.Get("/{jobID}", h.Web.HandleJob)
			r.Get("/{jobID}/report", h.Web.HandleJobReport)
			r.Delete("/{jobID}", h.Web.HandleJobDelete)
		})

		// 旧パス。MCP サーバーが /jobs 配下へ切り替わり、そのデプロイが済んだら消します。
		// GET /history/{jobID} だけは JSON の形も旧いままです（HandleLegacyReviewDetail の項）。
		r.Post("/submit_review", h.Web.HandleJobCreate)
		r.Get("/history", h.Web.HandleJobList)
		r.Get("/history/{jobID}", h.Web.HandleLegacyReviewDetail)
		r.Delete("/history/{jobID}", h.Web.HandleJobDelete)
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
			r.Post(domain.LegacyWorkerTaskPath, h.Worker.ProcessTask)
		}
	})
}

// compressionLevel は gzip の圧縮レベルです。
const compressionLevel = 5
