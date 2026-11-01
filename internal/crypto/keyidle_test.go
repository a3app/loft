package crypto

// Idle session keys are forgotten; keys in use are kept.

import (
	"bytes"
	"testing"
	"time"

	"github.com/a3app/loft/internal/wire"
)

func TestIdleKeysForgotten(t *testing.T) {
	stale := bytes.Repeat([]byte{0x51}, 32)
	live := bytes.Repeat([]byte{0x52}, 32)
	BindInner(stale, wire.InnerAES)
	if _, err := Seal(live, []byte("x"), nil); err != nil {
		t.Fatal(err)
	}
	v, _ := sealers.Load(string(stale))
	v.(*sealState).used.Store(time.Now().Add(-keyIdle - time.Minute).Unix())

	lastKeySweep.Store(0)
	if _, err := Seal(live, []byte("y"), nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := sealers.Load(string(stale)); ok {
		t.Fatal("idle key kept in sealers")
	}
	if _, ok := innerModes.Load(string(stale)); ok {
		t.Fatal("idle key kept in innerModes")
	}
	if _, ok := sealers.Load(string(live)); !ok {
		t.Fatal("live key forgotten")
	}
}

// TestOpenKeepsKeyAlive: a key that is only opened with (receive side) is
// still marked as used.
func TestOpenKeepsKeyAlive(t *testing.T) {
	key := bytes.Repeat([]byte{0x53}, 32)
	blob, err := Seal(key, []byte("z"), nil)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := sealers.Load(string(key))
	st := v.(*sealState)
	st.used.Store(1)
	if _, err := Open(key, blob, nil); err != nil {
		t.Fatal(err)
	}
	if time.Now().Unix()-st.used.Load() > 5 {
		t.Fatal("Open did not mark the key as used")
	}
}
