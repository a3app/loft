package transport

import (
	"testing"
	"time"

	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

func TestParkOrCloseDeadMuxCloses(t *testing.T) {
	ms := mux.NewClient()
	ms.Close()
	d := &sessDialer{sess: ms, done: make(chan struct{})}
	if err := parkOrClose(d); err != nil {
		t.Fatal(err)
	}
	if d.park != 0 || d.cls != 1 {
		t.Fatalf("park=%d close=%d", d.park, d.cls)
	}
}

func TestParkOrCloseLiveParks(t *testing.T) {
	t.Cleanup(func() {
		visCaps.Store(0)
		visGapMS.Store(0)
	})
	visCaps.Store(uint64(wire.CapHold))
	ms := mux.NewClient()
	ms.SetHold(true)
	d := &sessDialer{sess: ms, done: make(chan struct{})}
	if err := parkOrClose(d); err != nil {
		t.Fatal(err)
	}
	if d.park != 1 || d.cls != 0 {
		t.Fatalf("park=%d close=%d", d.park, d.cls)
	}
	ms.Close()
}

func TestParkTakeSameMux(t *testing.T) {
	t.Cleanup(func() { gapHoldFn = defaultGapHold })
	gapHoldFn = func() time.Duration { return time.Second }
	ms := mux.NewServer()
	ms.SetHold(true)
	ms.SetInstance("phone")
	st, err := ms.Open("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	parkServer(ms)
	got := takeParked("", "phone")
	if got != ms || got.Closed() {
		t.Fatalf("want parked mux, got %v closed=%v", got, ms.Closed())
	}
	if got.StreamCount() != 1 {
		t.Fatalf("stream lost: %d", got.StreamCount())
	}
	_ = st.Close()
	ms.Close()
}

func TestParkExpireCloses(t *testing.T) {
	t.Cleanup(func() { gapHoldFn = defaultGapHold })
	gapHoldFn = func() time.Duration { return 20 * time.Millisecond }
	ms := mux.NewServer()
	ms.SetHold(true)
	ms.SetInstance("expire")
	parkServer(ms)
	time.Sleep(60 * time.Millisecond)
	if takeParked("", "expire") != nil {
		t.Fatal("expired park still live")
	}
	if !ms.Closed() {
		t.Fatal("mux should close after gap_hold")
	}
}

func TestAdoptReusesHello(t *testing.T) {
	t.Cleanup(func() { gapHoldFn = defaultGapHold })
	gapHoldFn = func() time.Duration { return time.Second }
	h := wire.Hello{Version: 1, InstanceID: "a", Min: wire.ProtoMin, Max: wire.Proto, Caps: wire.CapHold}
	ms, reused := adoptServerMux(h.Encode(), nil)
	if reused || !ms.Holding() || ms.Instance() != "a" {
		t.Fatalf("first adopt reused=%v hold=%v inst=%q", reused, ms.Holding(), ms.Instance())
	}
	parkServer(ms)
	ms2, reused := adoptServerMux(h.Encode(), nil)
	if !reused || ms2 != ms {
		t.Fatalf("resume reused=%v", reused)
	}
	ms.Close()
}

// TestGETSpawnReusesMux: a handshake with the same instance_id keeps the mux.
func TestGETSpawnReusesMux(t *testing.T) {
	gs := NewGETServer("tok", "/static/v3/build")
	h := wire.Hello{Version: 1, InstanceID: "phone", Min: wire.ProtoMin, Max: wire.Proto, Caps: wire.CapHold}
	rest := h.Encode()
	a := gs.spawn([]byte("k1"), rest, nil)
	if a.mux.Instance() != "phone" {
		t.Fatalf("inst %q", a.mux.Instance())
	}
	st, err := a.mux.Open("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := gs.spawn([]byte("k2"), rest, nil)
	if b.mux != a.mux {
		t.Fatal("mux not adopted")
	}
	gs.mu.Lock()
	n := len(gs.sessions)
	gs.mu.Unlock()
	if n != 1 {
		t.Fatalf("sessions %d", n)
	}
	if a.mux.StreamCount() != 1 {
		t.Fatalf("streams %d", a.mux.StreamCount())
	}
	a.mux.Close()
}

// TestGETSpawnRejectsOtherMember: a second key with the same instance_id
// does not take the first member's live mux.
func TestGETSpawnRejectsOtherMember(t *testing.T) {
	prev := AuthKeys
	t.Cleanup(func() { AuthKeys = prev })
	ring := identity.NewRing()
	ring.Put("alice", "lt_alice")
	ring.Put("bob", "lt_bob")
	AuthKeys = ring
	gs := NewGETServer("node", "/static/v3/build")
	h := wire.Hello{Version: 1, InstanceID: "phone", Min: wire.ProtoMin, Max: wire.Proto, Caps: wire.CapHold}
	rest := h.Encode()
	a := gs.spawn([]byte("k1"), rest, []byte("lt_alice"))
	st, err := a.mux.Open("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := gs.spawn([]byte("k2"), rest, []byte("lt_bob"))
	if b.mux == a.mux {
		t.Fatal("bob adopted alice mux")
	}
	c := gs.spawn([]byte("k3"), rest, []byte("lt_alice"))
	if c.mux != a.mux {
		t.Fatal("alice did not resume")
	}
	if a.mux.Owner() != "alice" || b.mux.Owner() != "bob" {
		t.Fatalf("owners %q %q", a.mux.Owner(), b.mux.Owner())
	}
	gs.mu.Lock()
	n := len(gs.sessions)
	gs.mu.Unlock()
	if n != 2 {
		t.Fatalf("sessions %d", n)
	}
	a.mux.Close()
	b.mux.Close()
}

func TestParkTakeSameMemberOnly(t *testing.T) {
	t.Cleanup(func() { gapHoldFn = defaultGapHold })
	gapHoldFn = func() time.Duration { return time.Second }
	ms := mux.NewServer()
	ms.SetHold(true)
	ms.SetInstance("phone")
	ms.SetOwner("alice")
	parkServer(ms)
	if takeParked("bob", "phone") != nil {
		t.Fatal("bob took alice park")
	}
	got := takeParked("alice", "phone")
	if got != ms {
		t.Fatal("alice missed own park")
	}
	ms.Close()
}

func TestReleaseHoldLeavesNewerFront(t *testing.T) {
	t.Cleanup(clearClientHold)
	a, g1 := takeClientHold()
	b, g2 := takeClientHold()
	if a != b || g1 == g2 {
		t.Fatalf("reuse a=%p b=%p gen %d %d", a, b, g1, g2)
	}
	if releaseClientHold(g1) {
		t.Fatal("stale owner released")
	}
	if b.Closed() {
		t.Fatal("stale close killed the live mux")
	}
	if !releaseClientHold(g2) || !b.Closed() {
		t.Fatal("current owner did not close")
	}
}

func TestClientHoldMuxReusesUntilClear(t *testing.T) {
	t.Cleanup(clearClientHold)
	a := clientHoldMux()
	parkClient(a, "sid", []byte("k"), 7)
	b := clientHoldMux()
	if a != b {
		t.Fatal("want same mux")
	}
	sid, key, seq := clientHoldGET()
	if sid != "sid" || string(key) != "k" || seq != 7 {
		t.Fatalf("%s %q %d", sid, key, seq)
	}
	clearClientHold()
	c := clientHoldMux()
	if c == a {
		t.Fatal("cleared mux reused")
	}
}

func TestForgetGETSidKeepsMux(t *testing.T) {
	t.Cleanup(clearClientHold)
	a := clientHoldMux()
	parkClient(a, "sid", []byte("k"), 7)
	forgetGETSid()
	sid, key, seq := clientHoldGET()
	if sid != "" || key != nil || seq != 0 {
		t.Fatalf("held %s %q %d", sid, key, seq)
	}
	if a.Closed() {
		t.Fatal("mux closed")
	}
	b := clientHoldMux()
	if a != b {
		t.Fatal("want same mux")
	}
}
