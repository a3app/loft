package transport

// NodeInfo is stamped into Status by every front: caps, decoy, mask, roster, lists.

import (
	"time"

	ecrypto "github.com/a3app/loft/internal/crypto"
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
}

// Status builds the first AEAD Status from session defaults and this node's offer.
func (n NodeInfo) Status(sid string) wire.Status {
	st := netconf.DefaultSession().Status(wire.OK, "", "", sid, 1280, 0)
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
	if len(n.Token) > 0 && sid != "" {
		st.Ticket = ecrypto.MakeTicket(n.Token, sid, 24*time.Hour)
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
