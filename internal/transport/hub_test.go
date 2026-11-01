package transport

import (
	"net"
	"testing"
	"time"
)

// TestOnceLnUnblocksOnConnClose checks the fix for the hub/mask Serve leak:
// after the one connection onceLn hands out is closed, the next Accept must
// return (with net.ErrClosed) instead of blocking forever, so http.Server.Serve
// exits and nothing is retained.
func TestOnceLnUnblocksOnConnClose(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	ln := &onceLn{c: a, ch: make(chan struct{})}

	// First Accept hands out the wrapped stream.
	c, err := ln.Accept()
	if err != nil {
		t.Fatalf("first accept: %v", err)
	}

	// The next Accept blocks until the handed-out conn closes.
	done := make(chan error, 1)
	go func() {
		_, err := ln.Accept()
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("second Accept returned before the conn was closed")
	case <-time.After(100 * time.Millisecond):
	}

	// http.Server closes the conn when it finishes with it; that must unblock.
	if err := c.Close(); err != nil {
		t.Fatalf("conn close: %v", err)
	}
	select {
	case err := <-done:
		if err != net.ErrClosed {
			t.Fatalf("second Accept err = %v, want net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Accept did not unblock after the conn closed (Serve would leak)")
	}
}

// TestOnceLnDoubleCloseNoPanic guards the mutex fix: two concurrent Closes must
// not both close the channel (which would panic).
func TestOnceLnDoubleCloseNoPanic(t *testing.T) {
	a, _ := net.Pipe()
	ln := &onceLn{c: a, ch: make(chan struct{})}
	start := make(chan struct{})
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_ = ln.Close()
			done <- struct{}{}
		}()
	}
	close(start)
	<-done
	<-done
	// A third Close is still safe.
	_ = ln.Close()
}
