package transport

// Tests for overlay DNS upstream parsing, DoH/UDP resolve, and gateway match.

import (
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// TestDNSAAAANoDataWhileIPv4Only answers AAAA with NOERROR/empty so
// Happy Eyeballs does not egress the node's native IPv6.
func TestDNSAAAANoDataWhileIPv4Only(t *testing.T) {
	RememberVis(wire.Status{IP: "10.8.0.2"})
	defer RememberVis(wire.Status{})
	q := dnsQuestion("example.com", dnsTypeAAAA)
	r := newDNSResolver(nil)
	ans := r.Resolve(q)
	if len(ans) < 12 {
		t.Fatal("short")
	}
	flags := binary.BigEndian.Uint16(ans[2:4])
	if flags&0x8000 == 0 || flags&0xf != 0 {
		t.Fatalf("flags %#x", flags)
	}
	if binary.BigEndian.Uint16(ans[6:8]) != 0 {
		t.Fatal("answers")
	}
	if dnsQType(q) != dnsTypeAAAA {
		t.Fatal("qtype")
	}
}

func dnsQuestion(name string, qtype uint16) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], 1)
	binary.BigEndian.PutUint16(q[4:6], 1) // QDCOUNT
	for _, lab := range splitDNSName(name) {
		q = append(q, byte(len(lab)))
		q = append(q, lab...)
	}
	q = append(q, 0)
	var t [4]byte
	binary.BigEndian.PutUint16(t[0:2], qtype)
	binary.BigEndian.PutUint16(t[2:4], 1) // IN
	return append(q, t[:]...)
}

func splitDNSName(name string) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if i > start {
				out = append(out, []byte(name[start:i]))
			}
			start = i + 1
		}
	}
	return out
}

// TestDNSFailSetsRcode checks dnsFail sets QR and SERVFAIL.
func TestDNSFailSetsRcode(t *testing.T) {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], 0x1234)
	q[2] = 0x01 // RD
	out := dnsFail(q)
	if len(out) < 12 {
		t.Fatal("short")
	}
	if binary.BigEndian.Uint16(out[0:2]) != 0x1234 {
		t.Fatal("id")
	}
	flags := binary.BigEndian.Uint16(out[2:4])
	if flags&0x8000 == 0 || flags&0xf != 2 {
		t.Fatalf("flags %#x", flags)
	}
}

// TestParseDNSUp covers DoH, DoT default port, reserved quic, and UDP.
func TestParseDNSUp(t *testing.T) {
	u, err := parseDNSUp("https://1.1.1.1/dns-query")
	if err != nil || u.kind != "https" {
		t.Fatalf("%+v %v", u, err)
	}
	u, err = parseDNSUp("tls://1.1.1.1")
	if err != nil || u.kind != "tls" || u.addr != "1.1.1.1:853" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err = parseDNSUp("quic://1.1.1.1"); err == nil {
		t.Fatal("quic reserved")
	}
	u, err = parseDNSUp("8.8.8.8")
	if err != nil || u.kind != "udp" || u.addr != "8.8.8.8:53" {
		t.Fatalf("%+v %v", u, err)
	}
}

// TestDNSOverUDP resolves through a local UDP upstream.
func TestDNSOverUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 512)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		buf[2] |= 0x80
		_, _ = pc.WriteTo(buf[:n], addr)
	}()
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], 7)
	r := newDNSResolver([]string{pc.LocalAddr().String()})
	ans := r.Resolve(q)
	if len(ans) < 12 || binary.BigEndian.Uint16(ans[0:2]) != 7 {
		t.Fatalf("%x", ans)
	}
	if ans[2]&0x80 == 0 {
		t.Fatal("not a response")
	}
}

// TestDNSOverHTTPS POSTs application/dns-message to a DoH test server.
func TestDNSOverHTTPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "ct", 400)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if len(b) >= 3 {
			b[2] |= 0x80
		}
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], 9)
	r := newDNSResolver(nil)
	r.cli = srv.Client()
	ans, err := r.doH(srv.URL, q)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(ans[0:2]) != 9 || ans[2]&0x80 == 0 {
		t.Fatalf("%x", ans)
	}
}

// TestOverlayIsDNS matches any segment gateway address.
func TestOverlayIsDNS(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Enabled: true,
		VLANs: []netconf.VLAN{{ID: 10, CIDR: "10.9.0.0/24", Gateway: "10.9.0.1"}},
	}
	o := NewOverlay(n)
	if !o.IsDNS("10.8.0.1") || !o.IsDNS("10.9.0.1") || o.IsDNS("10.8.0.2") || o.IsDNS("8.8.8.8") {
		t.Fatal("segment gateways")
	}
}

// TestDNSUDPIdleTimeout requires dnsTimeout of at least one second.
func TestDNSUDPIdleTimeout(t *testing.T) {
	if dnsTimeout < time.Second {
		t.Fatal(dnsTimeout)
	}
}
