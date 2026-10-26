package vless

// XTLS Vision padding (xtls-rprx-vision) over a VLESS TCP stream.
//
// Why: most traffic inside the tunnel is itself TLS (HTTPS). A TLS
// handshake carried inside another TLS connection still leaks its typical
// record sizes, and censors look for exactly that. Vision wraps the first
// writes in frames with random padding. Once the inner TLS reaches
// application data (records starting 17 03 03), padding stops with command
// end and the rest of the stream stays on the outer TLS conn. Command
// direct is not sent: Xray reads it as "splice the TCP under REALITY",
// and bytes still written as TLS records arrive at the site as ciphertext.
// The site answers bad_record_mac (Firefox SSL_ERROR_BAD_MAC_ALERT).
// If the inner traffic is not TLS, padding stops after the first few
// writes, also with end.
//
// The server does send direct once it has seen a TLS 1.3 application
// record from the site. After that frame the rest of the downlink is on
// the raw socket. Read switches there. The record gate under the outer
// TLS conn keeps those bytes out of uTLS's buffer.
//
// A frame is: the UUID (first frame only), a command byte (continue, end,
// direct), the content length and the padding length (2 bytes each), the
// content, then random padding.

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

const (
	cmdPadContinue byte = 0x00
	cmdPadEnd      byte = 0x01
	cmdPadDirect   byte = 0x02
	visionFilter   int  = 8
	// One padded frame of application data. Content and pad lengths on
	// the wire are uint16. The allocation cap is a constant so it is
	// not computed from len(content).
	visionMaxContent = 8192
	visionMaxPad     = 900 + 499
	visionHeadMax    = 5 + 16
	visionFrameCap   = visionHeadMax + visionMaxContent + visionMaxPad
)

// tlsAppStart begins a TLS 1.2/1.3 application data record.
var tlsAppStart = []byte{0x17, 0x03, 0x03}

// visionConn is a VLESS stream with Vision framing on both directions. The
// write side looks at the first visionFilter writes to see whether the
// inner traffic is TLS (a handshake record starts 16 03); the read side
// strips frames until the server says end or direct.
type visionConn struct {
	net.Conn
	uuid [16]byte

	wmu        sync.Mutex
	writeUUID  bool
	padding    bool
	filterLeft int
	tlsInner   bool

	rmu        sync.Mutex
	readUUID   bool
	plainRead  bool
	directRead bool
	rawHead    []byte
	raw        net.Conn
	remainCont int
	remainPad  int
	cmd        byte
}

func wrapVision(c net.Conn, uuid [16]byte) net.Conn {
	return newVision(c, uuid, true)
}

// wrapVisionContinue is wrapVision after the UUID frame was already written
// with the VLESS header. The next frame must not repeat the UUID: Xray's
// unpadder only looks for it in the initial state.
func wrapVisionContinue(c net.Conn, uuid [16]byte) net.Conn {
	return newVision(c, uuid, false)
}

func newVision(c net.Conn, uuid [16]byte, writeUUID bool) net.Conn {
	return &visionConn{
		Conn:       c,
		uuid:       uuid,
		writeUUID:  writeUUID,
		readUUID:   true,
		padding:    true,
		filterLeft: visionFilter,
		remainCont: -1,
		remainPad:  -1,
	}
}

// visionHello is the empty long-padding frame Xray writes with the request
// header (command continue, no content, UUID once). tlsInner is set only
// here so the padding is the long one; the caller's visionConn still
// learns TLS from the first real payload.
func visionHello(uuid [16]byte) []byte {
	c := &visionConn{uuid: uuid, writeUUID: true, tlsInner: true}
	return c.pad(nil, cmdPadContinue)
}

// Write sends p in padded frames of up to 8 KiB of content. On the first
// inner TLS application record it sends that chunk with command end and
// the rest of p as ordinary TLS payload. Without inner TLS, the last chunk
// of a write after the filter window carries end. Padding stays off after
// either. Command direct is never written: see the package comment.
func (c *visionConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.filterLeft > 0 && len(p) > 0 {
		c.filterLeft--
		if len(p) >= 3 && p[0] == 0x16 && p[1] == 0x03 {
			c.tlsInner = true
		}
	}
	if !c.padding {
		return c.Conn.Write(p)
	}
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > visionMaxContent {
			chunk = chunk[:visionMaxContent]
		}
		cmd := cmdPadContinue
		last := len(p) <= visionMaxContent
		if c.tlsInner && len(chunk) >= 3 && chunk[0] == tlsAppStart[0] && chunk[1] == tlsAppStart[1] && chunk[2] == tlsAppStart[2] {
			cmd = cmdPadEnd
			c.padding = false
			last = true
		} else if last && c.filterLeft <= 0 && !c.tlsInner {
			cmd = cmdPadEnd
			c.padding = false
		}
		frame := c.pad(chunk, cmd)
		if _, err := c.Conn.Write(frame); err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
		if cmd == cmdPadEnd {
			if len(p) > 0 {
				n, err := c.Conn.Write(p)
				return total + n, err
			}
			break
		}
	}
	return total, nil
}

// pad builds one frame. Short inner-TLS content is padded out to 900–1399
// bytes so handshake records all look alike. Every other frame gets 0–255
// random bytes, including content of 900 bytes or more, which is what
// Xray's XtlsPadding does in its else branch. A full 8 KiB chunk has no
// room left, so that one stays unpadded.
func (c *visionConn) pad(content []byte, cmd byte) []byte {
	n := len(content)
	if n > visionMaxContent {
		n = visionMaxContent
		content = content[:n]
	}
	pad := 0
	if n < 900 && c.tlsInner {
		pad = 900 + int(randN(500)) - n
	} else {
		pad = int(randN(256))
	}
	if pad < 0 {
		pad = 0
	}
	if room := visionMaxContent - n; pad > room {
		pad = room
	}
	if pad > visionMaxPad {
		pad = visionMaxPad
	}
	out := make([]byte, 0, visionFrameCap)
	if c.writeUUID {
		out = append(out, c.uuid[:]...)
		c.writeUUID = false
	}
	out = append(out, cmd)
	var lens [4]byte
	binary.BigEndian.PutUint16(lens[0:2], uint16(n))
	binary.BigEndian.PutUint16(lens[2:4], uint16(pad))
	out = append(out, lens[:]...)
	out = append(out, content[:n]...)
	if pad > 0 {
		var z [visionMaxPad]byte
		_, _ = rand.Read(z[:pad])
		out = append(out, z[:pad]...)
	}
	return out
}

// Read returns content from the server's frames and skips their padding.
// Command end leaves the rest of the stream on the outer TLS conn.
// Command direct is the server's splice: the rest is on the raw socket.
func (c *visionConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.directRead {
			return c.readRaw(p)
		}
		if c.plainRead {
			return c.Conn.Read(p)
		}
		if c.remainCont > 0 {
			if len(p) > c.remainCont {
				p = p[:c.remainCont]
			}
			n, err := c.Conn.Read(p)
			c.remainCont -= n
			if c.remainCont == 0 && c.remainPad > 0 {
				_ = c.skipPad()
			}
			return n, err
		}
		if c.remainPad > 0 {
			if err := c.skipPad(); err != nil {
				return 0, err
			}
			continue
		}
		if c.cmd == cmdPadEnd {
			c.plainRead = true
			continue
		}
		if c.cmd == cmdPadDirect {
			c.rawHead, c.raw = unwrapSplice(c.Conn)
			c.directRead = true
			continue
		}
		if err := c.readHeader(); err != nil {
			return 0, err
		}
	}
}

// readRaw is the downlink after the server's command direct. head is
// whatever the record gate had already pulled off the socket.
func (c *visionConn) readRaw(p []byte) (int, error) {
	if len(c.rawHead) > 0 {
		n := copy(p, c.rawHead)
		c.rawHead = c.rawHead[n:]
		return n, nil
	}
	if c.raw != nil {
		return c.raw.Read(p)
	}
	return c.Conn.Read(p)
}

// splicer is xtls.RecordGate. Release stops TLS-record framing.
type splicer interface {
	Release() (head []byte, raw net.Conn)
}

// unwrapSplice walks NetConn until it finds the record gate under REALITY.
// No gate (security=none, or a test pipe) means the caller keeps reading
// the conn it already has.
func unwrapSplice(c net.Conn) (head []byte, raw net.Conn) {
	seen := map[net.Conn]struct{}{}
	for c != nil {
		if _, ok := seen[c]; ok {
			return nil, nil
		}
		seen[c] = struct{}{}
		if s, ok := c.(splicer); ok {
			return s.Release()
		}
		n, ok := c.(interface{ NetConn() net.Conn })
		if !ok {
			return nil, nil
		}
		next := n.NetConn()
		if next == nil || next == c {
			return nil, nil
		}
		c = next
	}
	return nil, nil
}

func (c *visionConn) skipPad() error {
	for c.remainPad > 0 {
		var buf [512]byte
		n := c.remainPad
		if n > len(buf) {
			n = len(buf)
		}
		got, err := io.ReadFull(c.Conn, buf[:n])
		c.remainPad -= got
		if err != nil {
			return err
		}
	}
	return nil
}

// readHeader reads the next frame header; the server's first frame starts
// with the UUID too.
func (c *visionConn) readHeader() error {
	need := 5
	if c.readUUID {
		need += 16
	}
	hdr := make([]byte, need)
	if _, err := io.ReadFull(c.Conn, hdr); err != nil {
		return err
	}
	off := 0
	if c.readUUID {
		off = 16
		c.readUUID = false
	}
	c.cmd = hdr[off]
	c.remainCont = int(binary.BigEndian.Uint16(hdr[off+1 : off+3]))
	c.remainPad = int(binary.BigEndian.Uint16(hdr[off+3 : off+5]))
	if c.cmd > cmdPadDirect {
		return fmt.Errorf("vless vision: command %d", c.cmd)
	}
	return nil
}

// randN is a random number in [0, n) from crypto/rand. The slight modulo
// bias does not matter for padding lengths.
func randN(n int) uint32 {
	if n <= 0 {
		return 0
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:]) % uint32(n)
}
