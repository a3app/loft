package protect

import (
	"net"
	"testing"
	"time"
)

func TestBindUnknown(t *testing.T) {
	if err := Bind("no-such-iface"); err == nil {
		t.Fatal("want error")
	}
}

func TestBindEmptyClears(t *testing.T) {
	if err := Bind("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if Bound() == "" {
		t.Fatal("bound")
	}
	if err := Bind(""); err != nil {
		t.Fatal(err)
	}
	if Bound() != "" {
		t.Fatalf("cleared %q", Bound())
	}
	if NetDialer(time.Second).Control != nil {
		t.Fatal("control")
	}
}

func TestBindLoopbackDial(t *testing.T) {
	if err := Bind("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Bind("") })
	if Bound() == "" {
		t.Fatal("bound")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	c, err := Timeout("tcp4", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestBindByName(t *testing.T) {
	var err error
	for _, n := range []string{"lo0", "lo"} {
		if err = Bind(n); err == nil {
			break
		}
	}
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = Bind("") })
	if Bound() == "" {
		t.Fatal("bound")
	}
}
