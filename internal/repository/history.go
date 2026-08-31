// Package repository は、GCS 上のレビュー履歴の読み取りを担います。
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/shouni/go-job-kit/cache"
	"github.com/shouni/go-job-kit/joblist"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-review-kit/review"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/adk-review/internal/domain"
)

// loadConcurrency は 1 ページ分のメタデータを取得する際の同時実行数です。
const loadConcurrency = 10

// maxReportBytes は report.json の読み取り上限です。
// AI の出力はスキーマで制約されていますが、壊れたオブジェクトを丸ごとメモリへ載せない
// ための歯止めです。
const maxReportBytes = 8 << 20 // 8 MiB

// History は、GCS 上のレビュー履歴を読み書きします。
type History struct {
	storage remoteio.Store
	store   domain.StatusStore
	layout  domain.StorageLayout
	ids     *cache.IDList
	logger  *slog.Logger
}

var _ domain.HistoryRepository = (*History)(nil)

// NewHistory は History を構築します。
func NewHistory(
	storage remoteio.Store,
	store domain.StatusStore,
	layout domain.StorageLayout,
) *History {
	return &History{
		storage: storage,
		store:   store,
		layout:  layout,
		ids:     cache.NewIDList(cache.DefaultIDListTTL),
		logger:  slog.Default().With("collection", "reviews"),
	}
}

// List は、新しい順に page ページ目を返します。
func (h *History) List(ctx context.Context, page, perPage int) (domain.HistoryPage, error) {
	jobIDs, err := h.listJobIDs(ctx)
	if err != nil {
		return domain.HistoryPage{}, fmt.Errorf("レビュー履歴の一覧取得に失敗しました: %w", err)
	}

	// LoadPage は個々の load 失敗を「その行を落として続行」で処理します。未記録の
	// ジョブには妥当ですが、読み取り障害まで同じ扱いだとジョブが一覧から消えるだけで
	// 200 が返り、障害が障害に見えません。そこで種類を見分けて拾い直します。
	var failure loadFailure

	// ID に埋め込まれた時刻で並べます。ID の辞書順に頼らないのは、採番の接頭辞が
	// 変わったり別サービス採番の ID が混ざったりすると、時刻より先に接頭辞の差が
	// 効いて日付を無視した並びになるためです。
	items, meta, err := paging.LoadPage(ctx, jobIDs, page, perPage, jobid.SortKey, failure.wrap(h.loadStatus),
		paging.WithConcurrency(loadConcurrency),
		paging.WithLogger(h.logger),
	)
	if err != nil {
		return domain.HistoryPage{}, fmt.Errorf("レビュー履歴の読み込みに失敗しました: %w", err)
	}
	if err := failure.err(); err != nil {
		return domain.HistoryPage{}, fmt.Errorf("レビュー履歴の読み込みに失敗しました: %w", err)
	}

	return domain.HistoryPage{Items: items, Meta: meta}, nil
}

// loadFailure は、並列に走る読み込みのうち最初の失敗を保持します。
//
// LoadPage は load のエラーを呼び出し元へ返さないため、こちらで捕まえます。
type loadFailure struct {
	mu    sync.Mutex
	first error
}

// wrap は、load 関数を包んで失敗を記録できるようにします。
func (f *loadFailure) wrap(
	load func(context.Context, string) (domain.JobStatus, error),
) func(context.Context, string) (domain.JobStatus, error) {
	return func(ctx context.Context, jobID string) (domain.JobStatus, error) {
		status, err := load(ctx, jobID)
		if err != nil {
			f.mu.Lock()
			if f.first == nil {
				f.first = err
			}
			f.mu.Unlock()
		}
		return status, err
	}
}

// err は、記録した最初の失敗を返します。
func (f *loadFailure) err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.first
}

// Get は、1 件分の進行状況とレビュー結果全文を返します。
func (h *History) Get(ctx context.Context, jobID string) (domain.ReviewDetail, error) {
	status, err := h.store.Get(ctx, jobID)
	if err != nil {
		// %w で包みます。呼び出し側は errors.Is で ErrNotFound（404）と
		// それ以外（500）を切り分けるため、種類は保ったまま文脈だけ足します。
		return domain.ReviewDetail{}, fmt.Errorf("進行状況の取得に失敗しました (job_id: %s): %w", jobID, err)
	}

	detail := domain.ReviewDetail{Status: status}
	if !status.HasReport() {
		// 実行中・失敗・スキップは成果物を持ちません。進行状況だけで詳細を組み立てます。
		return detail, nil
	}

	report, err := h.loadReport(ctx, status.ReportURI)
	if err != nil {
		// 結果本体が読めなくても、進行状況までは見せます。何が起きたかを画面から
		// 追えるほうが、404 を返すより手掛かりが残るためです。
		h.logger.WarnContext(ctx, "レビュー結果の読み込みに失敗しました",
			"job_id", jobID, "uri", status.ReportURI, "error", err)
		return detail, nil
	}

	detail.Report = &report
	return detail, nil
}

// Delete は、1 件分のオブジェクトをすべて削除します。
//
// ジョブのプレフィックスを走査して消すため、消す側は「そのジョブが何を作ったか」を
// 知らずに済みます。成果物の種類が増えてもここを直す必要はありません
// （進行状況も同じプレフィックス配下にあるので、まとめて消えます）。
func (h *History) Delete(ctx context.Context, jobID string) error {
	safeJobID, err := jobid.Sanitize(jobID)
	if err != nil {
		return err
	}

	if err := h.deletePrefix(ctx, h.layout.JobPrefixURI(safeJobID)); err != nil {
		return err
	}

	// 消したジョブが一覧に残らないよう、ID 一覧のキャッシュを捨てます。
	// 捨てないと、読めない ID がジョブIDだけの空行として TTL の間並びます。
	h.Invalidate()
	return nil
}

// deletePrefix はプレフィックス配下のオブジェクトをすべて削除します。
//
// プレフィックスが空、あるいは区切りで終わっていない場合は拒否します。ここを取り違えると
// バケットの広い範囲を消すことになるため、呼び出し側の組み立てを信用しません。
func (h *History) deletePrefix(ctx context.Context, prefix string) error {
	if prefix == "" || !strings.HasSuffix(prefix, "/") {
		return fmt.Errorf("範囲を限定できないプレフィックスの削除は拒否します: %q", prefix)
	}

	var uris []string
	for entry, err := range h.storage.List(ctx, prefix) {
		if err != nil {
			return fmt.Errorf("削除対象の一覧取得に失敗しました (%s): %w", prefix, err)
		}
		uris = append(uris, entry.URI)
	}

	var errs []error
	for _, uri := range uris {
		if err := h.storage.Delete(ctx, uri); err != nil {
			errs = append(errs, fmt.Errorf("%s の削除に失敗しました: %w", uri, err))
		}
	}
	return errors.Join(errs...)
}

// Invalidate は、ジョブ ID 一覧のキャッシュを捨てます。
func (h *History) Invalidate() {
	h.ids.Invalidate(h.layout.ReviewPrefixURI())
}

// loadStatus は 1 件分の進行状況を読み取ります。
//
// まだ記録が無い ID は一覧から落とさず、ジョブ ID だけの行として残します。
// 一覧から消えると、投入したはずのレビューを画面から追えなくなるためです。
//
// ★ ただしそれは ErrNotFound のときだけです。読み取り自体が失敗した場合
// （権限剥奪・ストレージ障害）まで同じ扱いにすると、一覧が「ジョブ ID だけの空行が
// 並ぶ 200 OK」になり、障害が障害に見えなくなります。store は両者を別のエラーとして
// 返すので、後者は呼び出し元へ持ち上げます。
func (h *History) loadStatus(ctx context.Context, jobID string) (domain.JobStatus, error) {
	status, err := h.store.Get(ctx, jobID)
	switch {
	case err == nil:
		return status, nil
	case errors.Is(err, jobstatus.ErrNotFound):
		return domain.JobStatus{JobID: jobID}, nil
	default:
		h.logger.ErrorContext(ctx, "進行状況の読み込みに失敗しました", "job_id", jobID, "error", err)
		return domain.JobStatus{}, fmt.Errorf("進行状況を読み込めませんでした (job_id: %s): %w", jobID, err)
	}
}

// listJobIDs はプレフィックス直下のジョブ ID を集めます。
//
// 走査そのものは joblist.Collect が持ちます。区切り文字を指定してジョブ 1 件を
// 1 エントリとして受け取る形は兄弟アプリと共通で、この走査を集めるために
// 切り出されたのが joblist です。ここで書き直すと、重複潰しやプレフィックスの
// 末尾補正といった細部が黙って抜け落ちます。
//
// 集めた ID の絞り込みは行いません。採番より前に作られたディレクトリを一覧から
// 消すかどうかは読み込み側の判断で、ここでは判断材料を落とさずに渡します。
func (h *History) listJobIDs(ctx context.Context) ([]string, error) {
	prefix := h.layout.ReviewPrefixURI()

	return h.ids.Load(ctx, prefix, func(ctx context.Context) ([]string, error) {
		return joblist.Collect(ctx, h.storage, prefix)
	})
}

// loadReport は report.json を読み取ります。
func (h *History) loadReport(ctx context.Context, uri string) (review.Report, error) {
	rc, err := h.storage.Open(ctx, uri)
	if err != nil {
		return review.Report{}, fmt.Errorf("レビュー結果を開けませんでした: %w", err)
	}
	defer func() { _ = rc.Close() }()

	body, err := io.ReadAll(io.LimitReader(rc, maxReportBytes))
	if err != nil {
		return review.Report{}, fmt.Errorf("レビュー結果の読み取りに失敗しました: %w", err)
	}

	var report review.Report
	if err := json.Unmarshal(body, &report); err != nil {
		return review.Report{}, fmt.Errorf("レビュー結果の解釈に失敗しました: %w", err)
	}
	return report, nil
}
