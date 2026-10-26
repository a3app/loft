package transport

// Tests for named-list lookup priority: drop, then direct, then via, then default.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/testcfg"
	"github.com/a3app/loft/internal/wire"
)

// TestLookupPriority checks drop wins over direct on the same host, then default via.
func TestLookupPriority(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	l := wire.Lists{
		Serial: 1,
		Sets: []wire.AddrList{
			{Name: "yandex", Entries: []wire.Addr{wire.ParseAddr(testcfg.Decoy2())}},
			{Name: "drop", Entries: []wire.Addr{wire.ParseAddr(testcfg.Decoy2())}},
			{Name: "via", Entries: []wire.Addr{wire.ParseAddr(testcfg.CDN())}},
		},
	}
	l.Pub = priv.Public().(ed25519.PublicKey)
	l.Sig = ed25519.Sign(priv, l.Body())
	resetRosterForTest()
	t.Cleanup(resetRosterForTest)
	PinControllerPub(l.Pub)
	SetUse(&config.ClientConfig{Use: map[string]string{"yandex": "direct", "drop": "drop", "via": "via"}, Default: "via"})
	if err := RememberLists(l); err != nil {
		t.Fatal(err)
	}
	if LookupAddr(testcfg.Decoy2(), nil) != ActDrop {
		t.Fatal("drop wins")
	}
	if LookupAddr(testcfg.CDN(), nil) != ActVia {
		t.Fatal("via list")
	}
	if LookupAddr("example.com", nil) != ActVia {
		t.Fatal("default")
	}
	if LookupAddr("", net.ParseIP("8.8.8.8")) != ActVia {
		t.Fatal("ip default")
	}
}

func TestBootstrapListsSkipsNodeWhenEdgeIsSet(t *testing.T) {
	var hit atomic.Bool
	hang := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hit.Store(true)
		time.Sleep(time.Second)
	}))
	t.Cleanup(hang.Close)
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(edge.Close)
	node := "http://" + hang.Listener.Addr().String()
	SetDirectNode(node)
	t.Cleanup(func() { SetDirectNode("") })
	cfg := &config.ClientConfig{
		Outbounds: []config.Outbound{
			{Name: "edge", Type: "get", CDN: "yandex", URL: edge.URL},
			{Name: "node", Type: "get", CDN: "yandex", URL: node},
			{Name: "h2", Type: "h2", CDN: "yandex", URL: node + "/api/v1/tunnel"},
		},
	}
	start := time.Now()
	BootstrapLists(context.Background(), cfg)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("took %s", time.Since(start))
	}
	if hit.Load() {
		t.Fatal("GET lists on node")
	}
}

func TestBootstrapListsSkipsDirectNode(t *testing.T) {
	var hit atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hit.Store(true)
		time.Sleep(time.Second)
	}))
	t.Cleanup(ts.Close)
	origin := "http://" + ts.Listener.Addr().String()
	cfg := &config.ClientConfig{
		Bootstrap: origin + "/static/v3/manifest.json",
		ListsURL:  origin + "/static/v3/lists.json",
		Outbounds: []config.Outbound{{Name: "node", Type: "ws", URL: origin + "/api/v1/sync"}},
	}
	start := time.Now()
	BootstrapLists(context.Background(), cfg)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("took %s", time.Since(start))
	}
	if hit.Load() {
		t.Fatal("GET lists on decoy node")
	}
}

// signedLists is a one-set document signed by priv.
func signedLists(priv ed25519.PrivateKey, serial uint64, host string) wire.Lists {
	l := wire.Lists{Serial: serial, Sets: []wire.AddrList{{Name: "out", Entries: []wire.Addr{wire.ParseAddr(host)}}}}
	l.Pub = priv.Public().(ed25519.PublicKey)
	l.Sig = ed25519.Sign(priv, l.Body())
	return l
}

// TestListsNeedControllerKey: a validly signed document from a key that is
// not the controller's is refused, so whoever serves lists.json (a CDN, a
// box on the path) cannot steer traffic out of the tunnel.
func TestListsNeedControllerKey(t *testing.T) {
	_, ctl, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	resetRosterForTest()
	t.Cleanup(resetRosterForTest)
	liveBook.Store(nil)
	t.Cleanup(func() { liveBook.Store(nil) })
	SetUse(&config.ClientConfig{Use: map[string]string{"out": "direct"}, Default: "via"})

	if err := RememberLists(signedLists(ctl, 1, "a.example")); err == nil {
		t.Fatal("no pin and no TOFU: lists accepted")
	}
	PinControllerPub(ctl.Public().(ed25519.PublicKey))
	if err := RememberLists(signedLists(other, 9, "evil.example")); err == nil {
		t.Fatal("foreign signer accepted")
	}
	if LookupAddr("evil.example", nil) == ActDirect {
		t.Fatal("foreign lists took effect")
	}
	if err := RememberLists(signedLists(ctl, 5, "a.example")); err != nil {
		t.Fatal(err)
	}
	if LookupAddr("a.example", nil) != ActDirect {
		t.Fatal("controller lists not applied")
	}
	if err := RememberLists(signedLists(ctl, 4, "b.example")); err == nil {
		t.Fatal("older serial accepted")
	}
	if err := RememberLists(signedLists(ctl, 5, "a.example")); err != nil {
		t.Fatalf("same serial refused: %v", err)
	}
}

// TestListsTOFUPinsSigner: with tofu on and nothing pinned, the first signer
// is pinned and a later document from another key is refused.
func TestListsTOFUPinsSigner(t *testing.T) {
	_, first, _ := ed25519.GenerateKey(rand.Reader)
	_, second, _ := ed25519.GenerateKey(rand.Reader)
	resetRosterForTest()
	t.Cleanup(resetRosterForTest)
	liveBook.Store(nil)
	t.Cleanup(func() { liveBook.Store(nil) })
	AllowTOFU(true)
	if err := RememberLists(signedLists(first, 1, "a.example")); err != nil {
		t.Fatal(err)
	}
	if err := RememberLists(signedLists(second, 2, "b.example")); err == nil {
		t.Fatal("second signer accepted after TOFU pin")
	}
}
