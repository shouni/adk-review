package repository

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"testing"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-remote-io/remoteio/memio"

	"github.com/shouni/adk-review/internal/domain"
)

const (
	testBucket = "review-bucket"
	testJobID  = "20260810-213000-a1b2c3d4"
)

// fakeIO は remoteio の読み書きのフェイクです。
// fakeIO は memio を包んだストレージのフェイクです。
//
// 一覧の畳み込みや不在の返し方といったストレージの振る舞いは memio が受け持ちます
// （本物のハンドラと同じ適合性スイートを通っています）。ここに残しているのは
// 「どのプレフィックスを走査したか」「何を消したか」という呼び出しの記録と、
// 障害の注入だけです。
type fakeIO struct {
	remoteio.Store
	h *memio.Handler

	// objects は init で memio へ流し込む前提のオブジェクトです。
	objects   []string
	listErr   error
	deleteErr error

	listedPrefix string
	deleted      []string
}

// ensure は memio を組み立てて objects を流し込みます。
//
// 各テストが構造体リテラルで前提を書けるよう、生成は最初の呼び出しまで遅らせています。
// 冪等なので、どの入口から先に触られても同じ状態になります。
func (f *fakeIO) ensure(t *testing.T) {
	t.Helper()
	if f.Store != nil {
		return
	}

	f.h = memio.New(memio.WithScheme(remoteio.SchemeGCS))
	f.Store = remoteio.NewStore(f.h)
	for _, uri := range f.objects {
		if err := f.h.Seed(uri, []byte("x")); err != nil {
			t.Fatalf("seed(%s) error = %v", uri, err)
		}
	}
}

func (f *fakeIO) List(ctx context.Context, name string, opts ...remoteio.ListOption) iter.Seq2[remoteio.Entry, error] {
	f.listedPrefix = name
	if f.listErr != nil {
		return func(yield func(remoteio.Entry, error) bool) { yield(remoteio.Entry{}, f.listErr) }
	}
	return f.Store.List(ctx, name, opts...)
}

func (f *fakeIO) Delete(ctx context.Context, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, name)
	return f.Store.Delete(ctx, name)
}

// fakeStore は進行状況のフェイクです。
type fakeStore struct{}

func (fakeStore) Get(context.Context, string) (domain.JobStatus, error) {
	return domain.JobStatus{}, errors.New("not recorded")
}

func (fakeStore) Save(context.Context, string, domain.JobStatus) error { return nil }

func newTestHistory(t *testing.T, fake *fakeIO) *History {
	t.Helper()
	fake.ensure(t)
	return NewHistory(fake, fakeStore{}, domain.NewStorageLayout(testBucket))
}

// 削除はジョブのプレフィックスを走査して行います。消す側が「何を作ったか」を
// 知らずに済むため、成果物の種類が増えてもここを直す必要がありません。
func TestDeleteRemovesEverythingUnderTheJobPrefix(t *testing.T) {
	prefix := "gs://" + testBucket + "/reviews/" + testJobID + "/"
	fake := &fakeIO{objects: []string{prefix + "status.json", prefix + "report.json"}}

	if err := newTestHistory(t, fake).Delete(context.Background(), testJobID); err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}

	if fake.listedPrefix != prefix {
		t.Errorf("走査したプレフィックス = %q, want %q", fake.listedPrefix, prefix)
	}
	if len(fake.deleted) != 2 {
		t.Fatalf("削除件数 = %d, want 2 (%v)", len(fake.deleted), fake.deleted)
	}
	for _, uri := range fake.deleted {
		if !strings.HasPrefix(uri, prefix) {
			t.Errorf("プレフィックス外を削除しています: %q", uri)
		}
	}
}

// ジョブ ID はストレージのパス要素になるため、正規化してから組み立てます。
// 正規化しないと、走査するプレフィックスを外から広げられます。
func TestDeleteSanitizesJobID(t *testing.T) {
	t.Run("パス要素は切り詰める", func(t *testing.T) {
		fake := &fakeIO{}

		if err := newTestHistory(t, fake).Delete(context.Background(), "../../etc/passwd"); err != nil {
			t.Fatalf("削除に失敗: %v", err)
		}

		want := "gs://" + testBucket + "/reviews/passwd/"
		if fake.listedPrefix != want {
			t.Fatalf("走査したプレフィックス = %q, want %q", fake.listedPrefix, want)
		}
	})

	t.Run("正規化しても不正なら拒否する", func(t *testing.T) {
		fake := &fakeIO{}

		if err := newTestHistory(t, fake).Delete(context.Background(), "-bad-id"); err == nil {
			t.Fatal("エラーを期待しましたが nil でした")
		}
		if fake.listedPrefix != "" {
			t.Errorf("不正なIDで走査しています: %q", fake.listedPrefix)
		}
	})
}

func TestDeleteReportsFailures(t *testing.T) {
	prefix := "gs://" + testBucket + "/reviews/" + testJobID + "/"

	t.Run("一覧に失敗", func(t *testing.T) {
		fake := &fakeIO{listErr: errors.New("gcs down")}

		err := newTestHistory(t, fake).Delete(context.Background(), testJobID)
		if err == nil {
			t.Fatal("エラーを期待しましたが nil でした")
		}
		if len(fake.deleted) != 0 {
			t.Error("一覧に失敗したのに削除が走っています")
		}
	})

	t.Run("削除に失敗", func(t *testing.T) {
		fake := &fakeIO{
			objects:   []string{prefix + "status.json"},
			deleteErr: errors.New("permission denied"),
		}

		if err := newTestHistory(t, fake).Delete(context.Background(), testJobID); err == nil {
			t.Fatal("エラーを期待しましたが nil でした")
		}
	})
}

// 走査対象が空でもエラーにしません（既に消えている場合など）。
func TestDeleteWithNoObjects(t *testing.T) {
	fake := &fakeIO{}

	if err := newTestHistory(t, fake).Delete(context.Background(), testJobID); err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("削除件数 = %d, want 0", len(fake.deleted))
	}
}

// プレフィックスが区切りで終わっていない場合は拒否します。
// ここを取り違えると、バケットの広い範囲を消すことになります。
func TestDeletePrefixRefusesUnboundedPrefix(t *testing.T) {
	fake := &fakeIO{}
	history := newTestHistory(t, fake)

	for _, prefix := range []string{"", "gs://review-bucket/reviews"} {
		if err := history.deletePrefix(context.Background(), prefix); err == nil {
			t.Errorf("prefix=%q を拒否していません", prefix)
		}
	}
	if len(fake.deleted) != 0 {
		t.Error("拒否したはずのプレフィックスで削除が走っています")
	}
}

// --- 読み取り経路 ---

// stubStore は、ジョブ ID ごとに任意の結果を返す進行状況のフェイクです。
type stubStore struct {
	statuses map[string]domain.JobStatus
	errs     map[string]error
}

func (s stubStore) Get(_ context.Context, jobID string) (domain.JobStatus, error) {
	if err, ok := s.errs[jobID]; ok {
		return domain.JobStatus{}, err
	}
	if status, ok := s.statuses[jobID]; ok {
		return status, nil
	}
	return domain.JobStatus{}, jobstatus.ErrNotFound
}

func (stubStore) Save(context.Context, string, domain.JobStatus) error { return nil }

// contentIO は、Open で任意の内容を返す fakeIO です。
type contentIO struct {
	*fakeIO
	contents map[string]string
	openErr  error
}

func (c contentIO) Open(_ context.Context, uri string) (io.ReadCloser, error) {
	if c.openErr != nil {
		return nil, c.openErr
	}
	body, ok := c.contents[uri]
	if !ok {
		return nil, errors.New("not found: " + uri)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func jobPrefix(jobID string) string {
	return "gs://" + testBucket + "/reviews/" + jobID + "/"
}

// 一覧は「疑似ディレクトリ」だけを拾い、ジョブ 1 件を 1 行として返すこと。
//
// 区切り指定が効かなくなると配下のオブジェクトが全件返り、末尾スラッシュの
// フィルタで全部落ちて履歴一覧が常に空になります。
func TestListReturnsOneRowPerJob(t *testing.T) {
	ids := []string{"20260810-213000-a1b2c3d4", "20260811-090000-b2c3d4e5"}
	fake := &fakeIO{objects: []string{jobPrefix(ids[0]), jobPrefix(ids[1])}}
	store := stubStore{statuses: map[string]domain.JobStatus{
		ids[0]: {Status: jobstatus.Status{JobID: ids[0], State: jobstatus.StateSucceeded}},
		ids[1]: {Status: jobstatus.Status{JobID: ids[1], State: jobstatus.StateSucceeded}},
	}}

	fake.ensure(t)
	h := NewHistory(fake, store, domain.NewStorageLayout(testBucket))
	page, err := h.List(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("一覧の取得に失敗: %v", err)
	}
	if len(page.Items) != len(ids) {
		t.Fatalf("件数 = %d, want %d (%+v)", len(page.Items), len(ids), page.Items)
	}
}

// 一覧はジョブ ID に埋め込まれた時刻で新しい順に並ぶこと。
// 並び順キーを外すと辞書順へ退行し、採番の接頭辞が変わった瞬間に日付を無視した
// 並びになります。
func TestListSortsNewestFirst(t *testing.T) {
	older := "20260810-213000-a1b2c3d4"
	newer := "20260811-090000-b2c3d4e5"
	// 敢えて古い順に返させ、並べ替えが効いていることを見ます。
	fake := &fakeIO{objects: []string{jobPrefix(older), jobPrefix(newer)}}
	store := stubStore{statuses: map[string]domain.JobStatus{
		older: {Status: jobstatus.Status{JobID: older}},
		newer: {Status: jobstatus.Status{JobID: newer}},
	}}

	fake.ensure(t)
	h := NewHistory(fake, store, domain.NewStorageLayout(testBucket))
	page, err := h.List(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("一覧の取得に失敗: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("件数 = %d, want 2", len(page.Items))
	}
	if page.Items[0].JobID != newer {
		t.Errorf("先頭 = %q, want %q（新しい順になっていません）", page.Items[0].JobID, newer)
	}
}

// まだ記録が無いジョブも一覧から落とさないこと。
// 落とすと、投入したはずのレビューを画面から追えなくなります。
func TestListKeepsJobsWithoutStatus(t *testing.T) {
	fake := &fakeIO{objects: []string{jobPrefix(testJobID)}}
	store := stubStore{errs: map[string]error{testJobID: jobstatus.ErrNotFound}}

	fake.ensure(t)
	h := NewHistory(fake, store, domain.NewStorageLayout(testBucket))
	page, err := h.List(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("一覧の取得に失敗: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("件数 = %d, want 1（未記録のジョブが落ちています）", len(page.Items))
	}
	if page.Items[0].JobID != testJobID {
		t.Errorf("JobID = %q, want %q", page.Items[0].JobID, testJobID)
	}
}

// ★ 読み取り自体の失敗は握り潰さないこと。
//
// 未記録と同じ扱いにすると、権限剥奪やストレージ障害が
// 「ジョブ ID だけの空行が並ぶ 200 OK」として出て、障害が障害に見えなくなります。
func TestListSurfacesUnavailableStatus(t *testing.T) {
	fake := &fakeIO{objects: []string{jobPrefix(testJobID)}}
	store := stubStore{errs: map[string]error{testJobID: jobstatus.ErrUnavailable}}

	fake.ensure(t)
	h := NewHistory(fake, store, domain.NewStorageLayout(testBucket))
	if _, err := h.List(context.Background(), 1, 20); err == nil {
		t.Fatal("読み取り失敗が握り潰されました")
	}
}

// 詳細はレポート本文まで読み込むこと。
func TestGetLoadsReport(t *testing.T) {
	uri := jobPrefix(testJobID) + "report.json"
	fake := &fakeIO{}
	rio := contentIO{
		fakeIO:   fake,
		contents: map[string]string{uri: `{"title":"レビュー結果","summary":"要約","verdict":{"decision":"minor"},"findings":[]}`},
	}
	store := stubStore{statuses: map[string]domain.JobStatus{
		testJobID: {
			Status:    jobstatus.Status{JobID: testJobID, State: jobstatus.StateSucceeded},
			ReportURI: uri,
		},
	}}

	fake.ensure(t)
	h := NewHistory(rio, store, domain.NewStorageLayout(testBucket))
	detail, err := h.Get(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("詳細の取得に失敗: %v", err)
	}
	if detail.Report == nil {
		t.Fatal("レポートが読み込まれていません")
	}
	if detail.Report.Title != "レビュー結果" {
		t.Errorf("Title = %q", detail.Report.Title)
	}
}

// レポートが読めなくても進行状況までは見せること。
// 404 を返すより、何が起きたかを画面から追えるほうが手掛かりが残ります。
func TestGetKeepsStatusWhenReportUnreadable(t *testing.T) {
	fake := &fakeIO{}
	rio := contentIO{fakeIO: fake, openErr: errors.New("permission denied")}
	store := stubStore{statuses: map[string]domain.JobStatus{
		testJobID: {
			Status:    jobstatus.Status{JobID: testJobID, State: jobstatus.StateSucceeded},
			ReportURI: jobPrefix(testJobID) + "report.json",
		},
	}}

	fake.ensure(t)
	h := NewHistory(rio, store, domain.NewStorageLayout(testBucket))
	detail, err := h.Get(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("進行状況まで失われました: %v", err)
	}
	if detail.Report != nil {
		t.Error("読めなかったレポートが入っています")
	}
	if detail.Status.JobID != testJobID {
		t.Errorf("JobID = %q, want %q", detail.Status.JobID, testJobID)
	}
}

// 未記録は ErrNotFound のまま返すこと。ハンドラーはこれで 404 と 500 を切り分けます。
func TestGetPreservesNotFound(t *testing.T) {
	fake := &fakeIO{}
	fake.ensure(t)
	h := NewHistory(fake, stubStore{}, domain.NewStorageLayout(testBucket))

	_, err := h.Get(context.Background(), testJobID)
	if !errors.Is(err, jobstatus.ErrNotFound) {
		t.Fatalf("err = %v, ErrNotFound として扱えません", err)
	}
}
