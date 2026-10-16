package main

// Bench origin protocol on the node loopback: echo, download, upload.

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const (
	originEcho = 'E'
	originDown = 'D'
	originUp   = 'U'
)

// serveOrigin accepts bench connections until the process is killed.
func serveOrigin(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	fmt.Fprintf(os.Stderr, "loft-bench origin %s\n", ln.Addr())
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleOrigin(c)
	}
}

func tuneOrigin(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetReadBuffer(2 << 20)
		_ = tc.SetWriteBuffer(2 << 20)
	}
}

// handleOrigin runs one E/D/U command then closes.
func handleOrigin(c net.Conn) {
	defer c.Close()
	tuneOrigin(c)
	_ = c.SetDeadline(time.Now().Add(2 * time.Minute))
	var cmd [1]byte
	if _, err := io.ReadFull(c, cmd[:]); err != nil {
		return
	}
	switch cmd[0] {
	case originEcho:
		var b [1]byte
		if _, err := io.ReadFull(c, b[:]); err != nil {
			return
		}
		_, _ = c.Write(b[:])
	case originDown:
		n, err := readU32(c)
		if err != nil {
			return
		}
		buf := make([]byte, 64<<10)
		var sent int
		for sent < n {
			chunk := len(buf)
			if chunk > n-sent {
				chunk = n - sent
			}
			k, err := c.Write(buf[:chunk])
			sent += k
			if err != nil {
				return
			}
		}
	case originUp:
		n, err := readU32(c)
		if err != nil {
			return
		}
		got, _ := io.CopyN(io.Discard, c, int64(n))
		_ = putU32(c, uint32(got))
	}
}

// originEchoRTT writes E+byte and reads it back.
func originEchoRTT(c net.Conn) (time.Duration, error) {
	t0 := time.Now()
	if _, err := c.Write([]byte{originEcho, 'x'}); err != nil {
		return 0, err
	}
	var b [1]byte
	if _, err := io.ReadFull(c, b[:]); err != nil {
		return 0, err
	}
	if b[0] != 'x' {
		return 0, fmt.Errorf("echo %q", b[0])
	}
	return time.Since(t0), nil
}

// originDownload asks the origin to send n bytes.
func originDownload(c net.Conn, n int) (time.Duration, error) {
	if err := writeCmdSize(c, originDown, n); err != nil {
		return 0, err
	}
	t0 := time.Now()
	got, err := io.CopyN(io.Discard, c, int64(n))
	if err != nil {
		return time.Since(t0), fmt.Errorf("down %d of %d: %w", got, n, err)
	}
	if int(got) != n {
		return time.Since(t0), fmt.Errorf("down %d of %d", got, n)
	}
	return time.Since(t0), nil
}

// originUpload sends n bytes and waits for the origin's received count.
func originUpload(c net.Conn, n int) (time.Duration, error) {
	if err := writeCmdSize(c, originUp, n); err != nil {
		return 0, err
	}
	t0 := time.Now()
	payload := make([]byte, 64<<10)
	left := n
	for left > 0 {
		chunk := len(payload)
		if chunk > left {
			chunk = left
		}
		k, err := c.Write(payload[:chunk])
		left -= k
		if err != nil {
			return time.Since(t0), err
		}
	}
	got, err := readU32(c)
	if err != nil {
		return time.Since(t0), err
	}
	if got != n {
		return time.Since(t0), fmt.Errorf("origin got %d of %d", got, n)
	}
	return time.Since(t0), nil
}

func writeCmdSize(c net.Conn, cmd byte, n int) error {
	var hdr [5]byte
	hdr[0] = cmd
	binary.BigEndian.PutUint32(hdr[1:], uint32(n))
	_, err := c.Write(hdr[:])
	return err
}

func readU32(r io.Reader) (int, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint32(b[:])), nil
}

func putU32(w io.Writer, n uint32) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	_, err := w.Write(b[:])
	return err
}
