package transport

import (
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

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
	got := takeParked("phone")
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
	if takeParked("expire") != nil {
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
	ms, reused := adoptServerMux(h.Encode())
	if reused || !ms.Holding() || ms.Instance() != "a" {
		t.Fatalf("first adopt reused=%v hold=%v inst=%q", reused, ms.Holding(), ms.Instance())
	}
	parkServer(ms)
	ms2, reused := adoptServerMux(h.Encode())
	if !reused || ms2 != ms {
		t.Fatalf("resume reused=%v", reused)
	}
	ms.Close()
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
