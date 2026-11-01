package transport

// The decoy listener reads each ClientHello off the accept loop.

import (
	"net"
	"testing"
	"time"
)

// TestDecoySilentConnDoesNotBlockOthers: a client that opens TCP and sends
// nothing must not stop the next connection from being handled. The next
// one sends a non-TLS first flight, which goes straight to the decoy.
func TestDecoySilentConnDoesNotBlockOthers(t *testing.T) {
	origin, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	reached := make(chan struct{}, 1)
	go func() {
		for {
			c, err := origin.Accept()
			if err != nil {
				return
			}
			reached <- struct{}{}
			c.Close()
		}
	}()
	inner, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := WrapDecoy(inner, []byte("test-token-please-change"), []string{origin.Addr().String()})
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	silent, err := net.Dial("tcp4", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	time.Sleep(50 * time.Millisecond) // the silent one is accepted first

	next, err := net.Dial("tcp4", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err := next.Write([]byte("GET / HTTP/1.1\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatal("a silent connection held up the next one")
	}
}

// TestDecoyCloseUnblocksAccept: Close ends a pending Accept with an error.
func TestDecoyCloseUnblocksAccept(t *testing.T) {
	inner, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := WrapDecoy(inner, []byte("test-token-please-change"), []string{"127.0.0.1:9"})
	done := make(chan error, 1)
	go func() {
		_, err := ln.Accept()
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	ln.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Accept returned no error after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept still blocked after Close")
	}
}
