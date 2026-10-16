// socks.go is a SOCKS5 client for loft-probe TCP CONNECT and UDP ASSOCIATE.
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// socksHandshake offers SOCKS5 with no authentication.
func socksHandshake(c net.Conn) error {
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	rep := make([]byte, 2)
	if _, err := io.ReadFull(c, rep); err != nil {
		return err
	}
	if rep[0] != 0x05 || rep[1] != 0x00 {
		return fmt.Errorf("socks auth %x", rep)
	}
	return nil
}

// socksRequest writes a CONNECT or UDP ASSOCIATE and checks a success reply.
func socksRequest(c net.Conn, cmd byte, host string, port int) error {
	req, err := socksAddr(cmd, host, port)
	if err != nil {
		return err
	}
	if _, err := c.Write(req); err != nil {
		return err
	}
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return err
	}
	if hdr[0] != 0x05 {
		return fmt.Errorf("not socks5")
	}
	if hdr[1] != 0x00 {
		return fmt.Errorf("socks %s", socksRep(hdr[1]))
	}
	if err := skipSocksBind(c, hdr[3]); err != nil {
		return err
	}
	return nil
}

// socksConnect handshakes then CONNECT to hostport.
func socksConnect(c net.Conn, hostport string) error {
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return err
	}
	p, _ := strconv.Atoi(portStr)
	if err := socksHandshake(c); err != nil {
		return err
	}
	return socksRequest(c, 0x01, host, p)
}

// socksUDPAssociate requests UDP ASSOCIATE and returns the relay address.
func socksUDPAssociate(c net.Conn) (*net.UDPAddr, error) {
	if err := socksHandshake(c); err != nil {
		return nil, err
	}
	req, err := socksAddr(0x03, "0.0.0.0", 0)
	if err != nil {
		return nil, err
	}
	if _, err := c.Write(req); err != nil {
		return nil, err
	}
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return nil, err
	}
	if hdr[0] != 0x05 || hdr[1] != 0x00 {
		return nil, fmt.Errorf("udp associate %s", socksRep(hdr[1]))
	}
	host, port, err := readSocksBind(c, hdr[3])
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
			ip = ta.IP
		} else {
			ip = net.IPv4(127, 0, 0, 1)
		}
	}
	return &net.UDPAddr{IP: ip, Port: port}, nil
}

// socksAddr encodes a SOCKS5 request (IPv4 or domain; IPv6 is rejected).
func socksAddr(cmd byte, host string, port int) ([]byte, error) {
	out := []byte{0x05, cmd, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, 0x01)
			out = append(out, v4...)
		} else {
			return nil, fmt.Errorf("ipv6 not supported")
		}
	} else {
		if len(host) > 255 {
			return nil, fmt.Errorf("host too long")
		}
		out = append(out, 0x03, byte(len(host)))
		out = append(out, host...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port))
	return append(out, pb[:]...), nil
}

// skipSocksBind discards the bind address after a SOCKS5 reply header.
func skipSocksBind(c net.Conn, atyp byte) error {
	_, _, err := readSocksBind(c, atyp)
	return err
}

// readSocksBind parses the bind host and port from a SOCKS5 reply.
func readSocksBind(c net.Conn, atyp byte) (string, int, error) {
	switch atyp {
	case 0x01:
		b := make([]byte, 6)
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, err
		}
		return net.IP(b[:4]).String(), int(binary.BigEndian.Uint16(b[4:6])), nil
	case 0x03:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(c, lb); err != nil {
			return "", 0, err
		}
		b := make([]byte, int(lb[0])+2)
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, err
		}
		n := int(lb[0])
		return string(b[:n]), int(binary.BigEndian.Uint16(b[n : n+2])), nil
	case 0x04:
		b := make([]byte, 18)
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, err
		}
		return net.IP(b[:16]).String(), int(binary.BigEndian.Uint16(b[16:18])), nil
	default:
		return "", 0, fmt.Errorf("socks atyp %d", atyp)
	}
}

// encodeSocksUDP wraps payload in a SOCKS5 UDP header (IPv4 or domain).
func encodeSocksUDP(host string, port int, data []byte) ([]byte, error) {
	out := []byte{0, 0, 0}
	if ip := net.ParseIP(host); ip != nil {
		v4 := ip.To4()
		if v4 == nil {
			return nil, fmt.Errorf("ipv6 not supported")
		}
		out = append(out, 0x01)
		out = append(out, v4...)
	} else {
		out = append(out, 0x03, byte(len(host)))
		out = append(out, host...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port))
	out = append(out, pb[:]...)
	return append(out, data...), nil
}

// parseSocksUDP reads a SOCKS5 UDP datagram (IPv4 or domain; no fragments).
func parseSocksUDP(b []byte) (host string, port int, data []byte, err error) {
	if len(b) < 7 {
		return "", 0, nil, fmt.Errorf("short")
	}
	if b[2] != 0 {
		return "", 0, nil, fmt.Errorf("frag")
	}
	switch b[3] {
	case 0x01:
		if len(b) < 10 {
			return "", 0, nil, fmt.Errorf("short")
		}
		host = net.IP(b[4:8]).String()
		port = int(binary.BigEndian.Uint16(b[8:10]))
		return host, port, b[10:], nil
	case 0x03:
		n := int(b[4])
		if len(b) < 7+n {
			return "", 0, nil, fmt.Errorf("short")
		}
		host = string(b[5 : 5+n])
		port = int(binary.BigEndian.Uint16(b[5+n : 7+n]))
		return host, port, b[7+n:], nil
	default:
		return "", 0, nil, fmt.Errorf("atyp")
	}
}

// socksRep is a short string for a SOCKS5 reply code.
func socksRep(code byte) string {
	switch code {
	case 0x01:
		return "general failure"
	case 0x02:
		return "not allowed"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "ttl expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("reply %d", code)
	}
}

// setDeadline sets an absolute deadline on c when d is positive.
func setDeadline(c net.Conn, d time.Duration) {
	if d <= 0 {
		return
	}
	_ = c.SetDeadline(time.Now().Add(d))
}
