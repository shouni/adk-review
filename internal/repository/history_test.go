package repository

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/adk-review/internal/domain"
)

const (
	testBucket = "review-bucket"
	testJobID  = "20260810-213000-a1b2c3d4"
)

// fakeIO は remoteio の読み書きのフェイクです。
type fakeIO struct {
	objects   []string
	listErr   error
	deleteErr error

	listedPrefix string
	deleted      []string
}

func (f *fakeIO) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeIO) Exists(context.Context, string) (bool, error) { return false, nil }

func (f *fakeIO) List(_ context.Context, path string, callback func(string) error, _ ...remoteio.ListOption) error {
	f.listedPrefix = path
	if f.listErr != nil {
		return f.listErr
	}
	for _, obj := range f.objects {
		if err := callback(obj); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeIO) Write(context.Context, string, io.Reader, ...remoteio.WriteOption) error {
	return errors.New("not implemented")
}

func (f *fakeIO) Delete(_ context.Context, path string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, path)
	return nil
}

// fakeStore は進行状況のフェイクです。
type fakeStore struct{}

func (fakeStore) Get(context.Context, string) (domain.JobStatus, error) {
	return domain.JobStatus{}, errors.New("not recorded")
}

func (fakeStore) Save(context.Context, string, domain.JobStatus) error { return nil }

func newTestHistory(t *testing.T, fake *fakeIO) *History {
	t.Helper()
	return NewHistory(fake, fake, fakeStore{}, domain.NewStorageLayout(testBucket))
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
