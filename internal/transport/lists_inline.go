package transport

// Lists command inside the mux (§6, §10.3). Node pieces, client assemble.

import (
	"crypto/sha256"
	"sync"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// ListsBlob is the node's signed lists body. Origin sets it. Nil or a
// zero serial means the node has nothing to send.
var ListsBlob func() (serial uint64, blob []byte)

// bindServerLists answers CmdLists asks. ListsBlob is read off the Push
// path so a slow sign cannot stall the mux.
func bindServerLists(ms *mux.Session) {
	if ms == nil {
		return
	}
	ms.OnListsAsk(func(have uint64, offset uint32) {
		go func() {
			serial, blob := uint64(0), []byte(nil)
			if ListsBlob != nil {
				serial, blob = ListsBlob()
			}
			_ = ms.Enqueue(wire.ListsReplyFrame(have, offset, serial, blob))
		}()
	})
}

// AskLists starts an inline fetch when Status lists_inline is set and the
// live book is older than Status. have stays the old serial for every
// piece of the new body. l3 has no mux and does not call this.
func AskLists(ms *mux.Session, st wire.Status) {
	if ms == nil || ms.Closed() {
		return
	}
	if !st.Caps.Has(wire.CapListsInline) || st.ListsSerial == 0 || len(st.ListsHash) != 32 {
		return
	}
	if b := liveBook.Load(); b != nil {
		if st.ListsSerial < b.ser {
			return
		}
		if st.ListsSerial == b.ser && bytesEqual32(b.hash, st.ListsHash) {
			return
		}
	}
	var want [32]byte
	copy(want[:], st.ListsHash)
	x := &listsXfer{have: ListsSerial(), hash: want}
	ms.OnListsPiece(func(serial uint64, total, offset uint32, body []byte) {
		x.onPiece(ms, serial, total, offset, body)
	})
	_ = ms.Enqueue(wire.ListsAskFrame(x.have, 0))
}

// listsXfer assembles one signed body. Pieces with another serial or an
// offset the client did not ask for are dropped.
type listsXfer struct {
	mu     sync.Mutex
	have   uint64
	serial uint64
	total  uint32
	want   uint32
	buf    []byte
	hash   [32]byte
}

func (x *listsXfer) onPiece(ms *mux.Session, serial uint64, total, offset uint32, body []byte) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if total == 0 {
		return
	}
	if x.serial != 0 && serial != x.serial {
		return
	}
	if x.serial == 0 {
		x.serial = serial
		x.total = total
		x.buf = make([]byte, 0, total)
		x.want = 0
	}
	if offset != x.want {
		return
	}
	if uint64(len(x.buf))+uint64(len(body)) > uint64(x.total) {
		return
	}
	x.buf = append(x.buf, body...)
	x.want = uint32(len(x.buf))
	if x.want < x.total {
		have, off := x.have, x.want
		go func() { _ = ms.Enqueue(wire.ListsAskFrame(have, off)) }()
		return
	}
	buf, hash := x.buf, x.hash
	go func() {
		if sha256.Sum256(buf) != hash {
			return
		}
		l, err := wire.DecodeLists(buf)
		if err != nil {
			return
		}
		_ = RememberLists(l)
	}()
}
