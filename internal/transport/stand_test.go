package transport

import (
	"testing"

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
