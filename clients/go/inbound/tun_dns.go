package inbound

// tun_dns.go answers overlay gateway:53 on the phone TUN. GET holds CmdDgram
// 80ms (DoH is slower), so browser lookups die while ICMP still works.

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/transport"
)

const (
	dnsTypeAAAA = 28
	dnsMax      = 4096
)

// dnsStubAddr is the UDP resolver used off-TUN (protected on Android).
var dnsStubAddr = "1.1.1.1:53"

// dnsStubSync answers on the caller goroutine (tests).
var dnsStubSync bool

var dnsStubSeen atomic.Uint32

// overlayDNS is true when ip is the Status gateway / overlay resolver.
func overlayDNS(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if gw := net.ParseIP(transport.OverlayGateway()); gw != nil && ip.Equal(gw) {
		return true
	}
	for _, s := range transport.OverlayDNS() {
		d := net.ParseIP(s)
		if d != nil && ip.Equal(d) && d.IsPrivate() {
			return true
		}
	}
	return false
}

// answerDNS resolves q off-TUN and rebuilds a UDP reply onto the remembered flow.
func (t *tunStack) answerDNS(addr string, q []byte) {
	ans := stubDNS(q)
	if len(ans) == 0 {
		return
	}
	n := dnsStubSeen.Add(1)
	if n <= 8 {
		fmt.Fprintf(os.Stderr, "tun dns dest=%s n=%d\n", addr, len(ans))
	}
	t.onOverlayUDP(addr, ans)
}

// stubDNS answers a wire query via dnsStubAddr. AAAA is NODATA while overlay
// IPv6 is off so Happy Eyeballs does not leave the tunnel.
func stubDNS(q []byte) []byte {
	if len(q) < 12 || len(q) > dnsMax {
		return dnsFail(q)
	}
	if dnsQType(q) == dnsTypeAAAA && transport.OverlayIPv6() == "" {
		return dnsNoData(q)
	}
	c, err := protect.Timeout("udp", dnsStubAddr, 3*time.Second)
	if err != nil {
		return dnsFail(q)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(q); err != nil {
		return dnsFail(q)
	}
	buf := make([]byte, dnsMax)
	n, err := c.Read(buf)
	if err != nil || n < 12 {
		return dnsFail(q)
	}
	return buf[:n]
}

// serveLocalDNSTCP answers length-prefixed DNS on a gVisor TCP Open to gateway:53.
func serveLocalDNSTCP(c net.Conn) {
	defer c.Close()
	for {
		var hdr [2]byte
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[:]))
		if n < 12 || n > dnsMax {
			return
		}
		q := make([]byte, n)
		if _, err := io.ReadFull(c, q); err != nil {
			return
		}
		ans := stubDNS(q)
		if len(ans) == 0 {
			return
		}
		binary.BigEndian.PutUint16(hdr[:], uint16(len(ans)))
		if _, err := c.Write(hdr[:]); err != nil {
			return
		}
		if _, err := c.Write(ans); err != nil {
			return
		}
	}
}

// dnsQType returns the record type asked for in DNS query q (for example
// A or AAAA), or 0 if q is too short. It skips the 12-byte header and the
// question name to reach the type field.
func dnsQType(q []byte) uint16 {
	if len(q) < 12 {
		return 0
	}
	off := 12
	for off < len(q) {
		l := int(q[off])
		if l == 0 {
			off++
			break
		}
		if l >= 0xc0 {
			off += 2
			break
		}
		off += 1 + l
	}
	if off+2 > len(q) {
		return 0
	}
	return binary.BigEndian.Uint16(q[off : off+2])
}

// dnsNoData turns query q into an empty successful answer: the name exists
// but has no records of that type. It is used for AAAA (IPv6) questions
// while the overlay has no IPv6, so apps fall back to IPv4 at once instead
// of waiting.
func dnsNoData(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	out := append([]byte(nil), q...)
	flags := binary.BigEndian.Uint16(q[2:4])
	flags |= 0x8000
	flags &= 0xfff0
	binary.BigEndian.PutUint16(out[2:4], flags)
	binary.BigEndian.PutUint16(out[6:8], 0)
	binary.BigEndian.PutUint16(out[8:10], 0)
	binary.BigEndian.PutUint16(out[10:12], 0)
	return out
}

// dnsFail turns query q into a SERVFAIL answer (error code 2, "server
// failure"), so the app gives up or tries another server quickly.
func dnsFail(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	out := append([]byte(nil), q...)
	flags := binary.BigEndian.Uint16(q[2:4])
	flags |= 0x8000
	flags = (flags & 0xfff0) | 2
	binary.BigEndian.PutUint16(out[2:4], flags)
	return out
}
