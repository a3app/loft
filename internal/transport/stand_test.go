package transport

import (
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

func TestStripePackRoundTrip(t *testing.T) {
	frames := []wire.Frame{
		{Cmd: wire.CmdData, StreamID: 1, Payload: []byte("one")},
		{Cmd: wire.CmdData, StreamID: 1, Payload: []byte("two")},
		{Cmd: wire.CmdPing, StreamID: 0, Payload: []byte{1}},
	}
	raw, err := stripePack(frames, []uint32{2, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if !isStripe(raw) {
		t.Fatal("magic")
	}
	seqs, got, err := stripeParse(raw[1:])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || seqs[0] != 2 || seqs[1] != 0 || seqs[2] != 1 {
		t.Fatalf("seqs %v frames %d", seqs, len(got))
	}
	if string(got[0].Payload) != "one" || string(got[1].Payload) != "two" {
		t.Fatalf("payload %q %q", got[0].Payload, got[1].Payload)
	}
}

func TestStripeReorder(t *testing.T) {
	rb := &stripeRB{hold: map[uint32]wire.Frame{}}
	take := func(seq uint32, payload string) []string {
		f := wire.Frame{Cmd: wire.CmdData, StreamID: 1, Payload: []byte(payload)}
		var ready []wire.Frame
		if seq < rb.next {
			return nil
		}
		if seq == rb.next {
			ready = append(ready, f)
			rb.next++
			for {
				h, ok := rb.hold[rb.next]
				if !ok {
					break
				}
				delete(rb.hold, rb.next)
				ready = append(ready, h)
				rb.next++
			}
		} else {
			rb.hold[seq] = f
		}
		var out []string
		for _, fr := range ready {
			out = append(out, string(fr.Payload))
		}
		return out
	}
	if got := take(1, "b"); len(got) != 0 {
		t.Fatalf("early %v", got)
	}
	got := take(0, "a")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("order %v", got)
	}
	got = take(2, "c")
	if len(got) != 1 || got[0] != "c" {
		t.Fatalf("tail %v", got)
	}
}

func stripeBody(t *testing.T, seqs []uint32) []byte {
	t.Helper()
	frames := make([]wire.Frame, len(seqs))
	for i := range frames {
		frames[i] = wire.Frame{Cmd: wire.CmdPing}
	}
	raw, err := stripePack(frames, seqs)
	if err != nil {
		t.Fatal(err)
	}
	return raw[1:]
}

// TestStripeOffOutsideStand: without LOFT_STAND_FAN=stripe a stripe batch
// is ignored and leaves no per-session state behind.
func TestStripeOffOutsideStand(t *testing.T) {
	t.Setenv("LOFT_STAND_FAN", "")
	sess := mux.NewServer()
	defer sess.Close()
	stripeIngest(sess, stripeBody(t, []uint32{5}))
	if _, ok := stripeTabs.Load(sess); ok {
		t.Fatal("stripe state kept outside stand mode")
	}
}

// TestStripeHoldCapClosesMux: a peer that never fills the gap cannot make
// the buffer grow past stripeHoldMax; the mux is closed instead, and its
// state is removed.
func TestStripeHoldCapClosesMux(t *testing.T) {
	t.Setenv("LOFT_STAND_FAN", "stripe")
	sess := mux.NewServer()
	seqs := make([]uint32, stripeHoldMax+1)
	for i := range seqs {
		seqs[i] = uint32(i + 1) // seq 0 never comes
	}
	stripeIngest(sess, stripeBody(t, seqs))
	if !sess.Closed() {
		t.Fatal("overflow did not close the mux")
	}
	waitStripeGone(t, sess)
}

// TestStripeStateGoneAfterClose: the reorder buffer goes with its mux.
func TestStripeStateGoneAfterClose(t *testing.T) {
	t.Setenv("LOFT_STAND_FAN", "stripe")
	sess := mux.NewServer()
	stripeIngest(sess, stripeBody(t, []uint32{0, 2}))
	if _, ok := stripeTabs.Load(sess); !ok {
		t.Fatal("no stripe state in stand mode")
	}
	sess.Close()
	waitStripeGone(t, sess)
}

func waitStripeGone(t *testing.T, sess *mux.Session) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if _, ok := stripeTabs.Load(sess); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stripe state kept after the mux closed")
}
