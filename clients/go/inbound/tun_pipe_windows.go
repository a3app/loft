//go:build windows

package inbound

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

func (p *packetPipe) Name() string { return p.name }

func (p *packetPipe) Close() error { return p.f.Close() }

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
