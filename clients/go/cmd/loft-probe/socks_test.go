// socks_test.go covers SOCKS5 CONNECT through a stub proxy and UDP header encode.
package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// TestSocksConnectEcho CONNECTs through a stub SOCKS to an echo server.
func TestSocksConnectEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go io.Copy(c, c)
		}
	}()
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	go serveTestSOCKS(proxy, ln.Addr().String())

	v, err := openSocks(proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer v.close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := v.dialTCP(ctx, "example.com:80")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("%q", buf)
	}
}

// TestSocksUDPEncode round-trips a SOCKS5 UDP header.
func TestSocksUDPEncode(t *testing.T) {
	b, err := encodeSocksUDP("1.2.3.4", 53, []byte("dns"))
	if err != nil {
		t.Fatal(err)
	}
	host, port, data, err := parseSocksUDP(b)
	if err != nil || host != "1.2.3.4" || port != 53 || string(data) != "dns" {
		t.Fatalf("%s %d %q %v", host, port, data, err)
	}
}

// serveTestSOCKS is a no-auth SOCKS5 CONNECT that dials backend.
func serveTestSOCKS(ln net.Listener, backend string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			hdr := make([]byte, 2)
			if _, err := io.ReadFull(c, hdr); err != nil {
				return
			}
			n := int(hdr[1])
			if n > 0 {
				_, _ = io.ReadFull(c, make([]byte, n))
			}
			_, _ = c.Write([]byte{0x05, 0x00})
			req := make([]byte, 4)
			if _, err := io.ReadFull(c, req); err != nil {
				return
			}
			if err := drainAtyp(c, req[3]); err != nil {
				return
			}
			remote, err := net.Dial("tcp", backend)
			if err != nil {
				_, _ = c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
				return
			}
			defer remote.Close()
			_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})
			errc := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(remote, c); errc <- struct{}{} }()
			go func() { _, _ = io.Copy(c, remote); errc <- struct{}{} }()
			<-errc
		}(c)
	}
}

// drainAtyp discards the SOCKS5 address after the request header.
func drainAtyp(c net.Conn, atyp byte) error {
	switch atyp {
	case 0x01:
		_, err := io.ReadFull(c, make([]byte, 6))
		return err
	case 0x03:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(c, lb); err != nil {
			return err
		}
		_, err := io.ReadFull(c, make([]byte, int(lb[0])+2))
		return err
	case 0x04:
		_, err := io.ReadFull(c, make([]byte, 18))
		return err
	default:
		return io.ErrUnexpectedEOF
	}
}

// TestSocksAddrPort checks IPv4 CONNECT encoding of 8.8.8.8:53.
func TestSocksAddrPort(t *testing.T) {
	b, err := socksAddr(0x01, "8.8.8.8", 53)
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != 5 || b[1] != 1 || b[3] != 1 {
		t.Fatalf("%x", b)
	}
	if binary.BigEndian.Uint16(b[8:10]) != 53 {
		t.Fatal("port")
	}
}
