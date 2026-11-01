package trojan

// Trojan UDP ASSOCIATE as net.PacketConn.

import (
	"io"
	"net"
	"sync"
	"time"
)

// udpConn turns a UDP ASSOCIATE stream into a net.PacketConn. Separate
// locks for reading and writing keep each datagram whole on the stream.
type udpConn struct {
	net.Conn
	c     *Client
	local net.Addr
	rmu   sync.Mutex
	wmu   sync.Mutex
}

// ReadFrom returns the next datagram. If p is too small the rest is
// dropped and io.ErrShortBuffer returned, the stream stays in step.
func (c *udpConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	addr, payload, err := readUDPPacket(c.Conn)
	if err != nil {
		return 0, nil, err
	}
	n := copy(p, payload)
	if n > 0 {
		c.c.down.Add(uint64(n))
	}
	if n < len(payload) {
		return n, addr, io.ErrShortBuffer
	}
	return n, addr, nil
}

func (c *udpConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if addr == nil {
		return 0, net.InvalidAddrError("trojan udp: nil addr")
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := writeUDPPacket(c.Conn, addr, p); err != nil {
		return 0, err
	}
	c.c.up.Add(uint64(len(p)))
	return len(p), nil
}

func (c *udpConn) LocalAddr() net.Addr { return c.local }

func (c *udpConn) Close() error { return c.Conn.Close() }

func (c *udpConn) SetDeadline(t time.Time) error      { return c.Conn.SetDeadline(t) }
func (c *udpConn) SetReadDeadline(t time.Time) error  { return c.Conn.SetReadDeadline(t) }
func (c *udpConn) SetWriteDeadline(t time.Time) error { return c.Conn.SetWriteDeadline(t) }
