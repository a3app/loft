package vless

// XTLS Vision padding (xtls-rprx-vision) over a VLESS TCP stream.

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

var tlsAppStart = []byte{0x17, 0x03, 0x03}

type visionConn struct {
	net.Conn
	uuid [16]byte

	wmu         sync.Mutex
	writeUUID   bool
	padding     bool
	directWrite bool
	filterLeft  int
	tlsInner    bool

	rmu        sync.Mutex
	readUUID   bool
	directRead bool
	remainCont int
	remainPad  int
	cmd        byte
	header     []byte
	headerNeed int
}

func wrapVision(c net.Conn, uuid [16]byte) net.Conn {
	return &visionConn{
		Conn:       c,
		uuid:       uuid,
		writeUUID:  true,
		readUUID:   true,
		padding:    true,
		filterLeft: visionFilter,
		remainCont: -1,
		remainPad:  -1,
	}
}

func (c *visionConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.directWrite {
		return c.Conn.Write(p)
	}
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
			cmd = cmdPadDirect
			c.padding = false
			c.directWrite = true
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
		if cmd == cmdPadDirect || cmd == cmdPadEnd {
			if len(p) > 0 {
				n, err := c.Conn.Write(p)
				return total + n, err
			}
			break
		}
	}
	return total, nil
}

func (c *visionConn) pad(content []byte, cmd byte) []byte {
	n := len(content)
	if n > visionMaxContent {
		n = visionMaxContent
		content = content[:n]
	}
	pad := 0
	if n < 900 {
		if c.tlsInner {
			pad = 900 + int(randN(500)) - n
		} else {
			pad = int(randN(256))
		}
		if pad < 0 {
			pad = 0
		}
		if pad > visionMaxPad {
			pad = visionMaxPad
		}
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

func (c *visionConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.directRead {
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
		if c.cmd == cmdPadDirect || c.cmd == cmdPadEnd {
			c.directRead = true
			continue
		}
		if err := c.readHeader(); err != nil {
			return 0, err
		}
	}
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

func randN(n int) uint32 {
	if n <= 0 {
		return 0
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:]) % uint32(n)
}
