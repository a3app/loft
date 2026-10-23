package transport

// Decoy splice pick, cache, resolveTLS, and DiscoverDecoy tests.

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/testcfg"
	"github.com/a3app/loft/internal/wire"
)

// TestDecoyPickIsServerPrimary splices unmatched SNI to the first origin.
func TestDecoyPickIsServerPrimary(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	d, ok := WrapDecoy(ln, []byte("tok"), []string{testcfg.DecoyAddr(), testcfg.Decoy2Addr()}).(*decoyListener)
	if !ok {
		t.Fatal("wrap")
	}
	if d.primary != testcfg.DecoyAddr() {
		t.Fatalf("primary %q", d.primary)
	}
	if got := d.pick("evil.example"); got != d.primary {
		t.Fatalf("unmatched SNI must splice to primary, got %q", got)
	}
	if got := d.pick(testcfg.Decoy2()); got != testcfg.Decoy2Addr() {
		t.Fatalf("matched SNI %q", got)
	}
	if got := d.pick(testcfg.FoldedDecoy()); got != testcfg.DecoyAddr() {
		t.Fatalf("case %q", got)
	}
}

func TestSNIIsOwn(t *testing.T) {
	names := []string{testcfg.Front()}
	if !sniIsOwn(testcfg.Front(), names) {
		t.Fatal("exact")
	}
	if !sniIsOwn(testcfg.FoldedFront(), names) {
		t.Fatal("case")
	}
	if sniIsOwn(testcfg.CDN(), names) {
		t.Fatal("cdn hostname is not the node")
	}
	if sniIsOwn("origin.example", names) {
		t.Fatal("decoy sni")
	}
	if sniIsOwn("", names) {
		t.Fatal("empty")
	}
	if !sniIsOwn(testcfg.Front(), []string{testcfg.Wildcard()}) {
		t.Fatal("wildcard")
	}
	if sniIsOwn(testcfg.Deep(), []string{testcfg.Wildcard()}) {
		t.Fatal("too many labels")
	}
}

func TestPublicSNIsFromAdvertise(t *testing.T) {
	got := PublicSNIs(testcfg.FrontHTTPS(), "")
	if len(got) != 1 || got[0] != testcfg.Front() {
		t.Fatalf("%v", got)
	}
}

func TestOwnSNIProbeSplices(t *testing.T) {
	dest, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	seen := make(chan []byte, 1)
	go func() {
		c, err := dest.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 256)
		n, _ := c.Read(buf)
		seen <- append([]byte(nil), buf[:n]...)
	}()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	w := WrapDecoyMask(inner, []byte("tok"), []string{dest.Addr().String()}, nil, nil, "node.example")
	go func() {
		c, err := w.Accept()
		if err != nil {
			return
		}
		c.Close()
	}()
	cli, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	go func() {
		_ = tls.Client(cli, &tls.Config{
			ServerName: "node.example", InsecureSkipVerify: true, NextProtos: []string{"http/1.1"},
		}).Handshake()
	}()
	select {
	case b := <-seen:
		if len(b) < 5 || b[0] != 0x16 {
			t.Fatalf("dest got %x", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("own SNI without sid must splice")
	}
}

func TestCdnPeerOwnSNIServesNode(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	w := WrapDecoyMask(inner, []byte("tok"), []string{"origin.example:443"}, nil, nil, "node.example")
	ApplyCDNPeers(w, []string{"127.0.0.0/8"})
	got := make(chan error, 1)
	go func() {
		c, err := w.Accept()
		if err != nil {
			got <- err
			return
		}
		c.Close()
		got <- nil
	}()
	cli, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	go func() {
		_ = tls.Client(cli, &tls.Config{
			ServerName: "node.example", InsecureSkipVerify: true, NextProtos: []string{"http/1.1"},
		}).Handshake()
	}()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cdn peer own SNI must hit the node")
	}
}

func TestProbeGarbageSplicesToDest(t *testing.T) {
	dest, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	seen := make(chan []byte, 1)
	go func() {
		c, err := dest.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		seen <- append([]byte(nil), buf[:n]...)
	}()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	w := WrapDecoy(inner, []byte("tok"), []string{dest.Addr().String()})
	go func() {
		c, err := w.Accept()
		if err != nil {
			return
		}
		c.Close()
	}()
	cli, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err := cli.Write([]byte("GET / HTTP/1.0\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-seen:
		if len(b) < 3 || b[0] != 'G' {
			t.Fatalf("dest got %q", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("plaintext probe must splice")
	}
}

func TestOwnSNIWithHMACSkipsMask(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	mask := &Mask{}
	mask.ready.Store(true)
	hanged := make(chan struct{})
	w := WrapDecoyMask(inner, []byte("tok"), []string{"origin.example:443"}, mask, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(hanged)
	}), "node.example")
	got := make(chan error, 1)
	go func() {
		c, err := w.Accept()
		if err != nil {
			got <- err
			return
		}
		c.Close()
		got <- nil
	}()
	cli, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	raw, _, err := buildMaskClientHello("node.example", []byte("tok"), nil, []string{"h2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRecord(cli, recHandshake, raw); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-hanged:
		t.Fatal("own SNI with HMAC entered serveMask")
	case <-time.After(2 * time.Second):
		t.Fatal("Accept hung (mask or splice)")
	}
}

func TestSpliceUDPProbeSeesDest(t *testing.T) {
	dest, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		_ = dest.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, addr, err := dest.ReadFrom(buf)
		if err != nil {
			return
		}
		got <- append([]byte(nil), buf[:n]...)
		_, _ = dest.WriteTo([]byte("pong"), addr)
	}()
	sl, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := sl.LocalAddr().String()
	_ = sl.Close()
	go func() { _ = SpliceUDP(listen, dest.LocalAddr().String()) }()
	time.Sleep(50 * time.Millisecond)
	cli, err := net.Dial("udp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err := cli.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if string(b) != "ping" {
			t.Fatalf("dest %q", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("udp probe must reach dest")
	}
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 8)
	n, err := cli.Read(buf)
	if err != nil || string(buf[:n]) != "pong" {
		t.Fatalf("reply %q err=%v", buf[:n], err)
	}
}

func TestMemberSIDAdmittedOnDecoySNI(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	w := WrapDecoyMask(inner, []byte("tok"), []string{"origin.example:443"}, nil, nil, "node.example")
	got := make(chan error, 1)
	go func() {
		c, err := w.Accept()
		if err != nil {
			got <- err
			return
		}
		c.Close()
		got <- nil
	}()
	cli, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	raw, _, err := buildMaskClientHello("origin.example", []byte("tok"), nil, []string{"h2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRecord(cli, recHandshake, raw); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("member sid on decoy SNI must be admitted")
	}
}

func TestMaskDecoySNIRejectsV2(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	mask := &Mask{}
	mask.ready.Store(true)
	w := WrapDecoyMask(inner, []byte("tok"), []string{"origin.example:443"}, mask, nil, "node.example")
	got := make(chan struct{}, 1)
	go func() {
		c, err := w.Accept()
		if err != nil {
			return
		}
		c.Close()
		got <- struct{}{}
	}()
	cli, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	raw, _, err := buildMaskClientHello("origin.example", []byte("tok"), nil, []string{"h2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRecord(cli, recHandshake, raw); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
		t.Fatal("sid v2 on a mask node's decoy SNI was admitted")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestHelloRandomIsSingleUse(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	w := WrapDecoyMask(inner, []byte("tok"), []string{"origin.example:443"}, nil, nil, "node.example")
	ok := make(chan struct{}, 2)
	go func() {
		for {
			c, err := w.Accept()
			if err != nil {
				return
			}
			c.Close()
			ok <- struct{}{}
		}
	}()
	pub := NodeMaskPub([]byte("tok"))
	raw, _, err := buildMaskClientHello("origin.example", []byte("tok"), pub, []string{"h2"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		cli, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if err := writeRecord(cli, recHandshake, raw); err != nil {
			cli.Close()
			t.Fatal(err)
		}
		cli.Close()
	}
	select {
	case <-ok:
	case <-time.After(2 * time.Second):
		t.Fatal("first sid v3 was not admitted")
	}
	select {
	case <-ok:
		t.Fatal("replayed client_random was admitted")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestProbeTLSSeesDestCert(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	w := WrapDecoy(inner, []byte("tok"), []string{ts.Listener.Addr().String()})
	go func() {
		c, err := w.Accept()
		if err != nil {
			return
		}
		c.Close()
	}()
	cli, err := tls.Dial("tcp", inner.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	peer := cli.ConnectionState().PeerCertificates
	if len(peer) == 0 {
		t.Fatal("no peer cert")
	}
	want := ts.Certificate()
	if want == nil || string(peer[0].Raw) != string(want.Raw) {
		t.Fatal("probe TLS must present dest certificate")
	}
}

func TestServerCapsMarksQUICWhenDecoy(t *testing.T) {
	c := ServerCaps(&config.ServerConfig{Decoy: []string{"www.example:443"}}, false)
	if c.Has(wire.CapQUIC) || c.Has(wire.CapH3) || c.Has(wire.CapQUICMark) || c.Has(wire.CapH3Mark) {
		t.Fatalf("GET-only decoy advertised quic: %v", c.Names())
	}
	if c&wire.CapDecoy == 0 {
		t.Fatal("missing CapDecoy")
	}
	on := ServerCaps(&config.ServerConfig{Decoy: []string{"www.example:443"}, Fronts: []string{"get", "quic", "h3"}}, false)
	if on.Has(wire.CapQUIC) || on.Has(wire.CapH3) {
		t.Fatalf("decoy advertised raw quic/h3: %v", on.Names())
	}
	if !on.Has(wire.CapQUICMark | wire.CapH3Mark) {
		t.Fatalf("decoy with quic listed must advertise marks: %v", on.Names())
	}
	if f := on.Fronts(); !slices.Contains(f, wire.FrontQUIC) || !slices.Contains(f, wire.FrontH3) {
		t.Fatalf("fronts %v", f)
	}
}

// TestServerCapsConnectOffByDefault keeps CapConnect off until fronts lists connect.
func TestServerCapsConnectOffByDefault(t *testing.T) {
	c := ServerCaps(&config.ServerConfig{}, false)
	if c.Has(wire.CapConnect) {
		t.Fatal("connect advertised")
	}
	on := ServerCaps(&config.ServerConfig{Fronts: []string{"get", "connect"}}, false)
	if !on.Has(wire.CapConnect) || !on.Has(wire.CapGET) {
		t.Fatalf("%v", on.Names())
	}
}

func TestForeignSNIStillSplices(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	w := WrapDecoyMask(inner, []byte("tok"), []string{"origin.example:443"}, nil, nil, "node.example")
	got := make(chan error, 1)
	go func() {
		c, err := w.Accept()
		if err != nil {
			got <- err
			return
		}
		c.Close()
		got <- nil
	}()
	cli, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	go func() {
		_ = tls.Client(cli, &tls.Config{
			ServerName: "probe.example", InsecureSkipVerify: true,
		}).Handshake()
	}()
	select {
	case err := <-got:
		if err == nil {
			t.Fatal("foreign SNI returned from Accept; must splice")
		}
	case <-time.After(400 * time.Millisecond):
		// Accept kept waiting: hello was spliced, not returned. That is the point.
	}
}

// TestWrapDecoyOff leaves the listener unchanged when origins are empty.
func TestWrapDecoyOff(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if WrapDecoy(ln, []byte("tok"), nil) != ln {
		t.Fatal("empty decoy must leave listener unchanged")
	}
}

// TestPeerNameForeign treats a foreign cert DNS name as foreign.
func TestPeerNameForeign(t *testing.T) {
	cert := &x509.Certificate{
		DNSNames: []string{testcfg.Decoy(), testcfg.DecoyWildcard()},
		Subject:  pkix.Name{CommonName: testcfg.Decoy()},
	}
	name, foreign, err := peerName([]*x509.Certificate{cert}, testcfg.Front())
	if err != nil || !foreign || name != testcfg.Decoy() {
		t.Fatalf("name=%q foreign=%v err=%v", name, foreign, err)
	}
	name, foreign, err = peerName([]*x509.Certificate{cert}, testcfg.Decoy())
	if err != nil || foreign || name != testcfg.Decoy() {
		t.Fatalf("match name=%q foreign=%v err=%v", name, foreign, err)
	}
	name, foreign, err = peerName([]*x509.Certificate{cert}, testcfg.Leaf())
	if err != nil || foreign {
		t.Fatalf("wildcard name=%q foreign=%v err=%v", name, foreign, err)
	}
}

// TestAdoptDecoyFromStatus caches Status.Decoy without dropping the session.
func TestAdoptDecoyFromStatus(t *testing.T) {
	clearDecoyCache()
	t.Cleanup(clearDecoyCache)
	addr := "10.0.0.1:443"
	if err := adoptDecoy(addr, "node.example.com", wire.Status{Decoy: []string{testcfg.Decoy2Addr()}}, true); err != nil {
		t.Fatal(err)
	}
	origins, known := CachedDecoy(addr)
	if !known || len(origins) != 1 || origins[0] != testcfg.Decoy2Addr() {
		t.Fatalf("cache %+v known=%v", origins, known)
	}
	if err := adoptDecoy(addr, testcfg.Decoy2(), wire.Status{Decoy: []string{testcfg.Decoy2Addr()}}, true); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptDecoyCDNKeepsEdgeSNI(t *testing.T) {
	clearDecoyCache()
	t.Cleanup(clearDecoyCache)
	st := wire.Status{Decoy: []string{testcfg.Decoy2Addr()}, MaskPub: make([]byte, 32)}
	if err := adoptDecoy("cdn.example:443", "cdn.example", st, false); err != nil {
		t.Fatalf("cdn hop: %v", err)
	}
	origins, known := CachedDecoy("cdn.example:443")
	if !known || len(origins) == 0 || origins[0] != testcfg.Decoy2Addr() {
		t.Fatalf("cache %+v known=%v", origins, known)
	}
}

// TestResolveTLSKeepsURLHost does not switch SNI to a cached dest hostname.
func TestResolveTLSKeepsURLHost(t *testing.T) {
	clearDecoyCache()
	t.Cleanup(clearDecoyCache)
	o := config.Outbound{URL: "https://10.1.2.3", Type: "ws"}
	RememberDecoy("10.1.2.3:443", []string{testcfg.DecoyAddr()})
	addr, sni, insecure, err := resolveTLS(o)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "10.1.2.3:443" || sni != "10.1.2.3" || insecure {
		t.Fatalf("addr=%s sni=%s insecure=%v", addr, sni, insecure)
	}
}

func TestResolveTLSSkipsDirectManifestFetch(t *testing.T) {
	clearDecoyCache()
	t.Cleanup(clearDecoyCache)
	o := config.Outbound{URL: testcfg.FrontHTTPS(), Type: "ws"}
	start := time.Now()
	addr, sni, _, err := resolveTLS(o)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("resolveTLS blocked %s", time.Since(start))
	}
	if addr != testcfg.FrontAddr() || sni != testcfg.Front() {
		t.Fatalf("addr=%s sni=%s", addr, sni)
	}
}

func TestResolveTLSExplicitDecoy(t *testing.T) {
	clearDecoyCache()
	t.Cleanup(clearDecoyCache)
	o := config.Outbound{URL: "https://10.1.2.3", Type: "ws", Decoy: testcfg.DecoyAddr()}
	addr, sni, insecure, err := resolveTLS(o)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "10.1.2.3:443" || sni != testcfg.Decoy() || !insecure {
		t.Fatalf("addr=%s sni=%s insecure=%v", addr, sni, insecure)
	}
}

func TestMaskOnDecoySNI(t *testing.T) {
	clearDecoyCache()
	t.Cleanup(clearDecoyCache)
	if maskOnDecoySNI(testcfg.FrontAddr(), testcfg.Front()) {
		t.Fatal("own SNI is not mask")
	}
	RememberDecoy(testcfg.FrontAddr(), []string{testcfg.DecoyAddr()})
	if !maskOnDecoySNI(testcfg.FrontAddr(), testcfg.Decoy()) {
		t.Fatal("cached dest SNI must mask")
	}
	if maskOnDecoySNI(testcfg.FrontAddr(), testcfg.Front()) {
		t.Fatal("own SNI with cache still not mask")
	}
}

func TestApplyRosterIdentity(t *testing.T) {
	clearDecoyCache()
	t.Cleanup(clearDecoyCache)
	pub := make([]byte, 32)
	pub[1] = 3
	rosterMu.Lock()
	heldRoster = wire.Roster{Relays: []wire.RelayInfo{{
		ID: "self", URL: testcfg.FrontHTTPS(), Decoy: testcfg.DecoyAddr(), MaskPub: pub,
	}}}
	rosterMu.Unlock()
	t.Cleanup(func() {
		rosterMu.Lock()
		heldRoster = wire.Roster{}
		rosterMu.Unlock()
	})
	applyRosterIdentity(testcfg.FrontAddr(), testcfg.Front())
	origins, known := CachedDecoy(testcfg.FrontAddr())
	if !known || len(origins) != 1 || decoyHost(origins[0]) != testcfg.Decoy() {
		t.Fatalf("decoy %+v known=%v", origins, known)
	}
	got := CachedMaskPub(testcfg.FrontAddr())
	if len(got) != 32 || got[1] != 3 {
		t.Fatalf("mask %x", got)
	}
}

func TestLoopbackAddr(t *testing.T) {
	if !loopbackAddr("127.0.0.1:443") || !loopbackAddr("localhost:444") {
		t.Fatal("loopback")
	}
	if loopbackAddr("10.1.2.3:444") || loopbackAddr(testcfg.FrontAddr()) {
		t.Fatal("public")
	}
}

func TestFrontCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	SetInstanceFile(filepath.Join(dir, "x.instance"))
	t.Cleanup(func() { SetInstanceFile(""); clearDecoyCache() })
	clearDecoyCache()
	RememberDecoy("159.1.2.3:444", []string{testcfg.DecoyAddr()})
	pub := make([]byte, 32)
	pub[0] = 7
	RememberMaskPub("159.1.2.3:444", pub)
	saveFrontCache("159.1.2.3:444")
	clearDecoyCache()
	loadFrontCache("159.1.2.3:444")
	origins, known := CachedDecoy("159.1.2.3:444")
	if !known || len(origins) != 1 || origins[0] != testcfg.DecoyAddr() {
		t.Fatalf("decoy %+v known=%v", origins, known)
	}
	got := CachedMaskPub("159.1.2.3:444")
	if len(got) != 32 || got[0] != 7 {
		t.Fatalf("mask_pub %x", got)
	}
}

// TestDiscoverDecoyReadsCert reads the origin cert name without a session HMAC.
func TestDiscoverDecoyReadsCert(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()
	name, foreign, err := DiscoverDecoy(ts.Listener.Addr().String(), "127.0.0.1", "go")
	if err != nil {
		t.Fatal(err)
	}
	if !foreign || name == "" || name == "127.0.0.1" {
		t.Fatalf("name=%q foreign=%v", name, foreign)
	}
}

func TestSpliceProxyHeader(t *testing.T) {
	dest, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	seen := make(chan string, 2)
	go func() {
		for {
			c, err := dest.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetReadDeadline(time.Now().Add(2 * time.Second))
				var got []byte
				buf := make([]byte, 256)
				for !strings.Contains(string(got), "\r\n\r\n") {
					n, err := c.Read(buf)
					got = append(got, buf[:n]...)
					if err != nil {
						break
					}
				}
				seen <- string(got)
			}(c)
		}
	}()
	probe := func(proxy bool) string {
		inner, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer inner.Close()
		w := WrapDecoy(inner, []byte("tok"), []string{dest.Addr().String()})
		ApplyDecoyProxy(w, proxy)
		go func() {
			if c, err := w.Accept(); err == nil {
				c.Close()
			}
		}()
		cli, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer cli.Close()
		cli.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
		select {
		case s := <-seen:
			want := fmt.Sprintf("PROXY TCP4 127.0.0.1 127.0.0.1 %d %d\r\n", cli.LocalAddr().(*net.TCPAddr).Port, inner.Addr().(*net.TCPAddr).Port)
			if proxy && !strings.HasPrefix(s, want+"GET / ") {
				t.Fatalf("with proxy got %q, want prefix %q", s, want)
			}
			if !proxy && !strings.HasPrefix(s, "GET / ") {
				t.Fatalf("without proxy got %q", s)
			}
			return s
		case <-time.After(3 * time.Second):
			t.Fatal("no splice")
		}
		return ""
	}
	probe(true)
	probe(false)
	ApplyDecoyProxy(&net.TCPListener{}, true) // not a decoy listener: no-op, no panic
}

func TestProxyHeaderFamilies(t *testing.T) {
	c4 := fakeAddrConn{r: &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 51000}, l: &net.TCPAddr{IP: net.ParseIP("::ffff:198.51.100.1"), Port: 443}}
	if got := string(proxyHeader(c4)); got != "PROXY TCP4 203.0.113.7 198.51.100.1 51000 443\r\n" {
		t.Fatalf("v4 (mapped local) %q", got)
	}
	c6 := fakeAddrConn{r: &net.TCPAddr{IP: net.ParseIP("2001:db8::7"), Port: 1}, l: &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 443}}
	if got := string(proxyHeader(c6)); got != "PROXY TCP6 2001:db8::7 2001:db8::1 1 443\r\n" {
		t.Fatalf("v6 %q", got)
	}
	mix := fakeAddrConn{r: &net.TCPAddr{IP: net.ParseIP("2001:db8::7"), Port: 1}, l: &net.TCPAddr{IP: net.ParseIP("198.51.100.1"), Port: 443}}
	if got := string(proxyHeader(mix)); got != "PROXY UNKNOWN\r\n" {
		t.Fatalf("mixed %q", got)
	}
	if got := string(proxyHeader(fakeAddrConn{})); got != "PROXY UNKNOWN\r\n" {
		t.Fatalf("non-tcp %q", got)
	}
}

type fakeAddrConn struct {
	net.Conn
	r, l net.Addr
}

func (f fakeAddrConn) RemoteAddr() net.Addr { return f.r }
func (f fakeAddrConn) LocalAddr() net.Addr  { return f.l }
