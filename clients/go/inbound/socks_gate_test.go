package inbound

import (
	"net"
	"testing"
	"time"
)

func TestSocksOff(t *testing.T) {
	for _, s := range []string{"", "off", "OFF", "false", "0", "-"} {
		if !SocksOff(s) {
			t.Fatalf("%q", s)
		}
	}
	if SocksOff("127.0.0.1:1080") {
		t.Fatal("addr")
	}
}

func TestSocksGateRebindKeepsDialer(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	d := &recDialer{ln: echo}
	g := NewSocksGate(d)
	defer g.Close()

	a1, err := g.Set("127.0.0.1:0")
	if err != nil || a1 == "" {
		t.Fatalf("first %q %v", a1, err)
	}
	c1, err := net.DialTimeout("tcp", a1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c1.Close()

	a2, err := g.Set("127.0.0.1:0")
	if err != nil || a2 == "" || a2 == a1 {
		t.Fatalf("second %q (was %q) %v", a2, a1, err)
	}
	if _, err := net.DialTimeout("tcp", a1, 200*time.Millisecond); err == nil {
		t.Fatal("old listen still up")
	}
	c2, err := net.DialTimeout("tcp", a2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c2.Close()

	if _, err := g.Set("off"); err != nil {
		t.Fatal(err)
	}
	if g.Addr() != "" {
		t.Fatal(g.Addr())
	}
	if _, err := net.DialTimeout("tcp", a2, 200*time.Millisecond); err == nil {
		t.Fatal("off still up")
	}
}

func TestSocksGateBindFailKeepsOld(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	busy := hold.Addr().String()

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	g := NewSocksGate(&recDialer{ln: echo})
	defer g.Close()
	old, err := g.Set("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.Set(busy)
	if err == nil {
		t.Fatal("want bind error")
	}
	if got != old {
		t.Fatalf("kept %q want %q", got, old)
	}
	c, err := net.DialTimeout("tcp", old, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}
