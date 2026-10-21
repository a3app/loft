package main

import (
	"io"
	"net"
	"testing"
	"time"
)

// TestOriginProtocol checks E/D/U against a local serveOrigin.
func TestOriginProtocol(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleOrigin(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })

	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := originEchoRTT(c); err != nil {
		t.Fatal(err)
	}
	c.Close()

	c, err = net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := originDownload(c, 64<<10); err != nil {
		t.Fatal(err)
	}
	c.Close()

	c, err = net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := originUpload(c, 64<<10); err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestMbit(t *testing.T) {
	if v := mbit(1_000_000, time.Second); v < 7.9 || v > 8.1 {
		t.Fatalf("mbit=%v", v)
	}
	_ = io.Discard
}
