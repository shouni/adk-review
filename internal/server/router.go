// Package server configures HTTP routing and middleware.
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

// compressionLevel is the gzip compression level.
const compressionLevel = 5

// NewRouter builds the http.Handler combining middleware and routing.
//
// projectID is used only for Cloud Logging trace correlation; empty means no correlation.
func NewRouter(h *builder.AppHandlers, projectID string) http.Handler {
	r := chi.NewRouter()
	registerMiddleware(r, projectID)
	registerRoutes(r, h)

	return r
}

// registerMiddleware configures the standard middleware.
func registerMiddleware(r *chi.Mux, projectID string) {
	// Trace correlation must apply before logging, so it is registered first. Without it the Cloud
	// Run request logs and the app logs are not linked as parent and child, and one review spanning
	// two services (web -> Cloud Tasks -> worker) cannot be followed.
	r.Use(cloudlog.TraceMiddleware(projectID))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)
	// The screens are Japanese UTF-8 (3 bytes per character), so compression works well. Static
	// files take the same path (vendor is immutable, so recompression is rare).
	r.Use(middleware.Compress(compressionLevel))
	r.Use(secureheaders.Middleware(secureheaders.Config{
		// Because Bootstrap's JS applies inline styles during transitions.
		AllowInlineStyle: true,
	}))
}

// registerRoutes registers each component's handlers with the router.
func registerRoutes(r chi.Router, h *builder.AppHandlers) {
	// --- 1. Public route (health check) ---
	// For why this path (not "/healthz"), see cloudrun.HealthPath.
	r.Get(cloudrun.HealthPath, cloudrun.Health)
	registerStaticRoutes(r)

	if h == nil {
		slog.Warn("AppHandlers is nil, skipping application routes registration")
		return
	}

	// A. Public routes (login, needing neither authentication nor CSRF). The worker role has no
	// Auth at all, so they are not registered there.
	if h.Auth != nil {
		r.Handle("/auth/*", h.Auth.Routes()) // login / callback / logout
	}

	// B. Routes requiring authentication (web UI)
	r.Group(func(r chi.Router) {
		if h.Auth == nil {
			if h.Web != nil {
				slog.Error("Auth handler is nil, skipping protected web routes")
			}
			return
		}

		// Browsers (session + CSRF) and other services (OIDC Bearer) call the same routes.
		// Combining the two is left to the library because, built by hand per path, only one
		// would get hardened and they would drift. For the order of checks and CSRF handling,
		// see auth.Protected.
		r.Use(auth.Protected(h.M2M, h.Auth))

		r.Get("/", h.Web.HandleForm)
		r.Get("/modes", h.Web.HandleModes)

		// The job is the only primary resource. The same /jobs/{jobID} names it from submission
		// to deletion, and the history is a list view of finished jobs (the public-docs URL
		// naming convention).
		r.Route("/jobs", func(r chi.Router) {
			r.Post("/", h.Web.HandleJobCreate)
			r.Get("/", h.Web.HandleJobList)
			r.Get("/{jobID}", h.Web.HandleJob)
			r.Get("/{jobID}/report", h.Web.HandleJobReport)
			r.Delete("/{jobID}", h.Web.HandleJobDelete)
		})

	})

	// C. Worker-only route (OIDC authentication)
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

// registerStaticRoutes serves the embedded static files at /static/*.
// go-serve-kit's staticfiles owns the Cache-Control decision (short-lived for our own files,
// immutable for vendor) and suppressing directory listings.
//
// It sits outside authentication. There is no reason to require login for a stylesheet, and the
// login page, shown unauthenticated, references them too.
func registerStaticRoutes(r chi.Router) {
	files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
	if err != nil {
		// This would be a mix-up of embedded constants, so it stops before accepting requests.
		panic(fmt.Sprintf("static assets: %v", err))
	}
	r.Handle("/static/*", files)
}
