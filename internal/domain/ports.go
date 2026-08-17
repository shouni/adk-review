// Package domain は、レビューワークフローが扱う中心的なドメインモデルと
// インターフェース（ポート）を定義します。
package domain

import (
	"context"
)

// Pipeline は、レビュー要求を処理するために実行される一連のプロセスを表します。
type Pipeline interface {
	// Execute 指定されたコンテキスト内で指定されたレビュー要求を処理し、操作が失敗した場合はエラーを返します。
	Execute(ctx context.Context, payload ReviewRequest) error
}
