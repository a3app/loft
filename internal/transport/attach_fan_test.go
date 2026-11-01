package transport

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

func TestAttachFanStreamAffinity(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	var mu sync.Mutex
	got := map[int][][]byte{}
	var dead1, dead2 atomic.Bool
	write := func(id int) func([]byte) error {
		return func(p []byte) error {
			mu.Lock()
			got[id] = append(got[id], append([]byte(nil), p...))
			mu.Unlock()
			return nil
		}
	}
	lanes := []fanLane{
		{id: 10, dead: &dead1, write: write(10)},
		{id: 20, dead: &dead2, write: write(20)},
	}
	fan := &attachFan{key: key, bind: map[uint32]int{}}
	fan.send(lanes, []wire.Frame{
		{Cmd: wire.CmdData, StreamID: 1, Payload: []byte("a")},
		{Cmd: wire.CmdData, StreamID: 2, Payload: []byte("b")},
		{Cmd: wire.CmdData, StreamID: 1, Payload: []byte("c")},
	})
	fan.send(lanes, []wire.Frame{
		{Cmd: wire.CmdData, StreamID: 1, Payload: []byte("d")},
		{Cmd: wire.CmdData, StreamID: 2, Payload: []byte("e")},
	})
	ids := map[int]map[uint32]bool{}
	for lane, blobs := range got {
		ids[lane] = map[uint32]bool{}
		for _, b := range blobs {
			plain, err := ecrypto.Open(key, b, nil)
			if err != nil {
				t.Fatal(err)
			}
			frames, err := wire.DecodeBatch(plain)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range frames {
				ids[lane][f.StreamID] = true
			}
		}
	}
	for _, sid := range []uint32{1, 2} {
		n := 0
		for _, set := range ids {
			if set[sid] {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("stream %d on %d lanes: %+v", sid, n, ids)
		}
	}

	dead1.Store(true)
	got = map[int][][]byte{}
	fan.send(lanes, []wire.Frame{
		{Cmd: wire.CmdData, StreamID: 1, Payload: []byte("z")},
	})
	if len(got[20]) != 1 || len(got[10]) != 0 {
		t.Fatalf("dead lane kept stream: %+v", got)
	}
}
