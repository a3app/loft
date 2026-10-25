package transport

import (
	"sync/atomic"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

// innerAllow is the node's extra data-cipher list (clear, aes). Empty means
// ChaCha only. Set from node.conf at start. A running session keeps the id
// it already agreed.
var innerAllow atomic.Value // []byte

// clientInnerBits packs the Hello ask. Bit 8 set means the byte is sent.
var clientInnerBits atomic.Uint32

// SetInnerAllow replaces the node allow-list. ChaCha stays allowed either way.
func SetInnerAllow(ids []byte) {
	innerAllow.Store(append([]byte(nil), ids...))
}

func currentInnerAllow() []byte {
	v, _ := innerAllow.Load().([]byte)
	return v
}

func setClientInner(set bool, id byte) {
	v := uint32(id)
	if set {
		v |= 0x100
	}
	clientInnerBits.Store(v)
}

func clientInnerAsk() (set bool, id byte) {
	v := clientInnerBits.Load()
	return v&0x100 != 0, byte(v)
}

// applyInner writes the Status byte when policy confirms the Hello ask.
// GET never confirms. Call this before Seal of Status.
func applyInner(st *wire.Status, front string, hello []byte) {
	if st == nil {
		return
	}
	h, err := wire.DecodeHello(hello)
	if err != nil {
		return
	}
	set, id := wire.AgreeInner(front, h.InnerSet, h.Inner, currentInnerAllow())
	st.InnerSet = set
	st.Inner = id
}

// commitInner binds the agreed data cipher. Call it after Seal of Status
// and before any data frame on this key.
func commitInner(key []byte, st wire.Status) {
	if !st.InnerSet {
		return
	}
	ecrypto.BindInner(key, st.Inner)
}

// takeInner binds the Status choice on a direct front. GET stays ChaCha
// even when Status carries another id.
func takeInner(front string, key []byte, st wire.Status) {
	if front == "" || front == wire.FrontGET || !st.InnerSet {
		return
	}
	switch st.Inner {
	case wire.InnerClear, wire.InnerAES:
		ecrypto.BindInner(key, st.Inner)
	}
}
