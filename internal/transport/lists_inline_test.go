package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

func signProfileLists(t *testing.T, priv ed25519.PrivateKey, serial uint64, extra int) wire.Lists {
	t.Helper()
	entries := []wire.Addr{
		{Kind: wire.KindDir, Val: wire.DirDirect},
		{Kind: wire.KindLabel, Val: "Russia"},
		{Kind: wire.KindCIDR, Val: "5.255.255.0/24"},
		{Kind: wire.KindSuffix, Val: "yandex.ru"},
	}
	for i := 0; i < extra; i++ {
		entries = append(entries, wire.Addr{Kind: wire.KindSuffix, Val: fmt.Sprintf("h%d.example", i)})
	}
	l := wire.Lists{Serial: serial, Sets: []wire.AddrList{
		{Name: "", Entries: []wire.Addr{{Kind: wire.KindLabel, Val: "RU direct"}, {Kind: wire.KindDNS, Val: "77.88.8.8"}}},
		{Name: "ru", Entries: entries},
		{Name: "ads", Entries: []wire.Addr{{Kind: wire.KindDir, Val: wire.DirDrop}, {Kind: wire.KindSuffix, Val: "doubleclick.net"}}},
	}}
	l.Pub = priv.Public().(ed25519.PublicKey)
	l.Sig = ed25519.Sign(priv, l.Body())
	return l
}

func waitListsSerial(t *testing.T, serial uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ListsSerial() == serial {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("serial %d, have %d", serial, ListsSerial())
}

func TestAskListsAssembles(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	resetRosterForTest()
	t.Cleanup(resetRosterForTest)
	liveBook.Store(nil)
	t.Cleanup(func() { liveBook.Store(nil) })
	PinControllerPub(priv.Public().(ed25519.PublicKey))

	l := signProfileLists(t, priv, 20261027, 0)
	blob := l.Encode()
	h := l.Hash()
	oldBlob := ListsBlob
	t.Cleanup(func() { ListsBlob = oldBlob })
	ListsBlob = func() (uint64, []byte) { return l.Serial, blob }

	c, s := muxPair(t)
	bindServerLists(s)
	AskLists(c, wire.Status{
		Caps:        wire.CapListsInline,
		ListsSerial: l.Serial,
		ListsHash:   h[:],
	})
	waitListsSerial(t, l.Serial)
	if LookupAddr("yandex.ru", nil) != ActDirect {
		t.Fatal("kind 4 direct")
	}
	if LookupAddr("doubleclick.net", nil) != ActDrop {
		t.Fatal("kind 4 drop")
	}
	if LookupAddr("other.example", nil) != ActVia {
		t.Fatal("default")
	}
	p := l.RouteProfile()
	if p.Name != "RU direct" || p.DNS != "77.88.8.8" || len(p.Sets) != 2 {
		t.Fatalf("%+v", p)
	}
}

func TestAskListsMultiPiece(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	resetRosterForTest()
	t.Cleanup(resetRosterForTest)
	liveBook.Store(nil)
	t.Cleanup(func() { liveBook.Store(nil) })
	PinControllerPub(priv.Public().(ed25519.PublicKey))

	l := signProfileLists(t, priv, 9, 2500)
	blob := l.Encode()
	if len(blob) <= wire.MaxPayload-16 {
		t.Fatalf("blob too small %d", len(blob))
	}
	h := l.Hash()
	oldBlob := ListsBlob
	t.Cleanup(func() { ListsBlob = oldBlob })
	ListsBlob = func() (uint64, []byte) { return l.Serial, blob }

	c, s := muxPair(t)
	bindServerLists(s)
	AskLists(c, wire.Status{
		Caps:        wire.CapListsInline,
		ListsSerial: l.Serial,
		ListsHash:   h[:],
	})
	waitListsSerial(t, l.Serial)
	if LookupAddr("h0.example", nil) != ActDirect {
		t.Fatal("padded suffix")
	}
}

func TestAskListsNothingNewer(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	resetRosterForTest()
	t.Cleanup(resetRosterForTest)
	liveBook.Store(nil)
	t.Cleanup(func() { liveBook.Store(nil) })
	PinControllerPub(priv.Public().(ed25519.PublicKey))
	l := signProfileLists(t, priv, 3, 0)
	if err := RememberLists(l); err != nil {
		t.Fatal(err)
	}
	oldBlob := ListsBlob
	t.Cleanup(func() { ListsBlob = oldBlob })
	ListsBlob = func() (uint64, []byte) { return l.Serial, l.Encode() }

	c, s := muxPair(t)
	bindServerLists(s)
	h := l.Hash()
	AskLists(c, wire.Status{
		Caps:        wire.CapListsInline,
		ListsSerial: l.Serial,
		ListsHash:   h[:],
	})
	time.Sleep(50 * time.Millisecond)
	if ListsSerial() != 3 {
		t.Fatalf("%d", ListsSerial())
	}
}

func TestListsProfileJSON(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	resetRosterForTest()
	t.Cleanup(resetRosterForTest)
	l := signProfileLists(t, priv, 20261027, 0)
	PinControllerPub(l.Pub)
	liveBook.Store(nil)
	liveProfile.Store("")
	if err := RememberLists(l); err != nil {
		t.Fatal(err)
	}
	raw := ListsProfileJSON()
	if raw == "" {
		t.Fatal("empty")
	}
	if want, _ := l.RouteProfile().SplitJSON(); string(want) != raw {
		t.Fatalf("%s", raw)
	}
}

func TestRememberListsKind4FillsUse(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	resetRosterForTest()
	t.Cleanup(resetRosterForTest)
	liveBook.Store(nil)
	t.Cleanup(func() { liveBook.Store(nil) })
	PinControllerPub(priv.Public().(ed25519.PublicKey))
	SetUse(&config.ClientConfig{Use: map[string]string{"ru": "via"}, Default: "via"})
	l := signProfileLists(t, priv, 1, 0)
	if err := RememberLists(l); err != nil {
		t.Fatal(err)
	}
	if LookupAddr("yandex.ru", nil) != ActVia {
		t.Fatal("yaml use wins")
	}
	if LookupAddr("doubleclick.net", nil) != ActDrop {
		t.Fatal("kind 4 fills gap")
	}
}

func TestMaybeRefreshListsSkipsGETWhenInline(t *testing.T) {
	var hit atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hit.Store(true)
	}))
	t.Cleanup(ts.Close)
	listSources.Store([]string{ts.URL})
	t.Cleanup(func() { listSources.Store([]string(nil)) })
	maybeRefreshLists(wire.Status{
		Caps:        wire.CapListsInline,
		ListsSerial: 99,
		ListsHash:   make([]byte, 32),
	})
	if hit.Load() {
		t.Fatal("GET lists while lists_inline")
	}
}

func TestServerCapsClearsListsInline(t *testing.T) {
	off := ServerCaps(&config.ServerConfig{}, false)
	if off.Has(wire.CapLists) || off.Has(wire.CapListsInline) {
		t.Fatalf("empty lists still advertised: %v", off.Names())
	}
	on := ServerCaps(&config.ServerConfig{Lists: map[string][]string{"ru": {"yandex.ru"}}}, false)
	if !on.Has(wire.CapLists) || !on.Has(wire.CapListsInline) {
		t.Fatalf("%v", on.Names())
	}
}

func TestAskListsWrongOffsetDropped(t *testing.T) {
	c := mux.NewClient()
	t.Cleanup(func() { c.Close() })
	x := &listsXfer{have: 0, hash: [32]byte{}}
	x.onPiece(c, 1, 10, 4, []byte("nope"))
	if x.serial != 1 || len(x.buf) != 0 {
		t.Fatalf("accepted wrong offset: %+v", x)
	}
}

func TestLookupKind4IP(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	resetRosterForTest()
	t.Cleanup(resetRosterForTest)
	liveBook.Store(nil)
	t.Cleanup(func() { liveBook.Store(nil) })
	PinControllerPub(priv.Public().(ed25519.PublicKey))
	l := signProfileLists(t, priv, 2, 0)
	if err := RememberLists(l); err != nil {
		t.Fatal(err)
	}
	if LookupAddr("", net.ParseIP("5.255.255.1")) != ActDirect {
		t.Fatal("cidr")
	}
}
