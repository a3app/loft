// icmp_test.go covers overlay ICMP echo via mux and HTTP via mux.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
)

// stubDialer is a transport.Dialer that only exposes a mux session.
type stubDialer struct {
	s    *mux.Session
	done chan struct{}
}

// Open always fails; ICMP tests use Session only.
func (s stubDialer) Open(string) (net.Conn, error) { return nil, fmt.Errorf("no open") }

// Session returns the stub mux session.
func (s stubDialer) Session() *mux.Session { return s.s }

// Done returns the stub closed channel.
func (s stubDialer) Done() <-chan struct{} { return s.done }

// Close closes the mux session.
func (s stubDialer) Close() error {
	s.s.Close()
	return nil
}

// pumpMux copies frames from a to b until a is closed.
func pumpMux(a, b *mux.Session) {
	for {
		fs := a.Pull(64*1024, 20*time.Millisecond)
		if len(fs) > 0 {
			b.Push(fs)
		}
		if a.Closed() {
			return
		}
	}
}

// TestICMPViaMux echoes an overlay ICMP request through a paired mux session.
func TestICMPViaMux(t *testing.T) {
	cli, srv := mux.NewClient(), mux.NewServer()
	go pumpMux(cli, srv)
	go pumpMux(srv, cli)
	go func() {
		for {
			addr, data, err := srv.ReadDgram()
			if err != nil {
				return
			}
			host, port, err := net.SplitHostPort(addr)
			if err != nil || port != "1" {
				continue
			}
			rep := icmpSwapReply(data)
			_ = srv.WriteDgram(net.JoinHostPort("10.8.0.5", "1"), rep)
			_ = host
		}
	}()
	v := &muxVia{
		d:  stubDialer{s: cli, done: make(chan struct{})},
		ip: "10.8.0.5",
		ch: make(chan datagram, 16),
	}
	cli.FilterDgram(func(addr string, data []byte) bool {
		select {
		case v.ch <- datagram{addr: addr, data: append([]byte(nil), data...)}:
		default:
		}
		return true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := runICMP(ctx, v, spec{proto: "icmp", host: "8.8.8.8", raw: "8.8.8.8"}, 1, 2*time.Second)
	if !r.OK {
		t.Fatalf("%+v", r)
	}
}

// TestICMPSkipSOCKS skips ICMP when the via is SOCKS.
func TestICMPSkipSOCKS(t *testing.T) {
	r := runICMP(context.Background(), &socksVia{addr: "127.0.0.1:1"}, spec{proto: "icmp", host: "8.8.8.8"}, 1, time.Second)
	if !r.Skip {
		t.Fatalf("%+v", r)
	}
}

// TestHTTPViaMux GETs through mux to a local HTTP stub and expects 200.
func TestHTTPViaMux(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 256)
		_, _ = c.Read(buf)
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\nConnection: close\r\n\r\npass")
	}()
	cli, srv := mux.NewClient(), mux.NewServer()
	go pumpMux(cli, srv)
	go pumpMux(srv, cli)
	go func() {
		st, err := srv.Accept()
		if err != nil {
			return
		}
		defer st.Close()
		backend, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		defer backend.Close()
		errc := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(backend, st); errc <- struct{}{} }()
		go func() { _, _ = io.Copy(st, backend); errc <- struct{}{} }()
		<-errc
	}()
	v := &muxVia{d: openStub{s: cli}, ip: "10.8.0.5", ch: make(chan datagram, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := runHTTP(ctx, v, spec{proto: "http", host: "example.com", port: 80, path: "/", raw: "http://example.com/"}, 2*time.Second, false, 0, 0)
	if !r.OK {
		t.Fatalf("%+v", r)
	}
}

// openStub is a transport.Dialer that opens mux streams.
type openStub struct {
	s *mux.Session
}

// Open opens a mux stream to target.
func (o openStub) Open(target string) (net.Conn, error) { return o.s.Open(target) }

// Session returns the stub mux session.
func (o openStub) Session() *mux.Session { return o.s }

// Done is unused in the HTTP test.
func (o openStub) Done() <-chan struct{} { return nil }

// Close closes the mux session.
func (o openStub) Close() error { o.s.Close(); return nil }
