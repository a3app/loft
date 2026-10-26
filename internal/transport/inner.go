package transport

// Inner cipher choice. Every mux frame is sealed with the session key even
// inside TLS. ChaCha20-Poly1305 is the default and always allowed. On a
// direct front (TLS ends at the node) a client may ask for AES-256-GCM, or
// for "clear" (no second layer; TLS still protects the bytes). The client
// asks in Hello, the node confirms in Status, and both sides then bind that
// cipher to the session key. GET never leaves ChaCha: its TLS ends at a
// CDN, which would otherwise see the frames.

import (
	"sync/atomic"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

// innerAllow is the node's extra data-cipher list (clear, aes). Empty means
// ChaCha only. Set from node.conf at start. A running session keeps the id
// it already agreed.
var innerAllow atomic.Value // []byte

// clientInnerBits packs the Hello ask: the low byte is the cipher id, and
// bit 8 set means the ask byte is sent at all.
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
// GET never confirms. Call this before Seal of Status: Status itself is
// still sealed with the default ChaCha; the new cipher starts after it.
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

// takeInner is the client side of commitInner: it binds the cipher Status
// chose, on a direct front only. GET stays ChaCha even when Status carries
// another id, and an unknown id is ignored (ChaCha stays).
func takeInner(front string, key []byte, st wire.Status) {
	if front == "" || front == wire.FrontGET || !st.InnerSet {
		return
	}
	switch st.Inner {
	case wire.InnerClear, wire.InnerAES:
		ecrypto.BindInner(key, st.Inner)
	}
}
