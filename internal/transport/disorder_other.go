//go:build !unix && !windows

package transport

import (
	"errors"
	"net"
)

// setSockTTL is not available here; disorder then sends ClientHello whole.
func setSockTTL(*net.TCPConn, int) (int, error) {
	return 0, errors.New("disorder: TTL not supported on this OS")
}
