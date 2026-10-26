package transport

import (
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/internal/mux"
)

func TestDialOutDropsInternal(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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
	cfg, err := egress.Parse("")
	if err != nil {
		t.Fatal(err)
	}
	r := egress.New(cfg, egress.Options{})
	defer r.Close()
	old := Egress.Load()
	t.Cleanup(func() { Egress.Store(old) })

	if spliceDenied("10.1.2.3") || spliceDenied("1.2.3.4") {
		t.Fatal("private and public stay open")
	}
	targets := []string{ln.Addr().String(), "169.254.169.254:80", "[::1]:80", "localhost:80", "metadata.google.internal:80"}
	for _, rt := range []*egress.Router{nil, r} {
		Egress.Store(rt)
		for _, target := range targets {
			c, err := dialOut(egress.Who{}, target)
			if c != nil {
				c.Close()
			}
			if err == nil {
				t.Fatalf("%s dialed", target)
			}
		}
	}
}

func TestServeDgramsDropsLoopback(t *testing.T) {
	ln, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	old := Egress.Load()
	Egress.Store(nil)
	t.Cleanup(func() { Egress.Store(old) })

	cli, srv := mux.NewClient(), mux.NewServer()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if fs := cli.Pull(64*1024, 20*time.Millisecond); len(fs) > 0 {
				srv.Push(fs)
			}
		}
	}()
	go serveDgrams(srv)
	time.Sleep(30 * time.Millisecond)
	if err := cli.WriteDgram(ln.LocalAddr().String(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = ln.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 32)
	if n, _, err := ln.ReadFrom(buf); err == nil {
		t.Fatalf("loopback udp %d", n)
	}
}
