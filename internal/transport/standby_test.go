package transport

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

func ctlWith(auto bool, relays ...string) *config.ServerConfig {
	c := &config.ServerConfig{Relays: []config.RelayEntry{{ID: "r2", Replica: true, AutoPromote: auto}}}
	for _, id := range relays {
		c.Relays = append(c.Relays, config.RelayEntry{ID: id})
	}
	return c
}

func TestPromoteDecision(t *testing.T) {
	now := time.Now()
	after := 5 * time.Minute
	book := &witnessBook{}
	report := func(id string, sees bool, at time.Time) { book.put(wire.Witness{ID: id, At: at.Unix(), Sees: sees}) }
	report("ru1", false, now)
	report("ru2", false, now)
	report("ru3", true, now)

	if !promoteDecision(ctlWith(true, "ru1", "ru2", "ru3"), "r2", 6*time.Minute, after, book, now) {
		t.Fatal("2 of 3 relays lost the controller: should promote")
	}
	if promoteDecision(ctlWith(false, "ru1", "ru2", "ru3"), "r2", 6*time.Minute, after, book, now) {
		t.Fatal("promoted without auto_promote")
	}
	if promoteDecision(ctlWith(true, "ru1", "ru2", "ru3"), "r2", time.Minute, after, book, now) {
		t.Fatal("promoted before promote_after")
	}
	if promoteDecision(ctlWith(true, "ru1", "ru2", "ru3", "ru4"), "r2", 6*time.Minute, after, book, now) {
		t.Fatal("2 of 4 is not a majority")
	}
	if promoteDecision(ctlWith(true, "ru1"), "r2", 6*time.Minute, after, book, now) {
		t.Fatal("one relay is not enough to promote")
	}
	stale := &witnessBook{}
	stale.put(wire.Witness{ID: "ru1", At: now.Add(-time.Minute).Unix()})
	stale.put(wire.Witness{ID: "ru2", At: now.Add(-time.Minute).Unix()})
	if promoteDecision(ctlWith(true, "ru1", "ru2"), "r2", 6*time.Minute, after, stale, now) {
		t.Fatal("stale reports counted")
	}
}

// TestWitnessReportsReachStandby: reports arrive as POSTs inside TLS on
// the standby's relay listener, several on one connection, and count only
// under the relay's own key from the standby copy. Other traffic is not
// taken for a report.
func TestWitnessReportsReachStandby(t *testing.T) {
	sb := t.TempDir()
	os.WriteFile(filepath.Join(sb, "relays.keys"), []byte("ru1: lt_ru1\nru2: lt_ru2\n"), 0o600)
	certFile, keyFile := quicCertFiles(t)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	wit := &witnessSink{book: &witnessBook{}, standby: sb}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go relayConn(&Holder{}, &relayRuntime{}, c, wit)
		}
	}()
	dial := func() (net.Conn, *bufio.Reader) {
		c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c, bufio.NewReader(c)
	}
	report := func(id, key string, sees bool) []byte {
		return wire.EncodeWitness(wire.Witness{ID: id, At: time.Now().Unix(), Sees: sees}, []byte(key))
	}

	c, br := dial()
	if err := postWitness(c, br, "r2.example", report("ru1", "lt_ru1", true)); err != nil {
		t.Fatal(err)
	}
	if err := postWitness(c, br, "r2.example", report("ru1", "lt_ru1", false)); err != nil {
		t.Fatalf("second report on the same connection: %v", err)
	}
	if wit.book.lost([]string{"ru1"}, time.Now()) != 1 {
		t.Fatal("ru1's report was not counted")
	}
	c, br = dial()
	if err := postWitness(c, br, "r2.example", report("ru2", "lt_wrong", false)); err == nil || wit.book.lost([]string{"ru2"}, time.Now()) != 0 {
		t.Fatalf("a report signed with a wrong key was taken: %v", err)
	}
}

// TestPeekedConnKeepsBytes: what relayConn peeked still reaches the hub.
func TestPeekedConnKeepsBytes(t *testing.T) {
	a, b := net.Pipe()
	go func() { a.Write([]byte("GET /static/v3/build HTTP/1.1\r\n")); a.Close() }()
	br := bufio.NewReader(b)
	if _, err := br.Peek(len(witnessPrefix)); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(&peekedConn{Conn: b, r: br})
	if string(got) != "GET /static/v3/build HTTP/1.1\r\n" {
		t.Fatalf("%q", got)
	}
}

func TestJitter(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if d := jitter(10 * time.Second); d < 8*time.Second || d > 12*time.Second {
			t.Fatal(d)
		}
	}
}
