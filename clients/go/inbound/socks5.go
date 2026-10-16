package inbound

// socks5.go is the SOCKS5 TCP CONNECT and UDP ASSOCIATE listener.
// Native IPv6 is rejected until overlay IPv6 is assigned.

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/transport"
)

// ListenSOCKS5 accepts SOCKS5 CONNECT and UDP ASSOCIATE on addr.
func ListenSOCKS5(addr string, d transport.Dialer) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ln = transport.TuneListener(ln)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSOCKS(c, d)
		}
	}()
	return ln, nil
}

// handleSOCKS serves one SOCKS5 client: handshake, then CONNECT or UDP ASSOCIATE.
func handleSOCKS(c net.Conn, d transport.Dialer) {
	defer c.Close()
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
	}
	if err := socksHandshake(c); err != nil {
		return
	}
	cmd, target, err := socksRequest(c)
	if err != nil {
		return
	}
	if cmd == 0x03 {
		handleUDPAssociate(c, d)
		return
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	canon, ok := canonSOCKSHost(host)
	if !ok {
		// Native IPv6 with no overlay IPv6: fail fast (ATYP not supported)
		// so Chrome Happy Eyeballs falls back to IPv4. Same as an IPv4-only
		// tunnel returning ENETUNREACH.
		_, _ = c.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	host = canon
	target = net.JoinHostPort(host, port)
	ip := net.ParseIP(host)
	switch transport.LookupAddr(host, ip) {
	case transport.ActDrop:
		_, _ = c.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	case transport.ActDirect:
		remote, err := net.DialTimeout("tcp", target, 15*time.Second)
		if err != nil {
			_, _ = c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return
		}
		defer remote.Close()
		_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		relay(c, remote)
		return
	}
	remote, err := d.Open(target)
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	relay(c, remote)
}

// relay copies both ways and half-closes. Waiting on the first Copy then
// Close() with unread bytes is a TCP RST (Chrome ERR_CONNECTION_RESET).
func relay(client, remote net.Conn) {
	errc := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(remote, client)
		closeWrite(remote)
		errc <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, remote)
		closeWrite(client)
		errc <- struct{}{}
	}()
	<-errc
	<-errc
	drain(client)
}

// closeWrite half-closes c when it supports CloseWrite.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

// drain reads leftover bytes after the first Copy so Close is not a RST.
func drain(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, _ = io.Copy(io.Discard, io.LimitReader(c, 64<<10))
}

// handleUDPAssociate binds a local UDP socket and relays SOCKS UDP over mux datagrams.
func handleUDPAssociate(c net.Conn, d transport.Dialer) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer pc.Close()
	uaddr := pc.LocalAddr().(*net.UDPAddr)
	ip := uaddr.IP.To4()
	if ip == nil {
		ip = net.IPv4(127, 0, 0, 1)
	}
	rep := []byte{0x05, 0x00, 0x00, 0x01, ip[0], ip[1], ip[2], ip[3], 0, 0}
	binary.BigEndian.PutUint16(rep[8:], uint16(uaddr.Port))
	if _, err := c.Write(rep); err != nil {
		return
	}
	if sess := d.Session(); sess != nil {
		relayMuxUDP(pc, sess)
		buf := make([]byte, 1)
		_, _ = c.Read(buf)
		return
	}
	ud, ok := d.(interface {
		ListenUDP() (net.PacketConn, error)
	})
	if !ok {
		buf := make([]byte, 1)
		_, _ = c.Read(buf)
		return
	}
	via, err := ud.ListenUDP()
	if err != nil {
		return
	}
	defer via.Close()
	var resolve func(string) (net.IP, error)
	if r, ok := d.(interface {
		LookupIP4(string) (net.IP, error)
	}); ok {
		resolve = r.LookupIP4
	}
	relayPacketUDP(pc, via, resolve)
	buf := make([]byte, 1)
	_, _ = c.Read(buf)
}

// relayMuxUDP copies SOCKS UDP datagrams over the overlay mux.
func relayMuxUDP(pc net.PacketConn, sess interface {
	WriteDgram(addr string, data []byte) error
	ReadDgram() (addr string, data []byte, err error)
}) {
	var client atomic.Value
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			client.Store(from)
			host, port, data, err := parseSocksUDP(buf[:n])
			if err != nil {
				continue
			}
			canon, ok := canonSOCKSHost(host)
			if !ok {
				continue
			}
			_ = sess.WriteDgram(net.JoinHostPort(canon, strconv.Itoa(int(port))), data)
		}
	}()
	go func() {
		for {
			addr, data, err := sess.ReadDgram()
			if err != nil {
				return
			}
			to, _ := client.Load().(net.Addr)
			if to == nil {
				continue
			}
			pkt, err := encodeSocksUDP(addr, data)
			if err != nil {
				continue
			}
			_, _ = pc.WriteTo(pkt, to)
		}
	}()
}

// relayPacketUDP copies SOCKS UDP datagrams through a userspace PacketConn (SSTP).
func relayPacketUDP(pc, via net.PacketConn, resolve func(string) (net.IP, error)) {
	var client atomic.Value
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			client.Store(from)
			host, port, data, err := parseSocksUDP(buf[:n])
			if err != nil {
				continue
			}
			canon, ok := canonSOCKSHost(host)
			if !ok {
				continue
			}
			ip := net.ParseIP(canon)
			if ip == nil {
				if resolve == nil {
					continue
				}
				var err error
				ip, err = resolve(canon)
				if err != nil {
					continue
				}
			}
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			_, _ = via.WriteTo(data, &net.UDPAddr{IP: v4, Port: int(port)})
		}
	}()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := via.ReadFrom(buf)
			if err != nil {
				return
			}
			to, _ := client.Load().(net.Addr)
			if to == nil {
				continue
			}
			a := ""
			if addr != nil {
				a = addr.String()
			}
			pkt, err := encodeSocksUDP(a, buf[:n])
			if err != nil {
				continue
			}
			_, _ = pc.WriteTo(pkt, to)
		}
	}()
}

// parseSocksUDP reads a SOCKS5 UDP datagram (IPv4, domain, or IPv6).
func parseSocksUDP(b []byte) (host string, port uint16, data []byte, err error) {
	if len(b) < 7 {
		return "", 0, nil, fmt.Errorf("short")
	}
	if b[2] != 0 {
		return "", 0, nil, fmt.Errorf("frag")
	}
	off := 3
	switch b[off] {
	case 0x01:
		if len(b) < 10 {
			return "", 0, nil, fmt.Errorf("short")
		}
		host = net.IP(b[4:8]).String()
		port = binary.BigEndian.Uint16(b[8:10])
		return host, port, b[10:], nil
	case 0x03:
		n := int(b[4])
		if len(b) < 7+n {
			return "", 0, nil, fmt.Errorf("short")
		}
		host = string(b[5 : 5+n])
		port = binary.BigEndian.Uint16(b[5+n : 7+n])
		return host, port, b[7+n:], nil
	case 0x04:
		if len(b) < 22 {
			return "", 0, nil, fmt.Errorf("short")
		}
		host = net.IP(b[4:20]).String()
		port = binary.BigEndian.Uint16(b[20:22])
		return host, port, b[22:], nil
	default:
		return "", 0, nil, fmt.Errorf("atyp")
	}
}

// encodeSocksUDP wraps payload in a SOCKS5 UDP header (IPv4 or domain).
func encodeSocksUDP(addr string, data []byte) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	p, _ := strconv.Atoi(portStr)
	ip := net.ParseIP(host)
	out := []byte{0, 0, 0}
	if v4 := ip.To4(); v4 != nil {
		out = append(out, 0x01)
		out = append(out, v4...)
	} else {
		out = append(out, 0x03, byte(len(host)))
		out = append(out, host...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(p))
	out = append(out, pb[:]...)
	return append(out, data...), nil
}

// canonSOCKSHost maps IPv4-mapped IPv6 (::ffff:a.b.c.d) to IPv4.
// Native IPv6 is not forwarded until the mux carries overlay IPv6.
func canonSOCKSHost(host string) (string, bool) {
	ip := net.ParseIP(host)
	if ip == nil {
		return host, true
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String(), true
	}
	return "", false
}

// socksHandshake accepts SOCKS5 with no authentication.
func socksHandshake(c net.Conn) error {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return err
	}
	if hdr[0] != 0x05 {
		return fmt.Errorf("not socks5")
	}
	nmethods := int(hdr[1])
	if _, err := io.ReadFull(c, make([]byte, nmethods)); err != nil {
		return err
	}
	_, err := c.Write([]byte{0x05, 0x00})
	return err
}

// socksRequest reads CONNECT (0x01) or UDP ASSOCIATE (0x03) and the target.
func socksRequest(c net.Conn) (byte, string, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return 0, "", err
	}
	if hdr[0] != 0x05 || (hdr[1] != 0x01 && hdr[1] != 0x03) {
		_, _ = c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return 0, "", fmt.Errorf("cmd")
	}
	host, port, err := readSocksAddr(c, hdr[3])
	if err != nil {
		return 0, "", err
	}
	return hdr[1], net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

// readSocksAddr parses ATYP IPv4, domain, or IPv6 plus port.
func readSocksAddr(c net.Conn, atyp byte) (string, uint16, error) {
	var host string
	switch atyp {
	case 0x01:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(c, addr); err != nil {
			return "", 0, err
		}
		host = net.IP(addr).String()
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return "", 0, err
		}
		name := make([]byte, l[0])
		if _, err := io.ReadFull(c, name); err != nil {
			return "", 0, err
		}
		host = string(name)
	case 0x04:
		addr := make([]byte, 16)
		if _, err := io.ReadFull(c, addr); err != nil {
			return "", 0, err
		}
		host = net.IP(addr).String()
	default:
		return "", 0, fmt.Errorf("atyp")
	}
	var pb [2]byte
	if _, err := io.ReadFull(c, pb[:]); err != nil {
		return "", 0, err
	}
	return host, binary.BigEndian.Uint16(pb[:]), nil
}
