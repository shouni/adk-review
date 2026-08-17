package domain

import (
	"context"

	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-review-kit/review"
)

// PageMeta は履歴一覧のページ情報です。画面と M2M クライアントの双方が
// 既存のレスポンス形式に依存しているため、go-job-kit の型をそのまま使います。
type PageMeta = paging.PageMeta

// HistoryPage は、履歴一覧の 1 ページ分です。
type HistoryPage struct {
	Items []JobStatus
	Meta  PageMeta
}

// ReviewDetail は、履歴の 1 件を詳細表示するために必要な一式です。
type ReviewDetail struct {
	Status JobStatus
	// Report はレビュー結果全文です。成果物が無い場合（実行中・失敗・スキップ）は nil です。
	Report *review.Report
}

// HistoryRepository は、履歴の一覧と 1 件の取得です。
type HistoryRepository interface {
	// List は、新しい順に page ページ目を返します。
	List(ctx context.Context, page, perPage int) (HistoryPage, error)
	// Get は、1 件分の進行状況とレビュー結果全文を返します。
	Get(ctx context.Context, jobID string) (ReviewDetail, error)
	// Delete は、1 件分のオブジェクトをすべて削除します。
	// 削除してよい状態かどうかの判断は呼び出し側（ハンドラー）が行います。
	Delete(ctx context.Context, jobID string) error
	// Invalidate は、ジョブ ID 一覧のキャッシュを捨てます。新しいジョブを投入した直後に
	// 呼ぶことで、投入したのに履歴へ現れない時間をなくします。
	Invalidate()
}

// StatusStore は、ジョブ進行状況の読み書きです。
//
// jobstatus.Store[JobStatus] がそのままこの形を満たすため、間にアダプタを挟まずに
// 渡せます（go-job-kit が推奨する形に合わせています）。
type StatusStore interface {
	Get(ctx context.Context, jobID string) (JobStatus, error)
	Save(ctx context.Context, jobID string, status JobStatus) error
}
