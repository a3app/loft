package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// SOCKS5 (RFC 1928, RFC 1929) and HTTP CONNECT clients. under dials the
// proxy itself (directly, or through the channel named in via).

type dialFn func(ctx context.Context, network, addr string) (net.Conn, error)

func socksAddr(target string) ([]byte, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 0 || p > 65535 {
		return nil, fmt.Errorf("bad port in %q", target)
	}
	var b []byte
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			b = append([]byte{1}, v4...)
		} else {
			b = append([]byte{4}, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, errors.New("host name too long")
		}
		b = append([]byte{3, byte(len(host))}, host...)
	}
	return binary.BigEndian.AppendUint16(b, uint16(p)), nil
}

// socksHandshake greets, authenticates and sends one command (1 CONNECT, 3 UDP ASSOCIATE).
func socksHandshake(c net.Conn, user, pass string, cmd byte, target string) (bound string, err error) {
	methods := []byte{5, 1, 0}
	if user != "" {
		methods = []byte{5, 2, 0, 2}
	}
	if _, err := c.Write(methods); err != nil {
		return "", err
	}
	var r [2]byte
	if _, err := io.ReadFull(c, r[:]); err != nil {
		return "", err
	}
	if r[0] != 5 {
		return "", errors.New("not a SOCKS5 proxy")
	}
	switch r[1] {
	case 0:
	case 2:
		if user == "" {
			return "", errors.New("the proxy wants a login")
		}
		a := append([]byte{1, byte(len(user))}, user...)
		a = append(append(a, byte(len(pass))), pass...)
		if _, err := c.Write(a); err != nil {
			return "", err
		}
		if _, err := io.ReadFull(c, r[:]); err != nil {
			return "", err
		}
		if r[1] != 0 {
			return "", errors.New("the proxy refused the login")
		}
	default:
		return "", errors.New("the proxy accepts no method we offer")
	}
	addr, err := socksAddr(target)
	if err != nil {
		return "", err
	}
	if _, err := c.Write(append([]byte{5, cmd, 0}, addr...)); err != nil {
		return "", err
	}
	var h [4]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return "", err
	}
	if h[1] != 0 {
		return "", fmt.Errorf("the proxy answered %d (%s)", h[1], socksReply(h[1]))
	}
	return readSocksAddr(c, h[3])
}

func socksReply(code byte) string {
	return map[byte]string{1: "general failure", 2: "not allowed", 3: "network unreachable", 4: "host unreachable",
		5: "connection refused", 6: "TTL expired", 7: "command not supported", 8: "address type not supported"}[code]
}

func readSocksAddr(r io.Reader, atyp byte) (string, error) {
	var host string
	switch atyp {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		host = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		host = net.IP(b).String()
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return "", err
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		host = string(b)
	default:
		return "", errors.New("bad address type from the proxy")
	}
	var p [2]byte
	if _, err := io.ReadFull(r, p[:]); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(p[:])))), nil
}

func socksDial(ctx context.Context, under dialFn, ch *Channel, target string) (net.Conn, error) {
	c, err := under(ctx, "tcp", ch.Addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	if _, err := socksHandshake(c, ch.User, ch.Pass, 1, target); err != nil {
		c.Close()
		return nil, err
	}
	c.SetDeadline(time.Time{})
	return c, nil
}

func httpDial(ctx context.Context, under dialFn, ch *Channel, target string) (net.Conn, error) {
	c, err := under(ctx, "tcp", ch.Addr)
	if err != nil {
		return nil, err
	}
	if ch.TLS {
		host, _, _ := net.SplitHostPort(ch.Addr)
		tc := tls.Client(c, &tls.Config{ServerName: host})
		if err := tc.HandshakeContext(ctx); err != nil {
			c.Close()
			return nil, err
		}
		c = tc
	}
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if ch.User != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(ch.User+":"+ch.Pass)) + "\r\n"
	}
	if _, err := io.WriteString(c, req+"\r\n"); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	// A 2xx to CONNECT has no body: what follows is the tunnel.
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		c.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.Close()
		return nil, fmt.Errorf("the proxy answered %s", resp.Status)
	}
	c.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufConn{Conn: c, r: br}, nil
	}
	return c, nil
}

type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// socksUDP is a UDP ASSOCIATE: datagrams to and from relay carry a SOCKS
// header; the association lives while ctl stays open.
type socksUDP struct {
	ctl   net.Conn
	pc    net.PacketConn
	relay net.Addr
	once  sync.Once
}

func socksAssociate(ctx context.Context, under dialFn, ch *Channel) (*socksUDP, error) {
	ctl, err := under(ctx, "tcp", ch.Addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		ctl.SetDeadline(dl)
	}
	bound, err := socksHandshake(ctl, ch.User, ch.Pass, 3, "0.0.0.0:0")
	if err != nil {
		ctl.Close()
		return nil, err
	}
	ctl.SetDeadline(time.Time{})
	host, port, _ := net.SplitHostPort(bound)
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		// The relay listens on the proxy's own address.
		phost, _, _ := net.SplitHostPort(ch.Addr)
		host = phost
	}
	ra, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, port))
	if err != nil {
		ctl.Close()
		return nil, err
	}
	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		ctl.Close()
		return nil, err
	}
	u := &socksUDP{ctl: ctl, pc: pc, relay: ra}
	go func() { io.Copy(io.Discard, ctl); u.Close() }() // the proxy ends the association by closing ctl
	return u, nil
}

func (u *socksUDP) WriteTo(p []byte, target string) error {
	a, err := socksAddr(target)
	if err != nil {
		return err
	}
	_, err = u.pc.WriteTo(append(append([]byte{0, 0, 0}, a...), p...), u.relay)
	return err
}

// ReadFrom returns one datagram and the address it came from.
func (u *socksUDP) ReadFrom(buf []byte) (int, string, error) {
	tmp := make([]byte, 65535)
	for {
		n, _, err := u.pc.ReadFrom(tmp)
		if err != nil {
			return 0, "", err
		}
		if n < 4 || tmp[2] != 0 { // fragments are not supported
			continue
		}
		r := &byteReader{b: tmp[4:n]}
		from, err := readSocksAddr(r, tmp[3])
		if err != nil {
			continue
		}
		return copy(buf, r.b), from, nil
	}
}

func (u *socksUDP) Close() error {
	u.once.Do(func() { u.ctl.Close(); u.pc.Close() })
	return nil
}

type byteReader struct{ b []byte }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}
