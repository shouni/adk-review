// Package handlers は、Web UI（フォーム表示・履歴閲覧等）のHTTPハンドラーを提供します。
package handlers

import (
	"context"

	"github.com/shouni/gcp-kit/auth"
)

// CSRFTokenFromContext は、コンテキストに保存された CSRF トークンを取得します。
func CSRFTokenFromContext(ctx context.Context) string {
	return auth.CSRFTokenFromContext(ctx)
}

// WithCSRFToken は、コンテキストに CSRF トークンを載せます。
// 実運用では上記ミドルウェアが行うため、テストから任意の値を載せる用途で使います。
func WithCSRFToken(ctx context.Context, token string) context.Context {
	return auth.WithCSRFToken(ctx, token)
}
