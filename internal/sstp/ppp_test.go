package sstp

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestLCPEchoReplyUsesOwnMagic(t *testing.T) {
	p := newPPP("u", "p", false)
	our := p.magic
	peer := uint32(0x11111111)
	extra := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	var mag [4]byte
	binary.BigEndian.PutUint32(mag[:], peer)
	opts := append(mag[:], extra...)
	info := []byte{cpEchoReq, 7, 0, 0}
	info = append(info, opts...)
	binary.BigEndian.PutUint16(info[2:4], uint16(len(info)))

	if err := p.handleLCP(info); err != nil {
		t.Fatal(err)
	}
	if len(p.out) != 1 {
		t.Fatalf("out %d", len(p.out))
	}
	_, payload, err := parsePPP(p.out[0])
	if err != nil {
		t.Fatal(err)
	}
	if payload[0] != cpEchoRep || payload[1] != 7 {
		t.Fatalf("hdr %x", payload[:2])
	}
	got := binary.BigEndian.Uint32(payload[4:8])
	if got != our {
		t.Fatalf("magic %x want our %x (peer was %x)", got, our, peer)
	}
	if !bytes.Equal(payload[8:], extra) {
		t.Fatalf("extra %x want %x", payload[8:], extra)
	}
}

func TestLCPNakAdoptsMRU(t *testing.T) {
	p := newPPP("u", "p", false)
	opts := []byte{1, 4, 0x05, 0x78} // 1400
	info := []byte{cpConfNak, 1, 0, 0}
	info = append(info, opts...)
	binary.BigEndian.PutUint16(info[2:4], uint16(len(info)))
	if err := p.handleLCP(info); err != nil {
		t.Fatal(err)
	}
	if p.ourMRU != 1400 {
		t.Fatalf("ourMRU %d", p.ourMRU)
	}
	if len(p.out) != 1 {
		t.Fatalf("out %d", len(p.out))
	}
	_, payload, err := parsePPP(p.out[0])
	if err != nil {
		t.Fatal(err)
	}
	if payload[0] != cpConfReq {
		t.Fatalf("code %d", payload[0])
	}
	if payload[4] != 1 || binary.BigEndian.Uint16(payload[6:8]) != 1400 {
		t.Fatalf("req mru %x", payload[4:8])
	}
}

func TestLCPPeerMRU(t *testing.T) {
	p := newPPP("u", "p", false)
	opts := []byte{1, 4, 0x05, 0x78, 5, 6, 0, 0, 0, 1}
	info := []byte{cpConfReq, 2, 0, 0}
	info = append(info, opts...)
	binary.BigEndian.PutUint16(info[2:4], uint16(len(info)))
	if err := p.handleLCP(info); err != nil {
		t.Fatal(err)
	}
	if p.peerMRU != 1400 {
		t.Fatalf("peerMRU %d", p.peerMRU)
	}
	if p.infoMTU() != 1400 {
		t.Fatalf("infoMTU %d", p.infoMTU())
	}
}

func TestLCPRejMRUResetsDefault(t *testing.T) {
	p := newPPP("u", "p", false)
	p.ourMRU = 1400
	opts := []byte{1, 4, 0x05, 0x78}
	info := []byte{cpConfRej, 3, 0, 0}
	info = append(info, opts...)
	binary.BigEndian.PutUint16(info[2:4], uint16(len(info)))
	if err := p.handleLCP(info); err != nil {
		t.Fatal(err)
	}
	if p.ourMRU != 1500 {
		t.Fatalf("ourMRU %d", p.ourMRU)
	}
}

func TestLCPTermAckQueued(t *testing.T) {
	p := newPPP("u", "p", false)
	info := []byte{cpTermReq, 3, 0, 4}
	err := p.handleLCP(info)
	if err == nil || !strings.Contains(err.Error(), "terminate") {
		t.Fatalf("err %v", err)
	}
	if len(p.out) != 1 {
		t.Fatalf("out %d", len(p.out))
	}
	_, payload, err := parsePPP(p.out[0])
	if err != nil {
		t.Fatal(err)
	}
	if payload[0] != cpTermAck || payload[1] != 3 {
		t.Fatalf("termack %x", payload)
	}
}
