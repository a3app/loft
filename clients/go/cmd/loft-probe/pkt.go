// pkt.go builds and parses IPv4 ICMP echo and DNS query packets for loft-probe.
package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
)

const (
	ipProtoICMP = 1
	icmpEcho    = 8
	icmpReply   = 0
)

// checksum is the Internet checksum (ones' complement) used by IPv4 and ICMP.
func checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s > 0xffff {
		s = (s >> 16) + (s & 0xffff)
	}
	return ^uint16(s)
}

// makeEcho builds an IPv4 ICMP echo request. It returns nil if src or dst is not IPv4.
func makeEcho(src, dst net.IP, id, seq uint16, payload []byte) []byte {
	src4, dst4 := src.To4(), dst.To4()
	if src4 == nil || dst4 == nil {
		return nil
	}
	ihl := 20
	icmpLen := 8 + len(payload)
	pkt := make([]byte, ihl+icmpLen)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = ipProtoICMP
	copy(pkt[12:16], src4)
	copy(pkt[16:20], dst4)
	binary.BigEndian.PutUint16(pkt[10:12], checksum(pkt[:ihl]))
	pkt[ihl] = icmpEcho
	binary.BigEndian.PutUint16(pkt[ihl+4:ihl+6], id)
	binary.BigEndian.PutUint16(pkt[ihl+6:ihl+8], seq)
	copy(pkt[ihl+8:], payload)
	binary.BigEndian.PutUint16(pkt[ihl+2:ihl+4], checksum(pkt[ihl:]))
	return pkt
}

// parseICMPReply is true when pkt is an IPv4 echo-reply with the given id and seq.
func parseICMPReply(pkt []byte, wantID, wantSeq uint16) (src net.IP, ok bool) {
	if len(pkt) < 28 || pkt[0]>>4 != 4 {
		return nil, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl+8 {
		return nil, false
	}
	if pkt[9] != ipProtoICMP || pkt[ihl] != icmpReply {
		return nil, false
	}
	id := binary.BigEndian.Uint16(pkt[ihl+4 : ihl+6])
	seq := binary.BigEndian.Uint16(pkt[ihl+6 : ihl+8])
	if id != wantID || seq != wantSeq {
		return nil, false
	}
	return net.IP(pkt[12:16]).To4(), true
}

// dnsQuery is a DNS message with RD set, one A IN question, and no EDNS.
func dnsQuery(id uint16, name string) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], id)
	q[2] = 0x01 // RD
	binary.BigEndian.PutUint16(q[4:6], 1)
	q = append(q, encodeName(name)...)
	q = append(q, 0, 1, 0, 1) // A IN
	return q
}

// encodeName writes a DNS wire name (length-prefixed labels, trailing zero).
func encodeName(name string) []byte {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" {
		return []byte{0}
	}
	var out []byte
	for _, lab := range strings.Split(name, ".") {
		if lab == "" || len(lab) > 63 {
			continue
		}
		out = append(out, byte(len(lab)))
		out = append(out, lab...)
	}
	out = append(out, 0)
	return out
}

// parseDNSAnswers returns rcode and A records from a DNS response.
func parseDNSAnswers(msg []byte) (rcode int, addrs []net.IP, err error) {
	if len(msg) < 12 {
		return 0, nil, fmt.Errorf("short dns")
	}
	rcode = int(msg[3] & 0x0f)
	qd := int(binary.BigEndian.Uint16(msg[4:6]))
	an := int(binary.BigEndian.Uint16(msg[6:8]))
	off := 12
	for i := 0; i < qd; i++ {
		off, err = skipName(msg, off)
		if err != nil {
			return rcode, nil, err
		}
		off += 4
		if off > len(msg) {
			return rcode, nil, fmt.Errorf("short question")
		}
	}
	for i := 0; i < an; i++ {
		off, err = skipName(msg, off)
		if err != nil {
			return rcode, nil, err
		}
		if off+10 > len(msg) {
			return rcode, nil, fmt.Errorf("short rr")
		}
		typ := binary.BigEndian.Uint16(msg[off : off+2])
		rdlen := int(binary.BigEndian.Uint16(msg[off+8 : off+10]))
		off += 10
		if off+rdlen > len(msg) {
			return rcode, nil, fmt.Errorf("short rdata")
		}
		if typ == 1 && rdlen == 4 {
			ip := make(net.IP, 4)
			copy(ip, msg[off:off+4])
			addrs = append(addrs, ip)
		}
		off += rdlen
	}
	return rcode, addrs, nil
}

// skipName advances past one DNS name, including compression pointers.
func skipName(msg []byte, off int) (int, error) {
	for hops := 0; hops < 16; hops++ {
		if off >= len(msg) {
			return 0, fmt.Errorf("name overflow")
		}
		l := int(msg[off])
		if l == 0 {
			return off + 1, nil
		}
		if l&0xc0 == 0xc0 {
			if off+1 >= len(msg) {
				return 0, fmt.Errorf("short ptr")
			}
			return off + 2, nil
		}
		if l&0xc0 != 0 {
			return 0, fmt.Errorf("bad name")
		}
		off += 1 + l
	}
	return 0, fmt.Errorf("name loop")
}
