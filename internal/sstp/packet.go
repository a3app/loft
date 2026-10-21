package sstp

// MS-SSTP framing: HTTPS after the duplex POST carries these packets.

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	ver                = 0x10
	msgConnectRequest  = 1
	msgConnectAck      = 2
	msgConnectNak      = 3
	msgConnected       = 4
	msgAbort           = 5
	msgDisconnect      = 6
	msgDisconnectAck   = 7
	msgEchoRequest     = 8
	msgEchoResponse    = 9
	attrEncapsulated   = 1
	attrStatusInfo     = 2
	attrCryptoBinding  = 3
	attrCryptoBindingReq = 4
	protoPPP           = 1
	hashSHA1           = 1
	hashSHA256         = 2
	// MS-SSTP / SSTP Connect / sstp-client URI. The EAADCD2F variant is a
	// common typo; Keenetic answers 200 only on E0ADCD75.
	duplexPath = "/sra_{BA195980-CD49-458b-9E23-C84EE0ADCD75}/"
)

// pkt is one SSTP control or data packet.
type pkt struct {
	ctrl bool
	typ  uint16
	attr []attr
	data []byte
}

type attr struct {
	id   byte
	val  []byte
}

func (p pkt) encode() []byte {
	if !p.ctrl {
		n := 4 + len(p.data)
		b := make([]byte, n)
		b[0] = ver
		b[1] = 0
		binary.BigEndian.PutUint16(b[2:4], uint16(n))
		copy(b[4:], p.data)
		return b
	}
	body := make([]byte, 4)
	binary.BigEndian.PutUint16(body[0:2], p.typ)
	binary.BigEndian.PutUint16(body[2:4], uint16(len(p.attr)))
	for _, a := range p.attr {
		al := 4 + len(a.val)
		ah := []byte{0, a.id, byte(al >> 8), byte(al)}
		body = append(body, ah...)
		body = append(body, a.val...)
	}
	n := 4 + len(body)
	b := make([]byte, n)
	b[0] = ver
	b[1] = 1
	binary.BigEndian.PutUint16(b[2:4], uint16(n))
	copy(b[4:], body)
	return b
}

func readPkt(r io.Reader) (pkt, error) {
	h := make([]byte, 4)
	if _, err := io.ReadFull(r, h); err != nil {
		return pkt{}, err
	}
	if h[0] != ver {
		return pkt{}, fmt.Errorf("sstp version 0x%02x", h[0])
	}
	n := int(binary.BigEndian.Uint16(h[2:4]))
	if n < 4 || n > 16384 {
		return pkt{}, fmt.Errorf("sstp length %d", n)
	}
	rest := make([]byte, n-4)
	if n > 4 {
		if _, err := io.ReadFull(r, rest); err != nil {
			return pkt{}, err
		}
	}
	if h[1]&1 == 0 {
		return pkt{data: rest}, nil
	}
	if len(rest) < 4 {
		return pkt{}, fmt.Errorf("sstp control short")
	}
	p := pkt{ctrl: true, typ: binary.BigEndian.Uint16(rest[0:2])}
	na := int(binary.BigEndian.Uint16(rest[2:4]))
	off := 4
	for i := 0; i < na; i++ {
		if off+4 > len(rest) {
			return pkt{}, fmt.Errorf("sstp attr")
		}
		id := rest[off+1]
		al := int(binary.BigEndian.Uint16(rest[off+2 : off+4]))
		if al < 4 || off+al > len(rest) {
			return pkt{}, fmt.Errorf("sstp attr len")
		}
		p.attr = append(p.attr, attr{id: id, val: append([]byte(nil), rest[off+4:off+al]...)})
		off += al
	}
	return p, nil
}

func connectRequest() pkt {
	return pkt{ctrl: true, typ: msgConnectRequest, attr: []attr{{
		id:  attrEncapsulated,
		val: []byte{0, protoPPP},
	}}}
}

func echoResponse() pkt { return pkt{ctrl: true, typ: msgEchoResponse} }

func echoRequest() pkt  { return pkt{ctrl: true, typ: msgEchoRequest} }

func disconnectPkt() pkt { return pkt{ctrl: true, typ: msgDisconnect} }

func attrByID(p pkt, id byte) []byte {
	for _, a := range p.attr {
		if a.id == id {
			return a.val
		}
	}
	return nil
}
