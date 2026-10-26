package vless

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/url"
	"testing"
	"time"
)

func TestParseUUID(t *testing.T) {
	a, err := parseUUID("550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseUUID("550e8400e29b41d4a716446655440000")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("uuid mismatch")
	}
	if _, err := parseUUID("not-a-uuid"); err == nil {
		t.Fatalf("want error")
	}
}

func TestWriteRequestDomain(t *testing.T) {
	id, _ := parseUUID("550e8400-e29b-41d4-a716-446655440000")
	var buf bytes.Buffer
	if err := writeRequest(&buf, id, "example.com:443", false); err != nil {
		t.Fatal(err)
	}
	p := buf.Bytes()
	if p[0] != 0 || p[17] != 0 || p[18] != cmdTCP {
		t.Fatalf("header %x", p[:19])
	}
	if binary.BigEndian.Uint16(p[19:21]) != 443 {
		t.Fatalf("port")
	}
	if p[21] != atypDomain || p[22] != byte(len("example.com")) {
		t.Fatalf("atyp")
	}
	if string(p[23:23+11]) != "example.com" {
		t.Fatalf("host %q", p[23:])
	}
}

func TestProbeReturnsWithoutResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 26)
		if _, err := io.ReadFull(c, buf); err != nil {
			return
		}
		close(got)
		time.Sleep(2 * time.Second)
	}()
	raw := "vless://550e8400-e29b-41d4-a716-446655440000@" + ln.Addr().String() + "?security=none"
	c, err := New(Config{URL: raw})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := c.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("probe waited for a response")
	}
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("server saw no header")
	}
}

func TestOpenWritesBeforeResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()
	raw := "vless://550e8400-e29b-41d4-a716-446655440000@" + ln.Addr().String() + "?security=none"
	cli, err := New(Config{URL: raw})
	if err != nil {
		t.Fatal(err)
	}
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := cli.Open("example.com:80")
		ch <- res{c, err}
	}()
	var srv net.Conn
	select {
	case srv = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("accept")
	}
	defer srv.Close()
	if _, err := io.ReadFull(srv, make([]byte, 34)); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatal(r.err)
		}
		defer r.c.Close()
		if _, err := r.c.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(srv, make([]byte, 4)); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.Write([]byte{0, 0, 'o', 'k'}); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 2)
		if _, err := io.ReadFull(r.c, got); err != nil {
			t.Fatal(err)
		}
		if string(got) != "ok" {
			t.Fatalf("payload %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Open waited for the response")
	}
}

func TestParseURL(t *testing.T) {
	e, err := ParseURL("vless://550e8400-e29b-41d4-a716-446655440000@de1.example.com:443?security=tls&sni=de1.example.com#n", "")
	if err != nil {
		t.Fatal(err)
	}
	if e.Addr != "de1.example.com:443" || !e.TLS || e.Reality || e.UUID == "" {
		t.Fatalf("%+v", e)
	}
	if e.SNI != "de1.example.com" {
		t.Fatalf("sni %q", e.SNI)
	}
	e, err = ParseURL("vless://550e8400-e29b-41d4-a716-446655440000@1.2.3.4:8443?security=reality&pbk=abc", "")
	if err != nil {
		t.Fatal(err)
	}
	if !e.Reality {
		t.Fatalf("want reality")
	}
	e, err = ParseURL("node.example.com", "550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatal(err)
	}
	if e.Addr != "node.example.com:443" {
		t.Fatalf("addr %s", e.Addr)
	}
}

func TestParseURLRealityVisionXHTTP(t *testing.T) {
	raw := "vless://550e8400-e29b-41d4-a716-446655440000@de1.example.com:443?security=reality&sni=www.microsoft.com&fp=chrome&pbk=q3ucJuIkRdJYS8_N0Si4U7FA3Qo7jqcblDghyDUXIc8&sid=0123456789abcdef&type=tcp&flow=xtls-rprx-vision#n"
	e, err := ParseURL(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if !e.Reality || !e.Vision || e.Network != "tcp" || e.Fingerprint != "chrome" {
		t.Fatalf("%+v", e)
	}
	if _, err := decodePbk(e.PublicKey); err != nil {
		t.Fatal(err)
	}
	sid, err := decodeSid(e.ShortID)
	if err != nil || len(sid) != 8 {
		t.Fatalf("sid %v %v", sid, err)
	}
	e, err = ParseURL("vless://550e8400-e29b-41d4-a716-446655440000@de1.example.com:443?type=xhttp&path=/api&mode=auto&security=tls&alpn=h2", "")
	if err != nil {
		t.Fatal(err)
	}
	if !e.xhttp() || e.Path != "/api" || e.Mode != "auto" || e.ALPN != "h2" {
		t.Fatalf("%+v", e)
	}
	if e.xhttpCfg().Reality {
		t.Fatal("tls xhttp is not reality")
	}
}

func TestClientALPN(t *testing.T) {
	c, err := New(Config{URL: "vless://550e8400-e29b-41d4-a716-446655440000@h:443?type=xhttp&security=tls"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.alpn(); len(got) != 1 || got[0] != "h2" {
		t.Fatalf("xhttp default %v", got)
	}
	c, err = New(Config{URL: "vless://550e8400-e29b-41d4-a716-446655440000@h:443?type=xhttp&security=tls&alpn=h2,http/1.1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.alpn(); len(got) != 2 || got[0] != "h2" || got[1] != "http/1.1" {
		t.Fatalf("link alpn %v", got)
	}
	c, err = New(Config{URL: "vless://550e8400-e29b-41d4-a716-446655440000@h:443?type=tcp&security=tls"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.alpn(); got != nil {
		t.Fatalf("tcp %v", got)
	}
}

func TestParseURLXHTTPStreamUp(t *testing.T) {
	extra := url.QueryEscape(`{"mode":"stream-up","xPaddingBytes":"100-1000"}`)
	raw := "vless://550e8400-e29b-41d4-a716-446655440000@in2.example:443?type=xhttp&security=tls&fp=firefox&sni=panel.example&path=/abc/&extra=" + extra
	e, err := ParseURL(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if e.Mode != "stream-up" || e.PadFrom != 100 || e.PadTo != 1000 || e.Fingerprint != "firefox" || e.Path != "/abc/" || e.SNI != "panel.example" {
		t.Fatalf("%+v", e)
	}
	cfg := e.xhttpCfg()
	if cfg.Mode != "stream-up" || cfg.PadFrom != 100 || cfg.PadTo != 1000 || cfg.SNI != "panel.example" || cfg.Path != "/abc/" {
		t.Fatalf("%+v", cfg)
	}
}

func TestWriteRequestVision(t *testing.T) {
	id, _ := parseUUID("550e8400-e29b-41d4-a716-446655440000")
	var buf bytes.Buffer
	if err := writeRequest(&buf, id, "example.com:443", true); err != nil {
		t.Fatal(err)
	}
	p := buf.Bytes()
	addon := visionAddon(true)
	if p[17] != byte(len(addon)) {
		t.Fatalf("addon len %d", p[17])
	}
	if string(p[18:18+len(addon)]) != string(addon) {
		t.Fatalf("addon %x", p[18:18+len(addon)])
	}
}

func TestVisionPadCapsContent(t *testing.T) {
	c := wrapVision(nil, [16]byte{1}).(*visionConn)
	c.writeUUID = false
	frame := c.pad(make([]byte, visionMaxContent), cmdPadContinue)
	if len(frame) != 5+visionMaxContent {
		t.Fatalf("full chunk: %d", len(frame))
	}
	huge := c.pad(make([]byte, 1<<20), cmdPadContinue)
	if len(huge) != 5+visionMaxContent {
		t.Fatalf("did not cap: %d", len(huge))
	}
	if len(huge) > visionFrameCap {
		t.Fatalf("frame %d > cap %d", len(huge), visionFrameCap)
	}
	c.tlsInner = true
	c.writeUUID = true
	small := c.pad([]byte("hi"), cmdPadContinue)
	if len(small) < 21+2 || len(small) > 21+2+visionMaxPad {
		t.Fatalf("padded small: %d", len(small))
	}
	if len(small) > visionFrameCap {
		t.Fatalf("padded small %d > cap %d", len(small), visionFrameCap)
	}
}

func TestVisionPadRoundtrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	id, _ := parseUUID("550e8400-e29b-41d4-a716-446655440000")
	wa := wrapVision(a, id)
	wb := wrapVision(b, id)
	msg := []byte("hello vision pad")
	errc := make(chan error, 1)
	go func() {
		_, err := wa.Write(msg)
		errc <- err
	}()
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(wb, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(msg) {
		t.Fatalf("got %q", got)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

// TestVisionHelloSharesTheHeaderWrite is Xray's first Read: one buffer, then
// the header is parsed out of it. The UUID frame has to already be in that
// buffer. The following user write is a frame without a second UUID, and the
// response (2-byte header plus a UUID frame) comes back as the payload.
func TestVisionHelloSharesTheHeaderWrite(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()
	id, err := parseUUID("550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatal(err)
	}
	raw := "vless://550e8400-e29b-41d4-a716-446655440000@" + ln.Addr().String() + "?security=none&type=tcp&flow=xtls-rprx-vision"
	cli, err := New(Config{URL: raw})
	if err != nil {
		t.Fatal(err)
	}
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := cli.Open("example.com:80")
		ch <- res{c, err}
	}()
	var srv net.Conn
	select {
	case srv = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("accept")
	}
	defer srv.Close()
	_ = srv.SetDeadline(time.Now().Add(2 * time.Second))
	if err := readVLESSHeader(srv); err != nil {
		t.Fatal(err)
	}
	_ = srv.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	extra := make([]byte, 2048)
	n, err := srv.Read(extra)
	if n < 21 || err != nil {
		t.Fatalf("uuid frame was not in the header write: n=%d err=%v", n, err)
	}
	extra = extra[:n]
	if !bytes.Equal(extra[:16], id[:]) {
		t.Fatalf("first body does not start with uuid: %x", extra[:16])
	}
	if extra[16] != cmdPadContinue {
		t.Fatalf("hello command %d", extra[16])
	}
	clen := int(binary.BigEndian.Uint16(extra[17:19]))
	plen := int(binary.BigEndian.Uint16(extra[19:21]))
	if clen != 0 {
		t.Fatalf("hello content %d", clen)
	}
	if 21+clen+plen > len(extra) {
		t.Fatalf("hello frame short: %d < %d", len(extra), 21+clen+plen)
	}
	var opened res
	select {
	case opened = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("Open waited")
	}
	if opened.err != nil {
		t.Fatal(opened.err)
	}
	defer opened.c.Close()
	if _, err := opened.c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = srv.SetReadDeadline(time.Now().Add(2 * time.Second))
	var fh [5]byte
	if _, err := io.ReadFull(srv, fh[:]); err != nil {
		t.Fatal(err)
	}
	if fh[0] != cmdPadContinue {
		t.Fatalf("second frame command %d (uuid repeated?)", fh[0])
	}
	clen = int(binary.BigEndian.Uint16(fh[1:3]))
	plen = int(binary.BigEndian.Uint16(fh[3:5]))
	body := make([]byte, clen+plen)
	if _, err := io.ReadFull(srv, body); err != nil {
		t.Fatal(err)
	}
	if clen < 4 || string(body[:4]) != "ping" {
		t.Fatalf("payload %q", body[:clen])
	}
	vc := wrapVision(nil, id).(*visionConn)
	frame := vc.pad([]byte("ok"), cmdPadEnd)
	if _, err := srv.Write(append([]byte{0, 0}, frame...)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2)
	_ = opened.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(opened.c, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "ok" {
		t.Fatalf("downlink %q", got)
	}
}

// readVLESSHeader consumes one request header (version, uuid, addon, command, port, address).
func readVLESSHeader(r io.Reader) error {
	var lead [18]byte
	if _, err := io.ReadFull(r, lead[:]); err != nil {
		return err
	}
	addon := int(lead[17])
	if addon > 0 {
		if _, err := io.ReadFull(r, make([]byte, addon)); err != nil {
			return err
		}
	}
	var cmd [4]byte
	if _, err := io.ReadFull(r, cmd[:]); err != nil {
		return err
	}
	switch cmd[3] {
	case atypIPv4:
		_, err := io.ReadFull(r, make([]byte, 4))
		return err
	case atypIPv6:
		_, err := io.ReadFull(r, make([]byte, 16))
		return err
	case atypDomain:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return err
		}
		_, err := io.ReadFull(r, make([]byte, int(n[0])))
		return err
	default:
		return io.ErrUnexpectedEOF
	}
}

func TestVisionAppDataCommandIsEnd(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	id, err := parseUUID("550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatal(err)
	}
	w := wrapVision(a, id)
	errc := make(chan error, 1)
	go func() {
		if _, err := w.Write([]byte{0x16, 0x03, 0x01}); err != nil {
			errc <- err
			return
		}
		_, err := w.Write([]byte{0x17, 0x03, 0x03, 0x00, 0x01, 0xaa})
		errc <- err
	}()
	_ = b.SetDeadline(time.Now().Add(2 * time.Second))
	cmd, _, err := readVisionFrame(b, true)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != cmdPadContinue {
		t.Fatalf("handshake command %d", cmd)
	}
	cmd, content, err := readVisionFrame(b, false)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != cmdPadEnd {
		t.Fatalf("application data command %d", cmd)
	}
	if len(content) < 3 || content[0] != 0x17 {
		t.Fatalf("content %x", content)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

type visionHop struct {
	net.Conn
	next net.Conn
}

func (h visionHop) NetConn() net.Conn { return h.next }

type visionSplice struct {
	net.Conn
	head []byte
}

func (s *visionSplice) Release() ([]byte, net.Conn) { return s.head, s.Conn }

func TestVisionDirectReadUsesRawSocket(t *testing.T) {
	tlsA, tlsB := net.Pipe()
	rawA, rawB := net.Pipe()
	defer tlsA.Close()
	defer tlsB.Close()
	defer rawA.Close()
	defer rawB.Close()
	id, err := parseUUID("550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatal(err)
	}
	gate := &visionSplice{Conn: rawA, head: []byte("Q")}
	vc := wrapVision(visionHop{Conn: tlsA, next: gate}, id)
	fr := wrapVision(nil, id).(*visionConn)
	frame := fr.pad([]byte("hi"), cmdPadDirect)
	go func() {
		_, _ = tlsB.Write(frame)
		_, _ = rawB.Write([]byte("XYZ"))
	}()
	_ = tlsA.SetDeadline(time.Now().Add(2 * time.Second))
	_ = rawA.SetDeadline(time.Now().Add(2 * time.Second))
	got := make([]byte, 2)
	if _, err := io.ReadFull(vc, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi" {
		t.Fatalf("content %q", got)
	}
	rest := make([]byte, 4)
	if _, err := io.ReadFull(vc, rest); err != nil {
		t.Fatal(err)
	}
	if string(rest) != "QXYZ" {
		t.Fatalf("after direct %q", rest)
	}
}

func readVisionFrame(r io.Reader, uuid bool) (cmd byte, content []byte, err error) {
	need := 5
	if uuid {
		need += 16
	}
	hdr := make([]byte, need)
	if _, err = io.ReadFull(r, hdr); err != nil {
		return
	}
	off := 0
	if uuid {
		off = 16
	}
	cmd = hdr[off]
	clen := int(binary.BigEndian.Uint16(hdr[off+1 : off+3]))
	plen := int(binary.BigEndian.Uint16(hdr[off+3 : off+5]))
	content = make([]byte, clen)
	if clen > 0 {
		if _, err = io.ReadFull(r, content); err != nil {
			return
		}
	}
	if plen > 0 {
		if _, err = io.ReadFull(r, make([]byte, plen)); err != nil {
			return
		}
	}
	return
}

func TestNewAcceptsReality(t *testing.T) {
	cli, err := New(Config{
		URL:  "vless://550e8400-e29b-41d4-a716-446655440000@127.0.0.1:443?security=reality&pbk=q3ucJuIkRdJYS8_N0Si4U7FA3Qo7jqcblDghyDUXIc8&sid=abcd&flow=xtls-rprx-vision",
		UUID: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	cli.Close()
}

func TestNewRejectsKCP(t *testing.T) {
	_, err := New(Config{URL: "vless://550e8400-e29b-41d4-a716-446655440000@127.0.0.1:443?type=kcp"})
	if err == nil {
		t.Fatal("want transport error")
	}
}

func TestDecodePbkSid(t *testing.T) {
	if _, err := decodePbk("abc"); err == nil {
		t.Fatal("short pbk")
	}
	b, err := decodePbk("q3ucJuIkRdJYS8_N0Si4U7FA3Qo7jqcblDghyDUXIc8")
	if err != nil || len(b) != 32 {
		t.Fatalf("%v %v", len(b), err)
	}
	if _, err := decodeSid("gg"); err == nil {
		t.Fatal("bad sid")
	}
	s, err := decodeSid("ab")
	if err != nil || len(s) != 1 || s[0] != 0xab {
		t.Fatalf("%v %v", s, err)
	}
}
