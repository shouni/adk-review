package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-review-kit/review"
)

const contentTypeJSON = "application/json; charset=utf-8"

// ReportPublisher は、レビュー結果全文を report.json として保存する review.Publisher です。
//
// HTML へ整形せず JSON のまま置くのは、表示を詳細画面（/history/{jobID}）が受け持つ
// ためです。整形済みの HTML も置くと、同じ内容の見た目が 2 系統に分かれるうえ、
// 署名付き URL 経由でアプリの認証を迂回して読めてしまいます。
type ReportPublisher struct {
	writer remoteio.Writer
}

var _ review.Publisher = (*ReportPublisher)(nil)

// NewReportPublisher は ReportPublisher を構築します。
func NewReportPublisher(writer remoteio.Writer) (*ReportPublisher, error) {
	if writer == nil {
		return nil, fmt.Errorf("writer が nil です")
	}
	return &ReportPublisher{writer: writer}, nil
}

// Publish は、レビュー結果を report.json として保存します。
//
// 保存先は Request.StorageURI です。ジョブ ID から組み立て直さないのは、投入時に
// 決めた場所と食い違わないようにするためです（採番と配置は投入側の責務です）。
func (p *ReportPublisher) Publish(ctx context.Context, req review.Request, report review.Report) error {
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("レビュー結果のJSON化に失敗しました: %w", err)
	}

	slog.InfoContext(ctx, "レビュー結果を保存します", "uri", req.StorageURI)

	if err := p.writer.Write(ctx, req.StorageURI, bytes.NewReader(body),
		remoteio.WithContentType(contentTypeJSON)); err != nil {
		return fmt.Errorf("レビュー結果の保存に失敗しました (%s): %w", req.StorageURI, err)
	}
	return nil
}
