package adapters

import (
	"context"
	"testing"

	"github.com/shouni/go-review-kit/pipeline"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/assets"
)

// markerReviewer は、どちらのパイプラインが動いたかを見分けるためのレビュアーです。
type markerReviewer struct {
	title  string
	called *bool
}

func (m markerReviewer) Review(context.Context, string, string) (review.Report, error) {
	*m.called = true
	return review.Report{
		Title:   m.title,
		Verdict: review.Verdict{Decision: review.DecisionNone, Reason: "ok"},
	}, nil
}

func newMarkerPipeline(t *testing.T, title string, called *bool) *pipeline.Pipeline {
	t.Helper()

	p, err := pipeline.New(pipeline.Deps{
		Sources:   stubFactory{source: stubSource{}},
		Prompts:   stubPrompts{},
		Reviewer:  markerReviewer{title: title, called: called},
		Publisher: stubPublisher{},
		Notifier:  &stubNotifier{},
	})
	if err != nil {
		t.Fatalf("パイプラインの構築に失敗: %v", err)
	}
	return p
}

// newTestRouter は、単発側・エージェント側それぞれの実行を記録するルーターを返します。
// エージェント側も単発レビュアーで代用します（見たいのは経路の選択だけです）。
func newTestRouter(t *testing.T) (router *EngineRouter, singleCalled, agentCalled *bool) {
	t.Helper()

	singleCalled, agentCalled = new(bool), new(bool)
	return NewEngineRouter(
		newMarkerPipeline(t, "single", singleCalled),
		newMarkerPipeline(t, "agent", agentCalled),
	), singleCalled, agentCalled
}

func TestEngineRouterUsesModeDefault(t *testing.T) {
	router, singleCalled, agentCalled := newTestRouter(t)

	req := testDomainRequest()
	req.Mode = "code" // front matter で agent を宣言しているモード
	req.Engine = ""

	if _, _, err := router.Run(context.Background(), req); err != nil {
		t.Fatalf("Run が失敗しました: %v", err)
	}
	if !*agentCalled || *singleCalled {
		t.Errorf("モードの既定が使われていません: agent=%v single=%v", *agentCalled, *singleCalled)
	}
}

func TestEngineRouterHonorsOverride(t *testing.T) {
	tests := []struct {
		name       string
		override   string
		wantAgent  bool
		wantSingle bool
	}{
		{name: "単発へ倒す", override: string(assets.EngineSingle), wantSingle: true},
		{name: "エージェントを明示", override: string(assets.EngineAgent), wantAgent: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, singleCalled, agentCalled := newTestRouter(t)

			req := testDomainRequest()
			req.Mode = "code"
			req.Engine = tt.override

			if _, _, err := router.Run(context.Background(), req); err != nil {
				t.Fatalf("Run が失敗しました: %v", err)
			}
			if *singleCalled != tt.wantSingle || *agentCalled != tt.wantAgent {
				t.Errorf("経路が違います: single=%v agent=%v", *singleCalled, *agentCalled)
			}
		})
	}
}

func TestEngineRouterRejectsUnknownEngine(t *testing.T) {
	router, singleCalled, agentCalled := newTestRouter(t)

	req := testDomainRequest()
	req.Mode = "code"
	req.Engine = "turbo"

	result, _, err := router.Run(context.Background(), req)
	if err == nil {
		t.Fatal("未知のエンジンがエラーになりません")
	}
	if got := review.StepOf(err); got != review.StepValidate {
		t.Errorf("StepOf = %q, want %q", got, review.StepValidate)
	}
	if result.Status != review.StatusFailed {
		t.Errorf("Status = %q, want %q", result.Status, review.StatusFailed)
	}
	if *singleCalled || *agentCalled {
		t.Error("解決に失敗したのにレビューが実行されています")
	}
}
