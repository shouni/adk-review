package app

import (
	"context"
	"errors"
	"io"
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

func TestContainerClose_ClosesEveryResource(t *testing.T) {
	// 片方が失敗しても残りを閉じ切ることを見ます。Close はエラーを返さないので、
	// ここが崩れると資源の閉じ漏れがログにしか出ません。
	ff := &fakeFactory{closeErr: errors.New("remote close failed")}
	te := &fakeTaskEnqueuer{closeErr: errors.New("task close failed")}
	rio := &RemoteIO{Factory: ff}

	c := &Container{
		RemoteIO:     rio,
		TaskEnqueuer: te,
		Closers:      []io.Closer{rio, te},
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
	// 3 つ目は typed-nil で、スライスの nil チェックをすり抜けて nil レシーバーの
	// Close が呼ばれる経路です（remoteio.Bundle.Close がこれを許容します）。
	c := &Container{}
	c.Close()

	c = &Container{Closers: []io.Closer{nil, &RemoteIO{}, (*RemoteIO)(nil)}}
	c.Close()
}
