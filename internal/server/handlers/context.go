// Package handlers は、Web UI（フォーム表示・履歴閲覧等）の HTTP ハンドラーを提供します。
//
// 外から受け取ったジョブ ID は、使う前に必ず jobid.Sanitize を通します。ID は
// ストレージのパス要素になるため、traversal を含んだ値をそのまま渡すとバケット内の
// 別のプレフィックスを指せます。各ハンドラーの入口にある Sanitize はこの決まりで、
// 個別の判断ではありません。
package handlers

import (
	"context"

	"github.com/shouni/gcp-kit/auth/session"
)

// CSRFTokenFromContext は、コンテキストに保存された CSRF トークンを取得します。
func CSRFTokenFromContext(ctx context.Context) string {
	return session.CSRFTokenFromContext(ctx)
}
