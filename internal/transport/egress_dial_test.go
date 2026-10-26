package transport

import (
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/internal/mux"
)

// shutLoopback is the production setting for one test; TestMain opens it.
func shutLoopback(t *testing.T) {
	LoopbackOpen = false
	t.Cleanup(func() { LoopbackOpen = true })
}

func TestDialOutDropsInternal(t *testing.T) {
	shutLoopback(t)
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
	targets := []string{ln.Addr().String(), "169.254.169.254:80", "[::1]:80", "localhost:80", "localhost.:80", "metadata.google.internal:80"}
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
	shutLoopback(t)
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

// TestDialGuardChecksConnectAddress: the connect-time check catches a name
// whose second lookup answers loopback (DNS rebinding).
func TestDialGuardChecksConnectAddress(t *testing.T) {
	shutLoopback(t)
	for addr, deny := range map[string]bool{"127.0.0.1:80": true, "[::1]:80": true, "169.254.169.254:80": true, "10.1.2.3:80": false, "1.2.3.4:443": false} {
		if got := dialGuard("tcp4", addr, nil) != nil; got != deny {
			t.Fatalf("%s: denied=%v", addr, got)
		}
	}
}

// TestL3UDPDropsLoopback: l3 UDP to the node's loopback is dropped; with
// LoopbackOpen the same packet arrives, so the drop is the check.
func TestL3UDPDropsLoopback(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	pkt := make([]byte, 20+8+1)
	pkt[0] = 0x45
	pkt[9] = ipProtoUDP
	copy(pkt[12:16], net.IPv4(10, 77, 0, 9).To4())
	copy(pkt[16:20], net.IPv4(127, 0, 0, 1).To4())
	pkt[20], pkt[21] = 0x9c, 0x40
	pkt[22], pkt[23] = byte(port>>8), byte(port)
	pkt[28] = 'x'
	src := net.IPv4(10, 77, 0, 9).To4()
	reply := func([]byte) error { return nil }
	for _, open := range []bool{false, true} {
		LoopbackOpen = open
		t.Cleanup(func() { LoopbackOpen = true })
		pkt[21]++ // a fresh flow each round
		l3Inet(reply, src, pkt)
		_ = pc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		buf := make([]byte, 8)
		_, _, err := pc.ReadFrom(buf)
		if open != (err == nil) {
			t.Fatalf("LoopbackOpen=%v: read err %v", open, err)
		}
	}
}
