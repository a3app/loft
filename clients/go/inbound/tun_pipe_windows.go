//go:build windows

package inbound

// tun_pipe_windows.go is the client side of the Windows packet pipe. On
// Windows the loft service owns the Wintun adapter (the virtual network
// card) and runs with admin rights; loft-front runs without them and gets
// IP packets from the service through a named pipe. Each packet travels as
// a 4-byte little-endian length followed by the packet bytes.

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

// packetPipe is one IP datagram per length-prefixed record. The loft
// service owns Wintun; loft-front only sees packets.
type packetPipe struct {
	f    *os.File
	name string
	hold []byte
}

// OpenPacketPipe dials a pipe the service is already listening on.
func OpenPacketPipe(name string) (tunIO, error) {
	if name == "" {
		return nil, fmt.Errorf("tun pipe: empty")
	}
	var last error
	deadline := time.Now().Add(15 * time.Second)
	for {
		f, err := os.OpenFile(name, os.O_RDWR, 0)
		if err == nil {
			return &packetPipe{f: f, name: "Loft"}, nil
		}
		last = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("tun pipe: %w", last)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Name is the interface name reported to the rest of the client ("Loft").
func (p *packetPipe) Name() string { return p.name }

// Close closes the pipe; the service sees the end of the stream.
func (p *packetPipe) Close() error { return p.f.Close() }

// Read returns one IP packet. It reads the length header, then the packet
// into an internal buffer, and copies it to b. A zero length means the
// service closed the pipe on purpose (io.EOF). A length outside 20..65535
// means the stream is broken, since no IPv4 packet is shorter than its
// 20-byte header.
func (p *packetPipe) Read(b []byte) (int, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(p.f, hdr[:]); err != nil {
		return 0, err
	}
	n := int(binary.LittleEndian.Uint32(hdr[:]))
	if n == 0 {
		return 0, io.EOF
	}
	if n < 20 || n > 65535 {
		return 0, fmt.Errorf("tun pipe: len %d", n)
	}
	if cap(p.hold) < n {
		p.hold = make([]byte, n)
	}
	p.hold = p.hold[:n]
	if _, err := io.ReadFull(p.f, p.hold); err != nil {
		return 0, err
	}
	if n > len(b) {
		return 0, fmt.Errorf("tun pipe: short buf %d need %d", len(b), n)
	}
	copy(b, p.hold)
	return n, nil
}

// Write sends one IP packet to the service: the length header, then b.
func (p *packetPipe) Write(b []byte) (int, error) {
	if len(b) == 0 || len(b) > 65535 {
		return 0, fmt.Errorf("tun pipe: write %d", len(b))
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := p.f.Write(hdr[:]); err != nil {
		return 0, err
	}
	if _, err := p.f.Write(b); err != nil {
		return 0, err
	}
	return len(b), nil
}
