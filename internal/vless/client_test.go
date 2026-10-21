package vless

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
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
	e, err = ParseURL("vless://550e8400-e29b-41d4-a716-446655440000@de1.example.com:443?type=xhttp&path=/api&mode=auto&security=tls", "")
	if err != nil {
		t.Fatal(err)
	}
	if !e.xhttp() || e.Path != "/api" {
		t.Fatalf("%+v", e)
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
