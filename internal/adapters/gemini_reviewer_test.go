package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	geminiclient "github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/go-review-kit/review"
)

const validReportJSON = `{
	"title": "レビュー結果",
	"summary": "概ね良好です。",
	"verdict": {"decision": "Minor", "reason": "軽微な指摘が1件"},
	"findings": [
		{"severity": "Minor", "file": "main.go", "line": 12, "excerpt": "x := 1", "message": "未使用です。"}
	]
}`

// fakeGenerator は geminiclient.Generator のテスト実装です。
type fakeGenerator struct {
	text string
	resp *geminiclient.Response
	err  error

	gotModel  string
	gotPrompt string
	gotOpts   geminiclient.GenerateOptions
}

func (f *fakeGenerator) GenerateWithAttachments(
	_ context.Context,
	model string,
	prompt string,
	_ []geminiclient.Attachment,
	opts geminiclient.GenerateOptions,
) (*geminiclient.Response, error) {
	f.gotModel, f.gotPrompt, f.gotOpts = model, prompt, opts

	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &geminiclient.Response{Text: f.text}, nil
}

func newReviewerForTest(t *testing.T, generator *fakeGenerator) *GeminiReviewer {
	t.Helper()

	reviewer, err := NewGeminiReviewer(generator)
	if err != nil {
		t.Fatalf("Reviewer の生成に失敗: %v", err)
	}
	return reviewer
}

func TestNewReviewerRejectsNil(t *testing.T) {
	if _, err := NewGeminiReviewer(nil); err == nil {
		t.Fatal("エラーを期待しましたが nil でした")
	}
}

func TestReviewReturnsTypedReport(t *testing.T) {
	generator := &fakeGenerator{text: validReportJSON}
	reviewer := newReviewerForTest(t, generator)

	report, err := reviewer.Review(context.Background(), "gemini-2.5-pro", "レビューしてください")
	if err != nil {
		t.Fatalf("予期しないエラー: %v", err)
	}

	if report.Verdict.Decision != review.DecisionMinor {
		t.Errorf("decision = %q, want %q", report.Verdict.Decision, review.DecisionMinor)
	}
	if len(report.Findings) != 1 || report.Findings[0].Severity != review.SeverityMinor {
		t.Errorf("findings が期待どおりではありません: %+v", report.Findings)
	}

	if generator.gotModel != "gemini-2.5-pro" || generator.gotPrompt != "レビューしてください" {
		t.Errorf("入力が渡されていません: model=%q prompt=%q", generator.gotModel, generator.gotPrompt)
	}
}

// 構造化出力の制約を必ず付けて呼び出します。これが外れると自由記述の Markdown が
// 返り、以降のデコードが崩れます。
func TestReviewRequestsStructuredOutput(t *testing.T) {
	generator := &fakeGenerator{text: validReportJSON}
	reviewer := newReviewerForTest(t, generator)

	if _, err := reviewer.Review(context.Background(), "gemini-2.5-pro", "prompt"); err != nil {
		t.Fatalf("予期しないエラー: %v", err)
	}

	if generator.gotOpts.ResponseMIMEType != "application/json" {
		t.Errorf("ResponseMIMEType = %q", generator.gotOpts.ResponseMIMEType)
	}
	if generator.gotOpts.ResponseSchema == nil {
		t.Fatal("ResponseSchema が設定されていません")
	}
	if _, ok := generator.gotOpts.ResponseSchema.Properties["findings"]; !ok {
		t.Error("スキーマに findings がありません")
	}
}

func TestReviewErrors(t *testing.T) {
	tests := []struct {
		name      string
		generator *fakeGenerator
		model     string
		prompt    string
		wantErr   error
	}{
		{
			name:      "モデル名が空",
			generator: &fakeGenerator{text: validReportJSON},
			prompt:    "prompt",
		},
		{
			name:      "プロンプトが空",
			generator: &fakeGenerator{text: validReportJSON},
			model:     "gemini-2.5-pro",
		},
		{
			name:      "API がエラーを返す",
			generator: &fakeGenerator{err: errors.New("503 unavailable")},
			model:     "gemini-2.5-pro",
			prompt:    "prompt",
		},
		{
			name:      "空の応答",
			generator: &fakeGenerator{text: ""},
			model:     "gemini-2.5-pro",
			prompt:    "prompt",
			wantErr:   review.ErrEmptyResponse,
		},
		{
			name:      "応答自体が nil",
			generator: &fakeGenerator{resp: nil, text: ""},
			model:     "gemini-2.5-pro",
			prompt:    "prompt",
			wantErr:   review.ErrEmptyResponse,
		},
		{
			name:      "JSON として壊れている",
			generator: &fakeGenerator{text: `{"title":`},
			model:     "gemini-2.5-pro",
			prompt:    "prompt",
			wantErr:   review.ErrInvalidReport,
		},
		{
			name:      "未知の severity",
			generator: &fakeGenerator{text: `{"title":"t","summary":"s","verdict":{"decision":"None","reason":"r"},"findings":[{"severity":"Trivial","file":"a.go","excerpt":"x","message":"m"}]}`},
			model:     "gemini-2.5-pro",
			prompt:    "prompt",
			wantErr:   review.ErrInvalidReport,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reviewer := newReviewerForTest(t, tt.generator)

			_, err := reviewer.Review(context.Background(), tt.model, tt.prompt)
			if err == nil {
				t.Fatal("エラーを期待しましたが nil でした")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("%v を期待しましたが: %v", tt.wantErr, err)
			}
		})
	}
}

func TestReviewWrapsAPIError(t *testing.T) {
	cause := errors.New("503 unavailable")
	reviewer := newReviewerForTest(t, &fakeGenerator{err: cause})

	_, err := reviewer.Review(context.Background(), "gemini-2.5-pro", "prompt")
	if !errors.Is(err, cause) {
		t.Fatalf("原因まで辿れません: %v", err)
	}
	if !strings.Contains(err.Error(), "gemini-2.5-pro") {
		t.Errorf("エラーにモデル名が含まれていません: %v", err)
	}
}

// モデルが軽い順に並べて返しても、重い順で受け取れること。
//
// 画面も Slack も返ってきた順にそのまま出すため、ここで確定させないと
// Blocker が Minor の後ろに埋もれます。
func TestReviewOrdersFindingsBySeverity(t *testing.T) {
	const unsorted = `{
		"title": "レビュー結果",
		"summary": "指摘が3件あります。",
		"verdict": {"decision": "Blocker", "reason": "重大な指摘があります"},
		"findings": [
			{"severity": "Minor", "file": "a.go", "excerpt": "x := 1", "message": "軽微"},
			{"severity": "Blocker", "file": "b.go", "excerpt": "panic()", "message": "重大"},
			{"severity": "Major", "file": "c.go", "excerpt": "_ = err", "message": "中程度"}
		]
	}`

	reviewer := newReviewerForTest(t, &fakeGenerator{text: unsorted})

	report, err := reviewer.Review(context.Background(), "gemini-2.5-pro", "レビューしてください")
	if err != nil {
		t.Fatalf("予期しないエラー: %v", err)
	}

	want := []review.Severity{review.SeverityBlocker, review.SeverityMajor, review.SeverityMinor}
	if len(report.Findings) != len(want) {
		t.Fatalf("findings = %d, want %d", len(report.Findings), len(want))
	}
	for i, severity := range want {
		if report.Findings[i].Severity != severity {
			t.Errorf("findings[%d].Severity = %q, want %q", i, report.Findings[i].Severity, severity)
		}
	}
}

// 引用に生の改行が入っていても、レビューが失われないこと（補修は go-review-kit）。
// **数分かけたレビューが解析失敗で丸ごと消える形**だったため、配線を固定します。
func TestReviewRecoversFromRawNewlineInExcerpt(t *testing.T) {
	const brokenReportJSON = "{\"title\":\"レビュー結果\",\"summary\":\"要約\"," +
		"\"verdict\":{\"decision\":\"Minor\",\"reason\":\"r\"}," +
		"\"findings\":[{\"severity\":\"Minor\",\"file\":\"a.md\",\"excerpt\":\"一行目\n二行目\",\"message\":\"m\"}]}"

	reviewer := newReviewerForTest(t, &fakeGenerator{text: brokenReportJSON})

	report, err := reviewer.Review(context.Background(), "gemini-2.5-pro", "レビューしてください")
	if err != nil {
		t.Fatalf("生の改行で失敗しました: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(report.Findings))
	}
	if got := report.Findings[0].Excerpt; got != "一行目\n二行目" {
		t.Errorf("excerpt = %q, 引用の中身が変わりました", got)
	}
}
