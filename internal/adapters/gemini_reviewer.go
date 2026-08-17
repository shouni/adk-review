package adapters

import (
	"context"
	"fmt"
	"strings"

	geminiclient "github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-review-kit/review"
)

// GeminiReviewer は、Gemini に構造化出力でレビューを依頼する単発（差分のみ）の
// review.Reviewer 実装です。
//
// もとは go-review-kit の gemini パッケージでしたが、v1.3.0 で kit から
// go-gemini-client 依存を切り離すためにこちらへ移しました。エージェント版
// （internal/adkagent）と同じく、レビュアーの実体はアプリ側が持ちます。
type GeminiReviewer struct {
	client geminiclient.Generator
}

// 実装がポートを満たすことをコンパイル時に確認します。
var _ review.Reviewer = (*GeminiReviewer)(nil)

// NewReviewer は、構築済みのクライアントから単発レビュアーを生成します。
// 認証・リトライのポリシーはクライアント側（builder が組む共有クライアント）に集約します。
func NewReviewer(client geminiclient.Generator) (*GeminiReviewer, error) {
	if client == nil {
		return nil, fmt.Errorf("gemini: クライアントが nil です")
	}
	return &GeminiReviewer{client: client}, nil
}

// Review は、プロンプトを Gemini へ送り、レビュー結果を review.Report として返します。
//
// ResponseSchema で出力を文法レベルで制約するため、自由記述の Markdown に起因する
// 出力の揺れ（見出しレベルのズレ、コードフェンスの崩れなど）が起きません。デコードは
// ここで一度だけ行い、以降の層へは型の付いた Report を渡します。
func (r *GeminiReviewer) Review(ctx context.Context, model, prompt string) (review.Report, error) {
	if strings.TrimSpace(model) == "" {
		return review.Report{}, fmt.Errorf("gemini: モデル名が空です")
	}
	if strings.TrimSpace(prompt) == "" {
		return review.Report{}, fmt.Errorf("gemini: プロンプトが空です")
	}

	// 添付は無いが、プロンプトと生成オプションを同時に渡せる入口はこちらです。
	// GenerateWithParts を使うと Part を組み立てるためだけに genai SDK を import する
	// ことになるため使いません（SDK 直接使用の例外は internal/adkagent だけに閉じます）。
	resp, err := r.client.GenerateWithAttachments(ctx, model, prompt, nil, geminiclient.GenerateOptions{
		ResponseMIMEType: "application/json",
		ResponseSchema:   reportSchema(),
	})
	if err != nil {
		return review.Report{}, fmt.Errorf("gemini: API呼び出しに失敗しました (model: %s): %w", model, err)
	}
	if resp == nil {
		return review.Report{}, review.ErrEmptyResponse
	}

	report, err := review.ParseReport([]byte(resp.Text))
	if err != nil {
		return review.Report{}, fmt.Errorf("gemini: %w", err)
	}
	return report, nil
}
