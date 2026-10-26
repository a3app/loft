package trojan

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestHexSHA224(t *testing.T) {
	h := hexSHA224("password")
	if len(h) != 56 {
		t.Fatalf("len %d", len(h))
	}
	if string(h) != "d63dc919e201d7bc4c825630d2cf25fdc93d4b2f0d46706d29038d01" {
		t.Fatalf("%s", h)
	}
}

func TestWriteRequestDomain(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRequest(&buf, "password", "example.com:443", false); err != nil {
		t.Fatal(err)
	}
	p := buf.Bytes()
	if len(p) < 56+2+1+1+1+11+2+2 {
		t.Fatalf("short %d", len(p))
	}
	if string(p[:56]) != "d63dc919e201d7bc4c825630d2cf25fdc93d4b2f0d46706d29038d01" {
		t.Fatalf("hash")
	}
	if p[56] != '\r' || p[57] != '\n' {
		t.Fatalf("crlf")
	}
	if p[58] != cmdTCP {
		t.Fatalf("cmd %d", p[58])
	}
	if p[59] != atypDomain || p[60] != byte(len("example.com")) {
		t.Fatalf("atyp")
	}
	if string(p[61:72]) != "example.com" {
		t.Fatalf("host")
	}
	if binary.BigEndian.Uint16(p[72:74]) != 443 {
		t.Fatalf("port")
	}
	if p[74] != '\r' || p[75] != '\n' {
		t.Fatalf("tail crlf")
	}
}

func TestWriteRequestUDP(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRequest(&buf, "password", "1.1.1.1:53", true); err != nil {
		t.Fatal(err)
	}
	p := buf.Bytes()
	if p[58] != cmdUDP {
		t.Fatalf("cmd %d", p[58])
	}
	if p[59] != atypIPv4 {
		t.Fatalf("atyp %d", p[59])
	}
}

func TestParseURL(t *testing.T) {
	e, err := ParseURL("trojan://secret@de1.example.com:443?security=tls&sni=de1.example.com#n", "")
	if err != nil {
		t.Fatal(err)
	}
	if e.Addr != "de1.example.com:443" || !e.TLS || e.Reality || e.Password != "secret" {
		t.Fatalf("%+v", e)
	}
	if e.SNI != "de1.example.com" {
		t.Fatalf("sni %q", e.SNI)
	}
	e, err = ParseURL("trojan://p%40ss@1.2.3.4:8443?security=reality&pbk=abc&allowInsecure=1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !e.Reality || e.Password != "p@ss" || !e.Insecure {
		t.Fatalf("%+v", e)
	}
	e, err = ParseURL("node.example.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if e.Addr != "node.example.com:443" || e.Password != "secret" {
		t.Fatalf("%+v", e)
	}
}

func TestParseURLRealityXHTTP(t *testing.T) {
	raw := "trojan://secret@de1.example.com:443?security=reality&sni=www.microsoft.com&fp=chrome&pbk=q3ucJuIkRdJYS8_N0Si4U7FA3Qo7jqcblDghyDUXIc8&sid=0123456789abcdef&type=tcp#n"
	e, err := ParseURL(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if !e.Reality || e.Network != "tcp" || e.Fingerprint != "chrome" || e.Password != "secret" {
		t.Fatalf("%+v", e)
	}
	e, err = ParseURL("trojan://secret@de1.example.com:443?type=xhttp&path=/api&mode=auto&security=tls", "")
	if err != nil {
		t.Fatal(err)
	}
	if !e.xhttp() || e.Path != "/api" || e.Mode != "auto" {
		t.Fatalf("%+v", e)
	}
}

func TestUDPPacketRoundtrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	want := []byte("dns-query")
	dst := &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53}
	errc := make(chan error, 1)
	go func() {
		errc <- writeUDPPacket(a, dst, want)
	}()
	addr, payload, err := readUDPPacket(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if string(payload) != string(want) {
		t.Fatalf("payload %q", payload)
	}
	u := addr.(*net.UDPAddr)
	if !u.IP.Equal(dst.IP) || u.Port != 53 {
		t.Fatalf("addr %v", addr)
	}
}

func TestUDPConnRoundtrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	cli := &Client{}
	ua := &udpConn{Conn: a, c: cli, local: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}}
	ub := &udpConn{Conn: b, c: cli, local: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}}
	msg := []byte("hello trojan udp")
	dst := &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 53}
	errc := make(chan error, 1)
	go func() {
		_, err := ua.WriteTo(msg, dst)
		errc <- err
	}()
	buf := make([]byte, 64)
	n, addr, err := ub.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != string(msg) {
		t.Fatalf("got %q", buf[:n])
	}
	if addr == nil {
		t.Fatal("nil addr")
	}
}

func TestNewAcceptsReality(t *testing.T) {
	cli, err := New(Config{
		URL: "trojan://secret@127.0.0.1:443?security=reality&pbk=q3ucJuIkRdJYS8_N0Si4U7FA3Qo7jqcblDghyDUXIc8&sid=abcd",
	})
	if err != nil {
		t.Fatal(err)
	}
	cli.Close()
}

func TestNewRejectsKCP(t *testing.T) {
	_, err := New(Config{URL: "trojan://secret@127.0.0.1:443?type=kcp"})
	if err == nil {
		t.Fatal("want transport error")
	}
}

func TestNewRejectsEmptyPassword(t *testing.T) {
	_, err := New(Config{URL: "trojan://@127.0.0.1:443?security=tls"})
	if err == nil {
		t.Fatal("want password error")
	}
}

func TestWriteRequestPipe(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	errc := make(chan error, 1)
	go func() {
		errc <- writeRequest(a, "password", "example.com:443", false)
	}()
	hdr := make([]byte, 76)
	if _, err := io.ReadFull(b, hdr); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if hdr[58] != cmdTCP {
		t.Fatalf("cmd")
	}
}
