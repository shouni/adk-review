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
