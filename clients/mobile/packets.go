package mobile

import (
	"errors"
	"io"
	"sync"
)

const pipeCap = 64

var errQueueFull = errors.New("queue full")

// PacketSink receives IP packets toward the OS. Exclusive with PullPacket.
type PacketSink interface {
	OnPacket(pkt []byte)
}

// packetPipe is a TUN-shaped IP queue: OS Inject/Pull or a PacketSink.
type packetPipe struct {
	in   chan []byte
	out  chan []byte
	mu   sync.Mutex
	dead bool
	sink PacketSink
}

func newPacketPipe() *packetPipe {
	return &packetPipe{
		in:  make(chan []byte, pipeCap),
		out: make(chan []byte, pipeCap),
	}
}

// Name is the fake interface name for gVisor logs.
func (p *packetPipe) Name() string { return "pkt" }

// Read is an IP packet from the OS (InjectPacket).
func (p *packetPipe) Read(b []byte) (int, error) {
	pkt, ok := <-p.in
	if !ok {
		return 0, io.EOF
	}
	return copy(b, pkt), nil
}

// Write is an IP packet toward the OS (PullPacket / PacketSink).
func (p *packetPipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	if p.dead {
		p.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	s := p.sink
	p.mu.Unlock()
	cp := append([]byte(nil), b...)
	if s != nil {
		s.OnPacket(cp)
		return len(b), nil
	}
	select {
	case p.out <- cp:
	default:
	}
	return len(b), nil
}

// Close unblocks Read and Inject.
func (p *packetPipe) Close() error {
	p.mu.Lock()
	if p.dead {
		p.mu.Unlock()
		return nil
	}
	p.dead = true
	close(p.in)
	p.mu.Unlock()
	return nil
}

func (p *packetPipe) setSink(s PacketSink) {
	p.mu.Lock()
	p.sink = s
	p.mu.Unlock()
}

func (p *packetPipe) inject(pkt []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead {
		return io.ErrClosedPipe
	}
	cp := append([]byte(nil), pkt...)
	select {
	case p.in <- cp:
		return nil
	default:
		return errQueueFull
	}
}

func (p *packetPipe) pull() []byte {
	select {
	case pkt := <-p.out:
		return pkt
	default:
		return nil
	}
}
