package transport

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// realHello builds the first TLS record a Chrome-shaped client sends for sni.
func realHello(t *testing.T, sni string) []byte {
	t.Helper()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	got := make(chan []byte, 1)
	go func() {
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(b, hdr); err != nil {
			got <- nil
			return
		}
		body := make([]byte, int(hdr[3])<<8|int(hdr[4]))
		_, _ = io.ReadFull(b, body)
		got <- append(hdr, body...)
	}()
	u := utls.UClient(a, &utls.Config{ServerName: sni}, utls.HelloChrome_Auto)
	go func() { _ = u.Handshake() }()
	select {
	case rec := <-got:
		if rec == nil {
			t.Fatal("no ClientHello")
		}
		return rec
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	return nil
}

func TestSNISpanFindsName(t *testing.T) {
	const sni = "files.example-node.net"
	rec := realHello(t, sni)
	off, n, err := sniSpan(rec)
	if err != nil {
		t.Fatal(err)
	}
	if string(rec[off:off+n]) != sni {
		t.Fatalf("span %q", rec[off:off+n])
	}
	cut := disorderCut(rec)
	if cut <= off || cut >= off+n {
		t.Fatalf("cut %d not inside name [%d,%d)", cut, off, off+n)
	}
}

func TestSNISpanRejectsOther(t *testing.T) {
	for _, rec := range [][]byte{nil, {0x17, 3, 3, 0, 1, 0}, bytes.Repeat([]byte{0x16}, 50)} {
		if _, _, err := sniSpan(rec); err == nil {
			t.Fatalf("accepted %x", rec)
		}
	}
}

// ttlLog records setTTL calls and what was written while each TTL was set.
type ttlLog struct {
	net.Conn
	ttl    int
	writes []struct {
		ttl int
		n   int
	}
}

func (l *ttlLog) Write(p []byte) (int, error) {
	l.writes = append(l.writes, struct{ ttl, n int }{l.ttl, len(p)})
	return l.Conn.Write(p)
}

func TestDisorderConnCutsAndRestores(t *testing.T) {
	rec := realHello(t, "a-rather-long-node-name.example.org")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	log := &ttlLog{Conn: a, ttl: 64}
	d := &disorderConn{Conn: log, setTTL: func(ttl int) (int, error) {
		old := log.ttl
		log.ttl = ttl
		return old, nil
	}}
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, len(rec)+5)
		_, _ = io.ReadFull(b, buf)
		got <- buf
	}()
	if n, err := d.Write(rec); err != nil || n != len(rec) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := d.Write([]byte{1, 2, 3, 4, 5}); err != nil {
		t.Fatal(err)
	}
	out := <-got
	if !bytes.Equal(out[:len(rec)], rec) {
		t.Fatal("bytes changed")
	}
	if len(log.writes) != 3 || log.writes[0].ttl != 1 || log.writes[1].ttl != 64 || log.writes[2].ttl != 64 {
		t.Fatalf("writes %+v", log.writes)
	}
	if log.writes[0].n != disorderCut(rec) {
		t.Fatalf("first part %d want %d", log.writes[0].n, disorderCut(rec))
	}
	if log.ttl != 64 {
		t.Fatalf("ttl left at %d", log.ttl)
	}
}

// TestDisorderRealSocket runs the real TTL switch on a loopback TCP socket.
// Loopback does not drop TTL-1 packets, so this checks the socket calls
// and that the server still reads the whole ClientHello in order.
func TestDisorderRealSocket(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	rec := realHello(t, "node.example.com")
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer c.Close()
		buf := make([]byte, len(rec))
		_, _ = io.ReadFull(c, buf)
		got <- buf
	}()
	raw, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tuneTCP(raw)
	SetDisorder(true)
	defer SetDisorder(false)
	c := helloConn(raw, CanaryPolicy{Split: true})
	if _, ok := c.(*disorderConn); !ok {
		t.Fatalf("got %T, want disorder over split", c)
	}
	if _, err := c.Write(rec); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(<-got, rec) {
		t.Fatal("server read other bytes")
	}
	old, err := setSockTTL(tcpOf(raw), 64)
	if err != nil {
		t.Fatal(err)
	}
	if old <= 1 {
		t.Fatalf("ttl not restored: %d", old)
	}
}

func TestHelloConnOffKeepsSplit(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	SetDisorder(false)
	if _, ok := helloConn(a, CanaryPolicy{Split: true}).(*splitHelloConn); !ok {
		t.Fatal("split lost")
	}
	if helloConn(a, CanaryPolicy{}) != a {
		t.Fatal("wrapped without need")
	}
}
