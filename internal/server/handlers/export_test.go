package handlers

import (
	"context"

	"github.com/shouni/gcp-kit/auth/session"
)

// withCSRFToken puts a CSRF token on the context.
//
// In production the middleware does this, so it is not part of the production API.
// It is a helper only for tests to put an arbitrary value on the context.
func withCSRFToken(ctx context.Context, token string) context.Context {
	return session.WithCSRFToken(ctx, token)
}
