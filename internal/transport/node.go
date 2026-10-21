package transport

// NodeInfo is stamped into Status by every front: caps, decoy, mask, roster, lists.

import (
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// NodeInfo is what every front stamps into Status.
type NodeInfo struct {
	Token       []byte
	Decoy       []string
	Caps        wire.Cap
	MaskPub     []byte
	Replicas    []string
	CanaryFlags byte
	CanaryPad   uint16
	QuicALPN    string
	Roster      []byte
	RosterFn    func() []byte
	ListsSerial uint64
	ListsHash   []byte
	// ListsFn, when set, gives the current lists serial and hash (lists
	// are re-signed on reload).
	ListsFn func() (uint64, []byte)
}

// Status builds the first AEAD Status from session defaults and this node's offer.
// sid is the GET HTTP sid (or "ws" / "quic"). Overlay instance_id is Hello /
// overlay Bind, not this sid — mixing them made GET X-Auth adopt miss the
// parked mux after RememberVis.
func (n NodeInfo) Status(sid string) wire.Status {
	st := netconf.DefaultSession().Status(wire.OK, "", "", "", 1280, 0)
	st.Caps = n.Caps
	st.Decoy = append([]string(nil), n.Decoy...)
	st.MaskPub = append([]byte(nil), n.MaskPub...)
	st.Replicas = append([]string(nil), n.Replicas...)
	st.CanaryFlags = n.CanaryFlags
	st.CanaryPad = n.CanaryPad
	if n.RosterFn != nil {
		st.Roster = n.RosterFn()
	} else {
		st.Roster = append([]byte(nil), n.Roster...)
	}
	st.ListsSerial = n.ListsSerial
	st.ListsHash = append([]byte(nil), n.ListsHash...)
	if n.ListsFn != nil {
		ser, h := n.ListsFn()
		st.ListsSerial, st.ListsHash = ser, append([]byte(nil), h...)
	}
	return st
}

// withDecoy copies decoy hostnames onto st.
func withDecoy(st wire.Status, decoy []string) wire.Status {
	st.Decoy = append([]string(nil), decoy...)
	return st
}

// applyNode overlays non-zero NodeInfo caps, decoy, and mask_pub onto st.
func applyNode(st wire.Status, n NodeInfo) wire.Status {
	if n.Caps != 0 {
		st.Caps = n.Caps
	}
	if len(n.Decoy) > 0 {
		st.Decoy = append([]string(nil), n.Decoy...)
	}
	if len(n.MaskPub) > 0 {
		st.MaskPub = append([]byte(nil), n.MaskPub...)
	}
	return st
}
