package transport

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestL3InetTCPEcho(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(c, c)
	}()
	addr := ln.Addr().(*net.TCPAddr)
	src := net.IPv4(10, 8, 0, 2)
	dst := addr.IP
	replies := make(chan []byte, 8)
	reply := func(p []byte) error {
		select {
		case replies <- append([]byte(nil), p...):
		default:
		}
		return nil
	}
	syn := l3TCPSyn(src, dst, 40000, uint16(addr.Port), 1000)
	l3Inet(reply, src, syn)
	var synack []byte
	select {
	case synack = <-replies:
	case <-time.After(2 * time.Second):
		t.Fatal("no syn-ack")
	}
	ihl := int(synack[0]&0x0f) * 4
	if synack[ihl+13]&tcpSYN == 0 || synack[ihl+13]&tcpACK == 0 {
		t.Fatalf("flags %x", synack[ihl+13])
	}
	iss := uint32(1000)
	ackn := be32(synack[ihl+4 : ihl+8])
	ack := l3TCPAck(src, dst, 40000, uint16(addr.Port), iss+1, ackn+1)
	l3Inet(reply, src, ack)
	payload := []byte("hello-l3")
	data := l3TCPData(src, dst, 40000, uint16(addr.Port), iss+1, ackn+1, payload)
	l3Inet(reply, src, data)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-replies:
			th := p[int(p[0]&0x0f)*4:]
			off := int(th[12]>>4) * 4
			if off < 20 || off > len(th) {
				continue
			}
			if string(th[off:]) == "hello-l3" {
				return
			}
		case <-deadline:
			t.Fatal("no echo")
		}
	}
}

func TestL3TCPSynAckOptions(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		time.Sleep(50 * time.Millisecond)
	}()
	addr := ln.Addr().(*net.TCPAddr)
	src := net.IPv4(10, 8, 0, 2)
	replies := make(chan []byte, 4)
	reply := func(p []byte) error {
		select {
		case replies <- append([]byte(nil), p...):
		default:
		}
		return nil
	}
	syn := l3TCPSynWScale(src, addr.IP, 40001, uint16(addr.Port), 1000, 7)
	l3Inet(reply, src, syn)
	var synack []byte
	select {
	case synack = <-replies:
	case <-time.After(2 * time.Second):
		t.Fatal("no syn-ack")
	}
	ihl := int(synack[0]&0x0f) * 4
	th := synack[ihl:]
	off := int(th[12]>>4) * 4
	if off < 28 {
		t.Fatalf("hdr %d", off)
	}
	if tcpWScale(th, off) != l3WScale {
		t.Fatalf("wscale %d", tcpWScale(th, off))
	}
	mss := tcpMSS(th, off)
	if mss != l3MSS {
		t.Fatalf("mss %d", mss)
	}
}

func be32(p []byte) uint32 {
	return uint32(p[0])<<24 | uint32(p[1])<<16 | uint32(p[2])<<8 | uint32(p[3])
}

func l3TCPSyn(src, dst net.IP, sport, dport uint16, seq uint32) []byte {
	th := make([]byte, 20)
	put16(th[0:2], sport)
	put16(th[2:4], dport)
	put32(th[4:8], seq)
	th[12] = 5 << 4
	th[13] = tcpSYN
	put16(th[14:16], 65535)
	pkt := encapsulateIPv4(src, dst, ipProtoTCP, th)
	sum := transportChecksum(src, dst, ipProtoTCP, pkt[20:])
	put16(pkt[20+16:20+18], sum)
	return pkt
}

func l3TCPAck(src, dst net.IP, sport, dport uint16, seq, ack uint32) []byte {
	th := make([]byte, 20)
	put16(th[0:2], sport)
	put16(th[2:4], dport)
	put32(th[4:8], seq)
	put32(th[8:12], ack)
	th[12] = 5 << 4
	th[13] = tcpACK
	put16(th[14:16], 65535)
	pkt := encapsulateIPv4(src, dst, ipProtoTCP, th)
	sum := transportChecksum(src, dst, ipProtoTCP, pkt[20:])
	put16(pkt[20+16:20+18], sum)
	return pkt
}

func l3TCPData(src, dst net.IP, sport, dport uint16, seq, ack uint32, payload []byte) []byte {
	th := make([]byte, 20+len(payload))
	put16(th[0:2], sport)
	put16(th[2:4], dport)
	put32(th[4:8], seq)
	put32(th[8:12], ack)
	th[12] = 5 << 4
	th[13] = tcpPSH | tcpACK
	put16(th[14:16], 65535)
	copy(th[20:], payload)
	pkt := encapsulateIPv4(src, dst, ipProtoTCP, th)
	sum := transportChecksum(src, dst, ipProtoTCP, pkt[20:])
	put16(pkt[20+16:20+18], sum)
	return pkt
}

func l3TCPSynWScale(src, dst net.IP, sport, dport uint16, seq uint32, shift uint8) []byte {
	th := make([]byte, 28)
	put16(th[0:2], sport)
	put16(th[2:4], dport)
	put32(th[4:8], seq)
	th[12] = 7 << 4
	th[13] = tcpSYN
	put16(th[14:16], 65535)
	th[20], th[21], th[22], th[23] = 2, 4, 0x04, 0xd8
	th[24] = 1
	th[25], th[26], th[27] = 3, 3, shift
	pkt := encapsulateIPv4(src, dst, ipProtoTCP, th)
	sum := transportChecksum(src, dst, ipProtoTCP, pkt[20:])
	put16(pkt[20+16:20+18], sum)
	return pkt
}

func tcpMSS(th []byte, dataOff int) int {
	opts := th[20:dataOff]
	for i := 0; i+3 < len(opts); {
		switch opts[i] {
		case 0:
			return 0
		case 1:
			i++
		default:
			n := int(opts[i+1])
			if n < 2 || i+n > len(opts) {
				return 0
			}
			if opts[i] == 2 && n == 4 {
				return int(opts[i+2])<<8 | int(opts[i+3])
			}
			i += n
		}
	}
	return 0
}

func put16(p []byte, v uint16) { p[0] = byte(v >> 8); p[1] = byte(v) }
func put32(p []byte, v uint32) {
	p[0] = byte(v >> 24)
	p[1] = byte(v >> 16)
	p[2] = byte(v >> 8)
	p[3] = byte(v)
}
