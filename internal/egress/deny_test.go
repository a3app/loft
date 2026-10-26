package egress

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestDenyAtConnectAndSend: Deny sees the address the direct way really
// uses, for TCP at connect and for UDP after resolution.
func TestDenyAtConnectAndSend(t *testing.T) {
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
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	c, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, deny := range []bool{false, true} {
		opt := Options{SysNet: t.TempDir()}
		if deny {
			opt.Deny = func(ip net.IP) bool { return ip.IsLoopback() }
		}
		r := New(c, opt)
		conn, _, err := r.DialTCP(ctx, Who{}, ln.Addr().String())
		if conn != nil {
			conn.Close()
		}
		if deny != (err != nil) {
			t.Fatalf("deny=%v: TCP err %v", deny, err)
		}

		u := r.NewUDP(Who{}, func(string, []byte) {})
		u.Send(pc.LocalAddr().String(), []byte("x"))
		_ = pc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		buf := make([]byte, 8)
		_, _, err = pc.ReadFrom(buf)
		if deny != (err != nil) {
			t.Fatalf("deny=%v: UDP read err %v", deny, err)
		}
		u.Close()
		r.Close()
	}
}
