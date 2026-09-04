// Package domain は、レビューワークフローが扱う中心的なドメインモデルと
// インターフェース（ポート）を定義します。
package domain

import (
	"context"
)

// Pipeline は、レビュー要求 1 件を最後まで処理する実行面です。
//
// 実体は internal/pipeline の Runner（go-review-kit のパイプラインの ACL）で、
// worker 面だけが持ちます。
type Pipeline interface {
	Execute(ctx context.Context, payload ReviewRequest) error
}
