//go:build linux || darwin

package transport

import (
	"net"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTuneTCPLeavesReceiveBufferAndCapsUnsent(t *testing.T) {
	t.Setenv("LOFT_STAND_TCPBUF", "")
	t.Setenv("LOFT_STAND_LOWAT", "")

	c := dialTCP(t)
	defer c.Close()
	before := sockOpt(t, c, unix.SOL_SOCKET, unix.SO_RCVBUF)
	tuneTCP(c)
	after := sockOpt(t, c, unix.SOL_SOCKET, unix.SO_RCVBUF)
	if after != before {
		t.Fatalf("receive buffer changed %d -> %d", before, after)
	}
	if g := sockOpt(t, c, unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT); g != notsentLowat {
		t.Fatalf("unsent cap %d", g)
	}
}

func TestTuneTCPPinStillSetsBuffer(t *testing.T) {
	t.Setenv("LOFT_STAND_TCPBUF", "1048576")
	t.Setenv("LOFT_STAND_LOWAT", "0")

	c := dialTCP(t)
	defer c.Close()
	before := sockOpt(t, c, unix.SOL_SOCKET, unix.SO_RCVBUF)
	lowBefore := sockOpt(t, c, unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT)
	tuneTCP(c)
	after := sockOpt(t, c, unix.SOL_SOCKET, unix.SO_RCVBUF)
	if after == before {
		t.Fatalf("pinned buffer stayed %d", after)
	}
	if g := sockOpt(t, c, unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT); g != lowBefore {
		t.Fatalf("unsent cap changed %d -> %d with the cap off", lowBefore, g)
	}
}

func dialTCP(t *testing.T) *net.TCPConn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	errc := make(chan error, 1)
	go func() {
		s, err := ln.Accept()
		if err != nil {
			errc <- err
			return
		}
		errc <- nil
		s.Close()
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		c.Close()
		t.Fatal(err)
	}
	return c.(*net.TCPConn)
}

func sockOpt(t *testing.T, c *net.TCPConn, level, opt int) int {
	t.Helper()
	var v int
	raw, err := c.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	cerr := raw.Control(func(fd uintptr) {
		v, err = unix.GetsockoptInt(int(fd), level, opt)
	})
	if cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}
