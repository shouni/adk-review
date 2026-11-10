// Package handlers provides the HTTP handlers of the web UI (the form, browsing the history and so on).
//
// A job ID received from outside always goes through jobid.Sanitize before use. The ID becomes
// a storage path element, so a value containing traversal, passed as is, could point at another
// prefix in the bucket. The Sanitize at each handler's entrance follows this rule; it is not a
// case-by-case decision.
package handlers

import (
	"context"

	"github.com/shouni/gcp-kit/auth/session"
)

// CSRFTokenFromContext returns the CSRF token stored in the context.
func CSRFTokenFromContext(ctx context.Context) string {
	return session.CSRFTokenFromContext(ctx)
}
