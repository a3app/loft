// Package protect marks VPN process sockets so they use the physical path
// (Android VpnService.protect). iOS uses excludedRoutes instead; the hook is a no-op.
package protect

import (
	"context"
	"net"
	"strings"
	"syscall"
	"time"
)

// Func is VpnService.protect(fd). True means the fd is bound off the tunnel.
type Func func(fd int) bool

var current Func

// Set installs the OS protector. Nil clears it.
func Set(f Func) { current = f }

// NetDialer is a Dialer whose new fds go through the protector and -if bind.
func NetDialer(timeout time.Duration) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	if ip := localIPv4(); ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	if current != nil || boundIndex() > 0 {
		d.Control = Control
		// PreferGo: Android VpnService DNS is overlay 10.8.0.1. A GET
		// reconnect that uses libc/getaddrinfo deadlocks (mux down → DNS
		// down → mux stays down). Dial public DNS on a protected socket.
		d.Resolver = &net.Resolver{PreferGo: true, Dial: resolverDial}
	}
	return d
}

// Timeout dials with NetDialer.
func Timeout(network, addr string, timeout time.Duration) (net.Conn, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return DialContext(ctx, network, addr)
}

// DialContext dials through the protector. ctx cancel aborts a stuck SYN.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	d := NetDialer(0)
	if ip := localIPv4(); ip != nil && strings.HasPrefix(network, "udp") {
		d.LocalAddr = &net.UDPAddr{IP: ip}
	}
	return d.DialContext(ctx, network, addr)
}

func resolverDial(ctx context.Context, network, address string) (net.Conn, error) {
	if current != nil {
		address = bypassDNS(network, address)
	}
	d := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if r := time.Until(deadline); r > 0 && r < d {
			d = r
		}
	}
	return Timeout(network, address, d)
}

// bypassDNS replaces overlay/private nameservers with a public resolver.
// Public IPs (8.8.8.8, ISP) stay so a working physical DNS is reused.
func bypassDNS(network, address string) string {
	host, port, err := net.SplitHostPort(address)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil && !poisonDNS(ip) {
			return address
		}
	}
	if port == "" {
		port = "53"
	}
	if strings.Contains(network, "6") {
		return net.JoinHostPort("2606:4700:4700::1111", port)
	}
	return net.JoinHostPort("1.1.1.1", port)
}

func poisonDNS(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

// Control is net.Dialer.Control: bind to -if, then protect the raw fd.
func Control(network, _address string, c syscall.RawConn) error {
	idx := boundIndex()
	f := current
	if idx <= 0 && f == nil {
		return nil
	}
	ipv6 := strings.Contains(network, "6")
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		if idx > 0 {
			sockErr = setBoundIf(int(fd), idx, ipv6)
		}
		if sockErr == nil && f != nil && !f(int(fd)) {
			sockErr = errProtect
		}
	}); err != nil {
		return err
	}
	return sockErr
}

// ListenUDP4 is a wildcard UDP socket, protected and bound when set.
func ListenUDP4() (*net.UDPConn, error) {
	la := &net.UDPAddr{}
	if ip := localIPv4(); ip != nil {
		la.IP = ip
	}
	c, err := net.ListenUDP("udp4", la)
	if err != nil {
		return nil, err
	}
	Apply(c)
	return c, nil
}

// Apply runs bind + protector on an existing socket. No-op if unset.
func Apply(conn syscall.Conn) {
	if conn == nil {
		return
	}
	idx := boundIndex()
	f := current
	if idx <= 0 && f == nil {
		return
	}
	rc, err := conn.SyscallConn()
	if err != nil {
		return
	}
	_ = rc.Control(func(fd uintptr) {
		if idx > 0 {
			_ = setBoundIf(int(fd), idx, false)
		}
		if f != nil {
			f(int(fd))
		}
	})
}

type protectError struct{}

func (protectError) Error() string { return "protect" }

var errProtect protectError
