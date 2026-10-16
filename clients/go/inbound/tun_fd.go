package inbound

// tun_fd.go accepts a Darwin utun fd over a unix socket from loft-priv.

import (
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/a3app/loft/internal/transport"

	"golang.org/x/sys/unix"
)

// ListenTUNSock binds a unix socket at path (0600) for loft-priv to send a utun fd.
func ListenTUNSock(path string) (net.Listener, error) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0600)
	return ln, nil
}

// AcceptTUNFD reads one SCM_RIGHTS fd and the utun name from ln.
func AcceptTUNFD(ln net.Listener) (int, string, error) {
	c, err := ln.Accept()
	if err != nil {
		return -1, "", err
	}
	defer c.Close()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, "", fmt.Errorf("tun sock")
	}
	buf := make([]byte, 16)
	oob := make([]byte, 256)
	n, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	if err != nil {
		return -1, "", err
	}
	if oobn == 0 {
		return -1, "", fmt.Errorf("tun fd: no cmsg")
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) == 0 {
		return -1, "", fmt.Errorf("tun fd: %v", err)
	}
	fds, err := unix.ParseUnixRights(&msgs[0])
	if err != nil || len(fds) == 0 {
		return -1, "", fmt.Errorf("tun fd")
	}
	name := strings.TrimRight(string(buf[:n]), "\x00")
	return fds[0], name, nil
}

// PacketTUNFromFD is a utun fd as IP ReadWriteCloser (no mux stack). For SSTP.
func PacketTUNFromFD(fd int, name string) io.ReadWriteCloser {
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, false); err != nil {
		fmt.Fprintf(os.Stderr, "tun setblock: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "tun fd %s packet\n", name)
	return &utunRWC{fd: fd, name: name}
}

// StartTUNFromFD wraps an already-configured utun fd in the userspace stack.
func StartTUNFromFD(fd int, name string, d transport.Dialer) (*Device, error) {
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, false); err != nil {
		fmt.Fprintf(os.Stderr, "tun setblock: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "tun fd %s\n", name)
	dev := &utunRWC{fd: fd, name: name}
	cidr := OverlayCIDR(transport.OverlayIP(), "")
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	st, err := newStack(dev, d, ip, ipnet)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	return &Device{ts: st, ifc: dev}, nil
}

// utunRWC talks to Darwin utun with unix.Read/Write on the raw fd.
// os.File + kqueue on PF_SYSTEM/SYSPROTO_CONTROL misfires (0-byte "ready"
// or never wakes); a short read used to look like EOF and kill tunToStack.
type utunRWC struct {
	fd   int
	name string
	rmu  sync.Mutex
	wmu  sync.Mutex
	rbuf []byte
	wbuf []byte
}

// Name is the utun interface name.
func (u *utunRWC) Name() string { return u.name }

// Close closes the raw fd once.
func (u *utunRWC) Close() error {
	if u.fd < 0 {
		return nil
	}
	err := unix.Close(u.fd)
	u.fd = -1
	return err
}

// Read strips the 4-byte utun family header and copies the IP packet into p.
func (u *utunRWC) Read(p []byte) (int, error) {
	u.rmu.Lock()
	defer u.rmu.Unlock()
	need := len(p) + 4
	if cap(u.rbuf) < need {
		u.rbuf = make([]byte, need)
	}
	buf := u.rbuf[:need]
	for {
		n, err := unix.Read(u.fd, buf)
		if err == unix.EINTR {
			continue
		}
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			runtime.Gosched()
			continue
		}
		if err != nil {
			return 0, err
		}
		if n <= 4 {
			continue
		}
		copy(p, buf[4:n])
		return n - 4, nil
	}
}

// Write prepends the 4-byte AF_INET/AF_INET6 header required by Darwin utun.
func (u *utunRWC) Write(p []byte) (int, error) {
	u.wmu.Lock()
	defer u.wmu.Unlock()
	need := 4 + len(p)
	if cap(u.wbuf) < need {
		u.wbuf = make([]byte, need)
	}
	buf := u.wbuf[:need]
	buf[0], buf[1], buf[2] = 0, 0, 0
	buf[3] = syscall.AF_INET
	if len(p) > 0 && p[0]>>4 == 6 {
		buf[3] = syscall.AF_INET6
	}
	copy(buf[4:], p)
	for off := 0; off < len(buf); {
		n, err := unix.Write(u.fd, buf[off:])
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.ErrShortWrite
		}
		off += n
	}
	return len(p), nil
}
