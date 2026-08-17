package app

import (
	"context"
	"errors"
	"testing"

	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-remote-io/remoteio"
)

type fakeTaskEnqueuer struct {
	closeErr error
	closed   bool
}

func (f *fakeTaskEnqueuer) Enqueue(_ context.Context, _ domain.ReviewRequest) error { return nil }
func (f *fakeTaskEnqueuer) Close() error {
	f.closed = true
	return f.closeErr
}

type fakeFactory struct {
	closeErr error
	closed   bool
}

func (f *fakeFactory) Close() error {
	f.closed = true
	return f.closeErr
}

func (f *fakeFactory) Reader() (remoteio.Reader, error) {
	return nil, nil
}

func (f *fakeFactory) Writer() (remoteio.Writer, error) {
	return nil, nil
}

func (f *fakeFactory) InputReader() (remoteio.InputReader, error) {
	return nil, nil
}

func (f *fakeFactory) OutputWriter() (remoteio.OutputWriter, error) {
	return nil, nil
}

func (f *fakeFactory) URLSigner() (remoteio.URLSigner, error) {
	return nil, nil
}

func TestRemoteIOClose(t *testing.T) {
	r := &RemoteIO{}
	if err := r.Close(); err != nil {
		t.Fatalf("nil factory should not error: %v", err)
	}

	ff := &fakeFactory{}
	r.Factory = ff
	if err := r.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if !ff.closed {
		t.Fatal("factory close should be called")
	}
}

func TestContainerClose_AlwaysClosesTaskEnqueuer(t *testing.T) {
	ff := &fakeFactory{closeErr: errors.New("remote close failed")}
	te := &fakeTaskEnqueuer{closeErr: errors.New("task close failed")}

	c := &Container{
		RemoteIO:     &RemoteIO{Factory: ff},
		TaskEnqueuer: te,
	}

	c.Close()

	if !ff.closed {
		t.Fatal("remote io close should be called")
	}
	if !te.closed {
		t.Fatal("task enqueuer close should be called even if remote io close fails")
	}
}

func TestContainerClose_NilSafe(_ *testing.T) {
	// アサーションは不要。パニックなく終了すること（nil-safe）を検証する。
	c := &Container{}
	c.Close()

	c = &Container{RemoteIO: &RemoteIO{}}
	c.Close()
}
