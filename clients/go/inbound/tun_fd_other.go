//go:build !darwin

package inbound

import (
	"fmt"
	"io"
	"net"

	"github.com/a3app/loft/internal/transport"
)

func ListenTUNSock(path string) (net.Listener, error) {
	return nil, fmt.Errorf("tun sock: %s", path)
}

func AcceptTUNFD(ln net.Listener) (int, string, error) {
	if ln != nil {
		_ = ln.Close()
	}
	return 0, "", fmt.Errorf("tun sock")
}

func PacketTUNFromFD(fd int, name string) io.ReadWriteCloser {
	return errRWC{fmt.Errorf("tun fd %d %s", fd, name)}
}

func StartTUNFromFD(fd int, name string, _ transport.Dialer) (*Device, error) {
	return nil, fmt.Errorf("tun fd %d %s", fd, name)
}

type errRWC struct{ err error }

func (e errRWC) Read([]byte) (int, error)  { return 0, e.err }
func (e errRWC) Write([]byte) (int, error) { return 0, e.err }
func (e errRWC) Close() error              { return nil }
