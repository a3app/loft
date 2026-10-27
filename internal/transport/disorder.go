package transport

// Disorder (experimental, client side, off by default): send the outer TLS
// ClientHello to the node so that a DPI box on the path never sees the start
// of the SNI (the server name in clear text inside ClientHello).
//
// How it works. The ClientHello bytes are cut in the middle of the server
// name. The first part is sent with IP TTL 1, so that packet dies at the
// first router and never reaches the DPI box or the node. The rest is sent
// at once with the normal TTL. The node's TCP stack sees a gap, does not
// acknowledge the first part, and the client's kernel sends that part again
// (about 200 ms later, or sooner on duplicate ACKs) with the normal TTL,
// because the TTL is restored by then. The node puts the stream back in
// order and reads a normal ClientHello. A DPI box that does not keep
// out-of-order segments sees a stream that starts in the middle of the name.
//
// Why: by late 2026 Russian DPI (TSPU) glues TLS records and TCP segments
// back together before it looks for the SNI, so the older trick of splitting
// ClientHello into small TLS records (canary split_hello) no longer hides
// the name. Disorder is the next step of that arms race; a DPI box that
// buffers reordered segments will defeat it too.
//
// Costs and limits: about 200 ms more on each new TCP connection to the
// node (loft keeps connections long, so this is rare); the TTL-1 packet and
// its resend are themselves a visible pattern; where only listed names are
// allowed (a whitelist), hiding the name may get the connection cut rather
// than passed. QUIC fronts (quic, h3) are UDP and are not touched. When
// disorder is on it replaces split_hello on that connection, because a
// split record would no longer line up with the cut.

import (
	"encoding/binary"
	"errors"
	"net"
	"sync/atomic"
	"time"
)

// disorderOn is the client switch, set from client.yaml (disorder: true) or
// the loft-front -disorder flag.
var disorderOn atomic.Bool

// disorderPause is how long the TTL-1 packet gets to leave the host before
// the TTL is set back. The socket has TCP_NODELAY, so the write goes out at
// once; the pause covers a busy kernel.
const disorderPause = 10 * time.Millisecond

// SetDisorder turns disorder on or off for new TCP connections to the node.
func SetDisorder(on bool) { disorderOn.Store(on) }

// DisorderOn reports whether disorder is on.
func DisorderOn() bool { return disorderOn.Load() }

// helloConn wraps a fresh TCP connection for the first ClientHello write:
// disorder when it is on and the TTL can be changed on this socket,
// otherwise split_hello when the node's canary policy asks for it.
func helloConn(raw net.Conn, pol CanaryPolicy) net.Conn {
	if disorderOn.Load() {
		if t := tcpOf(raw); t != nil {
			return &disorderConn{Conn: raw, setTTL: func(ttl int) (int, error) { return setSockTTL(t, ttl) }}
		}
	}
	if pol.Split {
		return &splitHelloConn{Conn: raw, at: 1}
	}
	return raw
}

// disorderConn sends the first TLS handshake record (the ClientHello) in the
// disorder way described at the top of this file; every later write passes
// through unchanged. setTTL sets the socket TTL and returns the old one; it
// is a field so tests can watch the calls.
type disorderConn struct {
	net.Conn
	used   bool
	setTTL func(ttl int) (old int, err error)
}

// NetConn lets tcpOf reach the TCP socket under the wrapper.
func (d *disorderConn) NetConn() net.Conn { return d.Conn }

// Write cuts the first ClientHello at the middle of its server name and
// sends the first part with TTL 1. If there is no server name, or the TTL
// cannot be changed, the record goes out whole as usual.
func (d *disorderConn) Write(p []byte) (int, error) {
	if d.used || len(p) < 6 || p[0] != 0x16 {
		return d.Conn.Write(p)
	}
	d.used = true
	cut := disorderCut(p)
	if cut <= 0 || cut >= len(p) {
		return d.Conn.Write(p)
	}
	old, err := d.setTTL(1)
	if err != nil || old <= 0 {
		return d.Conn.Write(p)
	}
	n1, err := d.Conn.Write(p[:cut])
	time.Sleep(disorderPause)
	// Every later packet, including the kernel's resend of the first part,
	// must carry the normal TTL; a failed restore would kill the connection
	// quietly, so report it instead.
	if _, rerr := d.setTTL(old); rerr != nil {
		return n1, rerr
	}
	if err != nil {
		return n1, err
	}
	n2, err := d.Conn.Write(p[cut:])
	return n1 + n2, err
}

// errNoSNI means the record is not a ClientHello with a server_name.
var errNoSNI = errors.New("disorder: no server name")

// disorderCut returns where to cut a TLS record that holds a ClientHello:
// the middle of the server name, so the first part holds its start. It
// returns 0 when the record has no server name.
func disorderCut(rec []byte) int {
	off, n, err := sniSpan(rec)
	if err != nil || n == 0 {
		return 0
	}
	half := n / 2
	if half < 1 {
		half = 1
	}
	return off + half
}

// sniSpan finds the host name of the server_name extension in a TLS record
// carrying a ClientHello. It returns the offset of the first name byte in
// rec and the name length.
func sniSpan(rec []byte) (int, int, error) {
	// Record header: type(1) version(2) length(2). Then the handshake
	// header: type(1) = 1 for ClientHello, length(3).
	if len(rec) < 9 || rec[0] != 0x16 || rec[5] != 0x01 {
		return 0, 0, errNoSNI
	}
	// After the handshake header: version(2) random(32) session_id.
	i := 9 + 2 + 32
	if i >= len(rec) {
		return 0, 0, errNoSNI
	}
	i += 1 + int(rec[i]) // session_id
	if i+2 > len(rec) {
		return 0, 0, errNoSNI
	}
	i += 2 + int(binary.BigEndian.Uint16(rec[i:])) // cipher suites
	if i+1 > len(rec) {
		return 0, 0, errNoSNI
	}
	i += 1 + int(rec[i]) // compression methods
	if i+2 > len(rec) {
		return 0, 0, errNoSNI
	}
	end := i + 2 + int(binary.BigEndian.Uint16(rec[i:]))
	if end > len(rec) {
		end = len(rec)
	}
	i += 2
	for i+4 <= end {
		typ := binary.BigEndian.Uint16(rec[i:])
		n := int(binary.BigEndian.Uint16(rec[i+2:]))
		body := i + 4
		if body+n > end {
			return 0, 0, errNoSNI
		}
		if typ == 0 {
			// server_name: list length(2), then name type(1) = 0 for a
			// host name, name length(2), name bytes.
			if n < 5 || rec[body+2] != 0 {
				return 0, 0, errNoSNI
			}
			nameLen := int(binary.BigEndian.Uint16(rec[body+3:]))
			if body+5+nameLen > body+n {
				return 0, 0, errNoSNI
			}
			return body + 5, nameLen, nil
		}
		i = body + n
	}
	return 0, 0, errNoSNI
}
