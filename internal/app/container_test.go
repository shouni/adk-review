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

func (f *fakeFactory) Store() (remoteio.Store, error) { return nil, nil }

func (f *fakeFactory) Handler() (remoteio.Handler, error) { return nil, nil }

func TestContainerClose_ClosesEveryResource(t *testing.T) {
	// Checks that the rest are still closed when one fails. Close returns no error, so if
	// this breaks, a leaked resource shows up only in the logs.
	ff := &fakeFactory{closeErr: errors.New("remote close failed")}
	te := &fakeTaskEnqueuer{closeErr: errors.New("task close failed")}

	// The factory owns the storage's lifetime. Unlike when Bundle grouped them, what goes
	// into Closers is the factory itself.
	c := &Container{
		TaskEnqueuer: te,
		Closers:      []io.Closer{ff, te},
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
	// No assertion needed: it checks that it finishes without panicking (nil-safe).
	// The second one is a typed nil, the path that slips past the slice's nil check and
	// calls Close on a nil receiver.
	c := &Container{}
	c.Close()

	c = &Container{Closers: []io.Closer{nil, (*nilSafeCloser)(nil)}}
	c.Close()
}

// nilSafeCloser is a type whose Close works on a nil receiver.
// It is used to check that a typed nil in Closers does not crash.
type nilSafeCloser struct{}

func (c *nilSafeCloser) Close() error { return nil }
