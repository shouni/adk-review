package handlers

import (
	"context"

	"github.com/shouni/gcp-kit/auth/session"
)

// withCSRFToken は、コンテキストに CSRF トークンを載せます。
//
// 実運用ではミドルウェアが行うため、本番の API には出しません。
// テストから任意の値を載せるためだけのヘルパーです。
func withCSRFToken(ctx context.Context, token string) context.Context {
	return session.WithCSRFToken(ctx, token)
}
