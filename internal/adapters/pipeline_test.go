package adapters

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shouni/go-review-kit/pipeline"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/domain"
)

// stubSource は差分取得元のフェイクです。Diff で受け取った context を観察できます。
type stubSource struct {
	seen func(ctx context.Context)
	diff string
	err  error
}

func (s stubSource) Diff(ctx context.Context, _, _ string) (string, error) {
	if s.seen != nil {
		s.seen(ctx)
	}
	if s.err != nil {
		return "", s.err
	}
	// 実際の差分取得と同じく、打ち切られていればエラーを返します。
	// ここで成功を返してしまうと、締切のテストが締切を再現できません。
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.diff != "" {
		return s.diff, nil
	}
	return "diff --git a/main.go b/main.go", nil
}

func (s stubSource) Close(context.Context) error { return nil }

type stubFactory struct{ source stubSource }

func (f stubFactory) Open(context.Context, review.Request) (review.DiffSource, error) {
	return f.source, nil
}

type stubPrompts struct{}

func (stubPrompts) Generate(_, _ string) (string, error) { return "prompt", nil }

type stubReviewer struct{}

func (stubReviewer) Review(context.Context, string, string) (review.Report, error) {
	return review.Report{
		Title:   "レビュー結果",
		Verdict: review.Verdict{Decision: review.DecisionNone, Reason: "問題なし"},
	}, nil
}

// stubPublisher は保存のフェイクです。保存時の context を観察できます。
type stubPublisher struct {
	seen func(ctx context.Context)
	err  error
}

func (p stubPublisher) Publish(ctx context.Context, _ review.Request, _ review.Report) error {
	if p.seen != nil {
		p.seen(ctx)
	}
	return p.err
}

// stubNotifier は通知のフェイクです。通知時の context を観察できます。
type stubNotifier struct {
	mu     sync.Mutex
	calls  int
	ctxErr error
}

func (n *stubNotifier) Notify(ctx context.Context, _ review.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.calls++
	n.ctxErr = ctx.Err()
	return nil
}

// result は通知の回数と、最後の通知が受け取った context のエラーを返します。
func (n *stubNotifier) result() (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls, n.ctxErr
}

// stubStore は進行状況のフェイクです。保存された状態を順に記録します。
type stubStore struct {
	mu    sync.Mutex
	saved []domain.JobStatus
}

func (s *stubStore) Get(_ context.Context, jobID string) (domain.JobStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := len(s.saved) - 1; i >= 0; i-- {
		if s.saved[i].JobID == jobID {
			return s.saved[i], nil
		}
	}
	return domain.JobStatus{}, jobNotFound
}

func (s *stubStore) Save(_ context.Context, jobID string, status domain.JobStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	status.JobID = jobID
	s.saved = append(s.saved, status)
	return nil
}

func (s *stubStore) states() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	states := make([]string, 0, len(s.saved))
	for _, status := range s.saved {
		states = append(states, string(status.State))
	}
	return states
}

// jobNotFound は「未記録」を表すテスト用のエラーです。
var jobNotFound = errNotRecorded{}

type errNotRecorded struct{}

func (errNotRecorded) Error() string { return "not recorded" }

// newTestPipeline は、観察用フックを差し込んだパイプラインを組み立てます。
// singleRunner は、EngineRouter を挟まず 1 本のパイプラインへ流す reviewRunner です。
// ReviewPipeline（記録・締切・再配信の面倒を見る層）の検証にエンジンの選択を
// 巻き込まないために使います。ルーティング自体は engine_router_test.go が見ます。
type singleRunner struct{ inner *pipeline.Pipeline }

func (s singleRunner) Run(ctx context.Context, req domain.ReviewRequest) (review.Result, *review.Report, error) {
	return s.inner.Run(ctx, toReviewRequest(req))
}

func newTestPipeline(t *testing.T, source stubSource, publisher stubPublisher) reviewRunner {
	t.Helper()
	return newTestPipelineWith(t, source, publisher, &stubNotifier{})
}

func newTestPipelineWith(
	t *testing.T,
	source stubSource,
	publisher stubPublisher,
	notifier review.Notifier,
) reviewRunner {
	t.Helper()

	p, err := pipeline.New(pipeline.Deps{
		Sources:   stubFactory{source: source},
		Prompts:   stubPrompts{},
		Reviewer:  stubReviewer{},
		Publisher: publisher,
		Notifier:  notifier,
	})
	if err != nil {
		t.Fatalf("パイプラインの構築に失敗: %v", err)
	}
	return singleRunner{inner: p}
}

func testDomainRequest() domain.ReviewRequest {
	return domain.ReviewRequest{
		JobID:         "20260810-213000-a1b2c3d4",
		RepoURL:       "git@github.com:org/repo.git",
		BaseBranch:    "main",
		FeatureBranch: "develop",
		Mode:          "code",
		ModelName:     "gemini-2.5-pro",
		StorageURI:    "gs://bucket/reviews/20260810-213000-a1b2c3d4/report.json",
	}
}

// ★ アダプタがレビューに締切を被せていること。
//
// これが無いと Cloud Tasks の dispatch deadline が先に来て、プロセスごと SIGTERM になり
// 失敗の記録も Slack 通知も残らない（config.DefaultPipelineTimeout のコメント参照）。
func TestReviewPipeline_レビューに締切を被せる(t *testing.T) {
	t.Parallel()

	var reviewDeadline time.Time
	var hasDeadline bool
	runner := newTestPipeline(t,
		stubSource{seen: func(ctx context.Context) { reviewDeadline, hasDeadline = ctx.Deadline() }},
		stubPublisher{},
	)

	const timeout = 25 * time.Minute
	before := time.Now()
	if err := NewReviewPipeline(runner, &stubStore{}, timeout).Execute(context.Background(), testDomainRequest()); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	if !hasDeadline {
		t.Fatal("レビューの context に締切が無い（PIPELINE_TIMEOUT が効いていない）")
	}
	if d := reviewDeadline.Sub(before); d < timeout-time.Minute || d > timeout+time.Minute {
		t.Errorf("締切までの時間 = %s, want ≒ %s", d, timeout)
	}
}

// 0 以下は無制限。ローカルでの長時間デバッグ用の逃げ道が塞がっていないこと。
func TestReviewPipeline_ゼロなら無制限(t *testing.T) {
	t.Parallel()

	var hasDeadline bool
	runner := newTestPipeline(t,
		stubSource{seen: func(ctx context.Context) { _, hasDeadline = ctx.Deadline() }},
		stubPublisher{},
	)

	if err := NewReviewPipeline(runner, &stubStore{}, 0).Execute(context.Background(), testDomainRequest()); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if hasDeadline {
		t.Error("timeout=0 なのに締切が付いている")
	}
}

// 締切で打ち切られても通知は走ること（ライブラリ側の切り離しが効いているかの結合確認）。
//
// 打ち切られた場合、保存する結果が無いので Publisher は呼ばれません。報告が要るのは
// まさにこの場面なので、Notifier が期限切れでない context で呼ばれることを確かめます。
func TestReviewPipeline_打ち切り後も通知は走る(t *testing.T) {
	t.Parallel()

	notifier := &stubNotifier{}
	runner := newTestPipelineWith(t,
		stubSource{seen: func(ctx context.Context) {
			<-ctx.Done() // 締切まで待つ = レビューが打ち切られた状態を作る
		}},
		stubPublisher{},
		notifier,
	)

	if err := NewReviewPipeline(runner, &stubStore{}, 10*time.Millisecond).Execute(context.Background(), testDomainRequest()); err == nil {
		t.Fatal("打ち切りがエラーとして返っていません")
	}

	calls, ctxErr := notifier.result()
	if calls == 0 {
		t.Fatal("打ち切り後に通知が行われていない")
	}
	if calls != 1 {
		t.Errorf("通知回数 = %d, want 1", calls)
	}
	if ctxErr != nil {
		t.Errorf("通知の context が期限切れ: %v", ctxErr)
	}
}

// 実行開始が記録され、試行回数が加算されること。
func TestReviewPipeline_実行開始を記録する(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	runner := newTestPipeline(t, stubSource{}, stubPublisher{})

	if err := NewReviewPipeline(runner, store, 0).Execute(context.Background(), testDomainRequest()); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	states := store.states()
	if len(states) == 0 || states[0] != "running" {
		t.Fatalf("最初の記録 = %v, want running", states)
	}
	if store.saved[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", store.saved[0].Attempts)
	}
}

// 完了済みのタスクが再配信されたら、レビューを実行せずに打ち切ること。
// Cloud Tasks は at-least-once 配信のため、これが無いと AI の呼び出しが二重に走ります。
func TestReviewPipeline_完了済みの再配信は打ち切る(t *testing.T) {
	t.Parallel()

	req := testDomainRequest()
	store := &stubStore{}
	if err := store.Save(context.Background(), req.JobID, domain.NewSucceededStatus(req, review.StatusSucceeded)); err != nil {
		t.Fatalf("事前の記録に失敗: %v", err)
	}

	reviewed := false
	runner := newTestPipeline(t,
		stubSource{seen: func(context.Context) { reviewed = true }},
		stubPublisher{},
	)

	if err := NewReviewPipeline(runner, store, 0).Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if reviewed {
		t.Error("完了済みなのにレビューが実行されている")
	}
}

// 結末は Run の戻り値から記録します。以前は通知フックへ相乗りしていましたが、
// Run が Report を返すようになったため直接組み立てられます。
func TestReviewPipeline_結末を記録する(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      stubSource
		publisher   stubPublisher
		wantState   string
		wantOutcome review.Status
		wantReport  bool
	}{
		{
			name:        "成功",
			wantState:   "succeeded",
			wantOutcome: review.StatusSucceeded,
			wantReport:  true,
		},
		{
			name:        "差分なし",
			source:      stubSource{diff: "   "},
			wantState:   "succeeded",
			wantOutcome: review.StatusSkipped,
		},
		{
			name:        "差分取得に失敗",
			source:      stubSource{err: errors.New("boom")},
			wantState:   "failed",
			wantOutcome: review.StatusFailed,
		},
		{
			name:        "保存に失敗",
			publisher:   stubPublisher{err: errors.New("gcs down")},
			wantState:   "failed",
			wantOutcome: review.StatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubStore{}
			runner := newTestPipeline(t, tt.source, tt.publisher)

			_ = NewReviewPipeline(runner, store, 0).Execute(context.Background(), testDomainRequest())

			// 1 件目は running、最後が結末です。
			if len(store.saved) < 2 {
				t.Fatalf("記録件数 = %d, want 2 以上 (%v)", len(store.saved), store.states())
			}

			got := store.saved[len(store.saved)-1]
			if string(got.State) != tt.wantState {
				t.Errorf("State = %q, want %q", got.State, tt.wantState)
			}
			if got.Outcome != tt.wantOutcome {
				t.Errorf("Outcome = %q, want %q", got.Outcome, tt.wantOutcome)
			}
			if got.HasReport() != tt.wantReport {
				t.Errorf("HasReport() = %v, want %v", got.HasReport(), tt.wantReport)
			}
			if tt.wantState == "failed" && got.Error == "" {
				t.Error("失敗理由が記録されていません")
			}
		})
	}
}

// 成功時はレビューの中身（題目・判定）まで記録します。履歴一覧の 1 行になります。
func TestReviewPipeline_結末にレビューの中身を載せる(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	runner := newTestPipeline(t, stubSource{}, stubPublisher{})

	req := testDomainRequest()
	if err := NewReviewPipeline(runner, store, 0).Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	got := store.saved[len(store.saved)-1]
	if got.Title != "レビュー結果" {
		t.Errorf("Title = %q, want %q", got.Title, "レビュー結果")
	}
	if got.Decision != review.DecisionNone {
		t.Errorf("Decision = %q, want %q", got.Decision, review.DecisionNone)
	}
	if got.ReportURI != req.StorageURI {
		t.Errorf("ReportURI = %q, want %q", got.ReportURI, req.StorageURI)
	}
}

// レビューが締切で打ち切られても結末は記録されること。
// 記録まで期限切れの context で行うと、いちばん記録が要る場面で残りません。
func TestReviewPipeline_打ち切り後も結末を記録する(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	runner := newTestPipeline(t,
		stubSource{seen: func(ctx context.Context) { <-ctx.Done() }},
		stubPublisher{},
	)

	_ = NewReviewPipeline(runner, store, 10*time.Millisecond).Execute(context.Background(), testDomainRequest())

	states := store.states()
	if len(states) < 2 || states[len(states)-1] != "failed" {
		t.Fatalf("打ち切りが記録されていません: %v", states)
	}
}

// engines は保存された各状態の Engine を返します。
func (s *stubStore) engines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	engines := make([]string, 0, len(s.saved))
	for _, status := range s.saved {
		engines = append(engines, status.Engine)
	}
	return engines
}

// 実行に使ったエンジンが進行状況へ記録されること。
//
// 記録が消えると、コストの高いエージェント実行が単発として残る（あるいはその逆）ので、
// 後から「なぜ高かったのか / なぜ浅かったのか」を追えなくなります。
func TestReviewPipeline_解決したエンジンを記録する(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	runner := newTestPipeline(t, stubSource{diff: "diff"}, stubPublisher{})

	req := testDomainRequest()
	req.Engine = "" // モードの既定に任せる
	if err := NewReviewPipeline(runner, store, 0).Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute が失敗した: %v", err)
	}

	engines := store.engines()
	if len(engines) == 0 {
		t.Fatal("進行状況が 1 件も記録されていない")
	}
	for i, engine := range engines {
		if engine == "" {
			t.Errorf("%d 件目の記録に Engine が載っていない: %v", i, engines)
		}
	}
}

// 解決できないエンジン指定は記録に載せないこと。
//
// 載せると「存在しないエンジンで実行して失敗した」という、実際には起きていない
// 記録が履歴に残ります。
func TestReviewPipeline_不正なエンジン指定は記録しない(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	runner := newTestPipeline(t, stubSource{diff: "diff"}, stubPublisher{})

	req := testDomainRequest()
	req.Engine = "turbo" // 存在しない
	_ = NewReviewPipeline(runner, store, 0).Execute(context.Background(), req)

	for i, engine := range store.engines() {
		if engine == "turbo" {
			t.Errorf("%d 件目の記録に不正なエンジンが載っている: %q", i, engine)
		}
	}
}
