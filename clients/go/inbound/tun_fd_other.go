//go:build !darwin

package inbound

// tun_fd_other.go holds stubs for the fd-passing TUN path on every system
// except macOS. On macOS a privileged helper (loft-priv) opens the utun
// device and hands its file descriptor over a unix socket (see tun_fd.go);
// elsewhere these functions only return errors so callers still compile.

import (
	"fmt"
	"io"
	"net"

	"github.com/a3app/loft/internal/transport"
)

// ListenTUNSock always fails here: fd passing exists only on macOS.
func ListenTUNSock(path string) (net.Listener, error) {
	return nil, fmt.Errorf("tun sock: %s", path)
}

// AcceptTUNFD closes ln and fails: there is no helper to receive an fd from.
func AcceptTUNFD(ln net.Listener) (int, string, error) {
	if ln != nil {
		_ = ln.Close()
	}
	return 0, "", fmt.Errorf("tun sock")
}

// PacketTUNFromFD returns a device whose every Read and Write fails, because
// a raw utun fd cannot exist on this system.
func PacketTUNFromFD(fd int, name string) io.ReadWriteCloser {
	return errRWC{fmt.Errorf("tun fd %d %s", fd, name)}
}

// StartTUNFromFD always fails here: fd passing exists only on macOS.
func StartTUNFromFD(fd int, name string, _ transport.Dialer) (*Device, error) {
	return nil, fmt.Errorf("tun fd %d %s", fd, name)
}

// errRWC is a ReadWriteCloser that reports the same error on every Read and
// Write. Close succeeds so cleanup code does not trip over it.
type errRWC struct{ err error }

// Read always fails with the stored error.
func (e errRWC) Read([]byte) (int, error) { return 0, e.err }

// Write always fails with the stored error.
func (e errRWC) Write([]byte) (int, error) { return 0, e.err }

// Close does nothing; there is nothing to release.
func (e errRWC) Close() error { return nil }
