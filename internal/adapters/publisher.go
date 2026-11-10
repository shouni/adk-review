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

// ReportPublisher is a review.Publisher that saves the full review result as report.json.
//
// It is stored as JSON rather than rendered to HTML because the detail page (/jobs/{jobID})
// handles display. Storing rendered HTML as well would split the look of the same content
// into two, and it could be read through a signed URL, bypassing the app's authentication.
type ReportPublisher struct {
	writer remoteio.Writer
}

var _ review.Publisher = (*ReportPublisher)(nil)

// NewReportPublisher builds a ReportPublisher.
func NewReportPublisher(writer remoteio.Writer) (*ReportPublisher, error) {
	if writer == nil {
		return nil, fmt.Errorf("writer is nil")
	}
	return &ReportPublisher{writer: writer}, nil
}

// Publish saves the review result as report.json.
//
// The destination is Request.StorageURI. It is not rebuilt from the job ID so that it cannot
// disagree with the location decided at submission (minting and placement belong to the submitter).
func (p *ReportPublisher) Publish(ctx context.Context, req review.Request, report review.Report) error {
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to encode the review result as JSON: %w", err)
	}

	slog.InfoContext(ctx, "saving the review result", "uri", req.StorageURI)

	if err := p.writer.Write(ctx, req.StorageURI, bytes.NewReader(body),
		remoteio.WithContentType(contentTypeJSON)); err != nil {
		return fmt.Errorf("failed to save the review result (%s): %w", req.StorageURI, err)
	}
	return nil
}
