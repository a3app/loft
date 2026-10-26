package transport

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// setTCPCongestion switches the socket's congestion control, e.g. to "bbr".
// It fails when the kernel has no such module or does not allow it.
func setTCPCongestion(t *net.TCPConn, name string) error {
	rc, err := t.SyscallConn()
	if err != nil {
		return err
	}
	return setRawCongestion(rc, name)
}

// TCPBBRReady reports why this process could not put a TCP socket on BBR,
// or nil. A node that runs unprivileged needs the tcp_bbr module loaded and
// bbr in net.ipv4.tcp_allowed_congestion_control.
func TCPBBRReady() error {
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	defer ln.Close()
	rc, err := ln.SyscallConn()
	if err != nil {
		return err
	}
	return setRawCongestion(rc, "bbr")
}

func setRawCongestion(rc syscall.RawConn, name string) error {
	var serr error
	if err := rc.Control(func(fd uintptr) {
		serr = unix.SetsockoptString(int(fd), unix.IPPROTO_TCP, unix.TCP_CONGESTION, name)
	}); err != nil {
		return err
	}
	return serr
}
