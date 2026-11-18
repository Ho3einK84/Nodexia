package sshclient

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

type nopReadCloser struct {
	io.Reader
	closed bool
}

func (n *nopReadCloser) Close() error {
	n.closed = true
	return nil
}

func TestRemoteReadCloserLifecycle(t *testing.T) {
	inner := &nopReadCloser{Reader: bytes.NewReader([]byte("hello world"))}
	cleanedUp := false
	cleanup := func() { cleanedUp = true }

	rc := &remoteReadCloser{
		reader:  inner,
		cleanup: cleanup,
	}

	buf := make([]byte, 5)
	n, err := rc.Read(buf)
	if err != nil || n != 5 || string(buf) != "hello" {
		t.Fatalf("unexpected read: n=%d err=%v buf=%s", n, err, string(buf))
	}

	if err := rc.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	if !inner.closed {
		t.Fatal("expected inner reader to be closed")
	}
	if !cleanedUp {
		t.Fatal("expected cleanup to be called")
	}

	// Reading after Close should return EOF without panic
	n, err = rc.Read(buf)
	if !errors.Is(err, io.EOF) || n != 0 {
		t.Fatalf("expected EOF after close, got n=%d err=%v", n, err)
	}

	// Double Close should be idempotent
	if err := rc.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestRemoteReadCloserConcurrent(t *testing.T) {
	inner := &nopReadCloser{Reader: bytes.NewReader(bytes.Repeat([]byte("a"), 10000))}
	rc := &remoteReadCloser{
		reader: inner,
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 64)
			for {
				_, err := rc.Read(buf)
				if err != nil {
					return
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = rc.Close()
	}()

	wg.Wait()
}
