package main

// SOCKS5 CONNECT client and the GUI TCP probe (HTTP via SOCKS).

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// socksDial CONNECT to hostport through a SOCKS5 proxy at proxyAddr.
func socksDial(ctx context.Context, proxyAddr, hostport string) (net.Conn, error) {
	d := net.Dialer{Timeout: 8 * time.Second}
	c, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		c.Close()
		return nil, err
	}
	var hs [2]byte
	if _, err := io.ReadFull(c, hs[:]); err != nil {
		c.Close()
		return nil, err
	}
	if hs[0] != 0x05 || hs[1] != 0x00 {
		c.Close()
		return nil, fmt.Errorf("socks auth %x", hs)
	}
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		c.Close()
		return nil, err
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		c.Close()
		return nil, err
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port))
	req = append(req, pb[:]...)
	if _, err := c.Write(req); err != nil {
		c.Close()
		return nil, err
	}
	var rep [4]byte
	if _, err := io.ReadFull(c, rep[:]); err != nil {
		c.Close()
		return nil, err
	}
	if rep[0] != 0x05 || rep[1] != 0x00 {
		c.Close()
		return nil, fmt.Errorf("socks connect status=%d", rep[1])
	}
	switch rep[3] {
	case 0x01:
		var rest [6]byte
		if _, err := io.ReadFull(c, rest[:]); err != nil {
			c.Close()
			return nil, err
		}
	case 0x03:
		var n [1]byte
		if _, err := io.ReadFull(c, n[:]); err != nil {
			c.Close()
			return nil, err
		}
		rest := make([]byte, int(n[0])+2)
		if _, err := io.ReadFull(c, rest); err != nil {
			c.Close()
			return nil, err
		}
	case 0x04:
		var rest [18]byte
		if _, err := io.ReadFull(c, rest[:]); err != nil {
			c.Close()
			return nil, err
		}
	default:
		c.Close()
		return nil, fmt.Errorf("socks atyp %d", rep[3])
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	return c, nil
}

// socksCarriesTCP is Priv.loftCarriesTCP: SOCKS CONNECT 1.1.1.1:80 and read HTTP.
func socksCarriesTCP(ctx context.Context, proxyAddr string) (bool, float64, error) {
	t0 := time.Now()
	c, err := socksDial(ctx, proxyAddr, "1.1.1.1:80")
	if err != nil {
		return false, float64(time.Since(t0).Milliseconds()), err
	}
	defer c.Close()
	_, err = c.Write([]byte("HEAD / HTTP/1.1\r\nHost: 1.1.1.1\r\nConnection: close\r\n\r\n"))
	if err != nil {
		return false, float64(time.Since(t0).Milliseconds()), err
	}
	var buf [16]byte
	n, err := c.Read(buf[:])
	ms := float64(time.Since(t0).Milliseconds())
	if err != nil && n == 0 {
		return false, ms, err
	}
	return n > 4 && string(buf[:4]) == "HTTP", ms, nil
}
