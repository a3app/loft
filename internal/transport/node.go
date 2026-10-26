package transport

// NodeInfo is stamped into Status by every front: caps, decoy, mask, roster, lists.
//
// Status is the node's first encrypted answer after the client's 0x02
// handshake and Hello. Most of it is per-session (overlay address, timers),
// but part of it describes the node itself: which fronts it accepts and on
// which HTTP paths, the decoy site behind it, the mask key, the signed roster
// and lists. That node-wide part is the same for every front, so it lives in
// one NodeInfo value that each front copies into its Status.

import (
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// NodeInfo is what every front stamps into Status.
type NodeInfo struct {
	Token []byte
	// Decoy is the real website(s) behind the node. Non-loft traffic is
	// handed to it, so a scanner sees an ordinary site. The first entry is
	// the identity the client uses as SNI.
	Decoy []string
	Caps  wire.Cap
	// MaskPub is the node's X25519 public key for mask mode (a certificate
	// shaped like the decoy's). Empty means mask is off.
	MaskPub []byte
	// Replicas are extra host:port addresses of this node for failover.
	Replicas []string
	// CanaryFlags and CanaryPad tell the client how to shape its next
	// ClientHello (bit0 fat hello, bit1 split ClientHello; extra padding).
	// The canary checks that no middlebox rewrote the TLS handshake.
	CanaryFlags byte
	CanaryPad   uint16
	QuicALPN    string
	// Roster is the controller-signed list of live fronts and relays, so the
	// client knows where to fail over. RosterFn, when set, wins: it returns
	// the current roster, which changes as relays come and go.
	Roster      []byte
	RosterFn    func() []byte
	ListsSerial uint64
	ListsHash   []byte
	// ListsFn, when set, gives the current lists serial and hash (lists
	// are re-signed on reload).
	ListsFn func() (uint64, []byte)
	GetPath string
	WSPath  string
	H2Path  string
	H3Path  string
}

// Status builds the first AEAD Status from session defaults and this node's offer.
// sid is the GET HTTP sid (or "ws" / "quic"). Only a real GET sid is sent
// back as the ticket, which other fronts present later to attach to the same
// session. Overlay instance_id is Hello / overlay Bind, not this sid — mixing
// them made the GET handshake miss (fail to adopt) the parked mux after
// RememberVis.
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
	if validGETSID(sid) {
		st.Ticket = []byte(sid)
	}
	st.Fronts = n.frontEPs()
	return st
}

// frontEPs lists mux fronts this node accepts, with HTTP paths. connect/l3 stay off this map:
// they do not carry a mux (connect splices one TCP connection, l3 carries
// raw IP packets), so they are not mux fronts. An empty path falls back to the config default; h3 reuses the h2 path.
func (n NodeInfo) frontEPs() []wire.FrontEP {
	var out []wire.FrontEP
	for _, f := range n.Caps.Fronts() {
		if f == wire.FrontConnect || f == wire.FrontL3 {
			continue
		}
		ep := wire.FrontEP{Name: f}
		switch f {
		case wire.FrontGET:
			ep.Path = n.GetPath
			if ep.Path == "" {
				ep.Path = config.DefaultGETPath
			}
		case wire.FrontWS:
			ep.Path = n.WSPath
			if ep.Path == "" {
				ep.Path = config.DefaultWSPath
			}
		case wire.FrontH2:
			ep.Path = n.H2Path
			if ep.Path == "" {
				ep.Path = config.DefaultH2Path
			}
		case wire.FrontH3:
			ep.Path = n.H3Path
			if ep.Path == "" {
				ep.Path = n.H2Path
			}
			if ep.Path == "" {
				ep.Path = config.DefaultH2Path
			}
		}
		out = append(out, ep)
	}
	return out
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
