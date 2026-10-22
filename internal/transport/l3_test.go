package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

func TestL3GatewayPing(t *testing.T) {
	withOverlay(t)
	EnableHTTP2ExtendedConnect()
	const token = "l3-test-token"
	h2s := &H2Server{
		Info: NodeInfo{Token: []byte(token), Caps: wire.NodeCaps},
		Path: "/api/v1/tunnel",
	}
	url := startH2L3(t, h2s)
	l := NewL3(token, config.Outbound{
		Type: "l3", URL: url, Path: "/api/v1/tunnel",
		Fingerprint: "go", Insecure: true,
	}, wire.CapL3)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ip := OverlayIP()
	if ip == "" {
		t.Fatal("overlay ip")
	}
	req := makeEchoPkt(net.ParseIP(ip), net.ParseIP("10.8.0.1"), []byte("ping"))
	if err := l.WriteIP(req); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() {
		p, err := l.ReadIP()
		if err != nil {
			return
		}
		done <- p
	}()
	select {
	case got := <-done:
		ihl := int(got[0]&0x0f) * 4
		if len(got) < ihl+8 || got[ihl] != icmpReply {
			t.Fatalf("want echo-reply, type=%d", got[ihl])
		}
		if !net.IP(got[16:20]).Equal(net.ParseIP(ip)) {
			t.Fatalf("dst %s want %s", net.IP(got[16:20]), ip)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

// TestL3PipeGatewayPing is the inner L3 path without HTTP/2: 0x02, Status, ICMP echo.
func TestL3PipeGatewayPing(t *testing.T) {
	withOverlay(t)
	const token = "l3-pipe-token"
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	go serveRaw(b, NodeInfo{Token: []byte(token), Caps: wire.NodeCaps}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inner, err := startL3(ctx, a, []byte(token), wire.CapL3, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.die()
	ip := OverlayIP()
	if ip == "" {
		t.Fatal("overlay ip")
	}
	req := makeEchoPkt(net.ParseIP(ip), net.ParseIP("10.8.0.1"), []byte("ping"))
	if err := inner.WriteIP(req); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() {
		p, err := inner.ReadIP()
		if err != nil {
			return
		}
		done <- p
	}()
	select {
	case got := <-done:
		ihl := int(got[0]&0x0f) * 4
		if len(got) < ihl+8 || got[ihl] != icmpReply {
			t.Fatalf("want echo-reply, type=%d", got[ihl])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestServerCapsL3(t *testing.T) {
	c := ServerCaps(&config.ServerConfig{Fronts: []string{"l3"}}, false)
	if !c.Has(wire.CapL3) || c.Has(wire.CapGET) || c.Has(wire.CapH2) {
		t.Fatalf("%v", c.Names())
	}
	if f := c.Fronts(); len(f) != 1 || f[0] != wire.FrontL3 {
		t.Fatalf("%v", f)
	}
}

func TestL3MeshNeighborPing(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersMesh, Enabled: true}
	o := NewOverlay(n)
	oldKeys, oldSwitch := AuthKeys, NetSwitch()
	AuthKeys = nil
	SetNetSwitch(o)
	t.Cleanup(func() {
		AuthKeys = oldKeys
		SetNetSwitch(oldSwitch)
	})
	const token = "l3-mesh-token"
	info := NodeInfo{Token: []byte(token), Caps: wire.NodeCaps}
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()
	t.Cleanup(func() { _ = a1.Close(); _ = a2.Close(); _ = b1.Close(); _ = b2.Close() })
	go serveRaw(a2, info, nil)
	go serveRaw(b2, info, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sa, err := startL3(ctx, a1, []byte(token), wire.CapL3, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer sa.die()
	ipA := OverlayIP()
	sb, err := startL3(ctx, b1, []byte(token), wire.CapL3, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer sb.die()
	ipB := OverlayIP()
	if ipA == "" || ipB == "" || ipA == ipB {
		t.Fatalf("ips %s %s", ipA, ipB)
	}
	req := makeEchoPkt(net.ParseIP(ipA), net.ParseIP(ipB), []byte("x"))
	if err := sa.WriteIP(req); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() {
		p, err := sb.ReadIP()
		if err != nil {
			return
		}
		done <- p
	}()
	select {
	case got := <-done:
		if !net.IP(got[16:20]).Equal(net.ParseIP(ipB)) {
			t.Fatalf("dst %s", net.IP(got[16:20]))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func startH2L3(t *testing.T, h http.Handler) string {
	t.Helper()
	s, ok := h.(*H2Server)
	if !ok {
		t.Fatal("H2Server")
	}
	dummy := httptest.NewTLSServer(http.NotFoundHandler())
	certs := dummy.TLS.Certificates
	dummy.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates:           certs,
		NextProtos:             []string{http2.NextProtoTLS},
		MinVersion:             tls.VersionTLS13,
		SessionTicketsDisabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				stream, err := acceptH2CONNECT(c)
				if err != nil {
					_ = c.Close()
					return
				}
				serveRaw(stream, s.Info, s.Seen)
			}(c)
		}
	}()
	return "https://" + ln.Addr().String()
}

// acceptH2CONNECT is the node side of RFC 8441 CONNECT for tests.
// x/net's HTTP/2 server keeps extended CONNECT off unless GODEBUG=http2xconnect=1
// (loft-node.service sets it); go test does not.
func acceptH2CONNECT(c net.Conn) (net.Conn, error) {
	br := bufio.NewReaderSize(c, 32<<10)
	preface := make([]byte, len(h2Preface))
	if _, err := io.ReadFull(br, preface); err != nil {
		return nil, err
	}
	bw := bufio.NewWriterSize(c, 32<<10)
	fr := http2.NewFramer(bw, br)
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if err := fr.WriteSettings(
		http2.Setting{ID: http2.SettingEnableConnectProtocol, Val: 1},
		http2.Setting{ID: http2.SettingInitialWindowSize, Val: h2StreamWindow},
		http2.Setting{ID: http2.SettingMaxFrameSize, Val: 1 << 20},
	); err != nil {
		return nil, err
	}
	if extra := uint32(h2ConnWindow - 65535); extra > 0 {
		if err := fr.WriteWindowUpdate(0, extra); err != nil {
			return nil, err
		}
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	for i := 0; i < 16; i++ {
		f, err := fr.ReadFrame()
		if err != nil {
			return nil, err
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				if err := fr.WriteSettingsAck(); err != nil {
					return nil, err
				}
				if err := bw.Flush(); err != nil {
					return nil, err
				}
			}
		case *http2.MetaHeadersFrame:
			var hdr []byte
			enc := hpack.NewEncoder(&hpackAppend{p: &hdr})
			if err := enc.WriteField(hpack.HeaderField{Name: ":status", Value: "200"}); err != nil {
				return nil, err
			}
			if err := fr.WriteHeaders(http2.HeadersFrameParam{
				StreamID: f.StreamID, BlockFragment: hdr, EndHeaders: true,
			}); err != nil {
				return nil, err
			}
			if err := bw.Flush(); err != nil {
				return nil, err
			}
			st := &h2stream{c: c, fr: fr, bw: bw, br: br, sid: f.StreamID, win: int32(h2StreamWindow), cwin: int32(h2ConnWindow)}
			pr, pw := io.Pipe()
			st.inR, st.inW = pr, pw
			go st.readLoop(make(chan h2hs, 1))
			return st, nil
		}
	}
	return nil, fmt.Errorf("no CONNECT")
}

func TestL3CorkBatches(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	s := &l3Sess{conn: a, key: key, in: make(chan []byte, 8), done: make(chan struct{})}
	p1, p2 := l3TCPSeg(40), l3TCPSeg(41)
	if err := s.WriteIP(p1); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteIP(p2); err != nil {
		t.Fatal(err)
	}
	_ = b.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	raw, err := readMsg(b)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ecrypto.Open(key, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := wire.SplitL3(plain)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d err=%v", len(got), err)
	}
}

func TestL3UrgentFlushesAlone(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	key := make([]byte, 32)
	s := &l3Sess{conn: a, key: key, in: make(chan []byte, 8), done: make(chan struct{})}
	first := make(chan []byte, 1)
	go func() {
		raw, err := readMsg(b)
		if err != nil {
			return
		}
		plain, err := ecrypto.Open(key, raw, nil)
		if err != nil {
			return
		}
		got, err := wire.SplitL3(plain)
		if err != nil || len(got) == 0 {
			return
		}
		first <- got[0]
	}()
	icmp := makeEchoPkt(net.IPv4(10, 8, 0, 2), net.IPv4(10, 8, 0, 1), []byte("x"))
	if err := s.WriteIP(icmp); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-first:
		if p[9] != 1 {
			t.Fatalf("proto %d", p[9])
		}
	case <-time.After(time.Second):
		t.Fatal("icmp blocked by cork")
	}
}

func TestL3AdoptKeepsDone(t *testing.T) {
	a, b := net.Pipe()
	c, d := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close(); _ = c.Close(); _ = d.Close() })
	key := make([]byte, 32)
	s := &l3Sess{conn: a, key: key, in: make(chan []byte, 8), done: make(chan struct{})}
	go s.readLoop(a, key)
	s.adopt(c, key)
	select {
	case <-s.done:
		t.Fatal("died on adopt")
	case <-time.After(50 * time.Millisecond):
	}
}

func l3TCPSeg(id byte) []byte {
	p := make([]byte, 80)
	p[0] = 0x45
	p[2], p[3] = 0, 80
	p[8] = 64
	p[9] = ipProtoTCP
	p[20+12] = 5 << 4
	p[20+13] = tcpPSH | tcpACK
	p[79] = id
	return p
}
