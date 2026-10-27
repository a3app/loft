package wire

// Protocol version 0x0100 (1.0), capability bits, and handshake Negotiate.

import "fmt"

// Protocol version: high byte major, low byte minor. 0x0100 is 1.0.
//
// Major bump = incompatible framing or auth. Minor = new caps, old clients
// still speak. Handshake picks the highest version in both ranges.
const (
	Proto    uint16 = 0x0100 // 1.0 until the first release; no bump while stubs remain
	ProtoMin uint16 = 0x0100
)

// ProtoString formats a protocol version as "major.minor" (0x0100 is "1.0").
func ProtoString(v uint16) string {
	return fmt.Sprintf("%d.%d", v>>8, v&0xff)
}

// Cap is a feature bit advertised on the inner handshake (inside TLS).
// Only protocol abilities, never build or OS version. The client lists its
// caps in Hello, the node lists its own in Status, and each side uses a
// feature only when the other side has the bit.
type Cap uint64

// Capability bits on Hello/Status. SOCKS, TUN, CDN vendor, OS, and origin names are not caps.
const (
	CapMulti       Cap = 1 << iota // several instances on one key
	CapResume                      // ok_resumed + lease_hold
	CapStickyIP                    // reserved overlay IP on the key
	CapVLAN                        // VLAN from the key
	CapRules                       // switch ACL
	CapGET                         // GET + header uplink
	CapWS                          // WebSocket
	CapStatus                      // Hello/Status with Code
	CapTimers                      // keepalive / idle / reconnect in Status
	CapHelloCaps                   // this cap block
	CapReset                       // CmdReset on streams
	CapQUIC                        // QUIC/UDP front (loss recovery, migration)
	CapDgram                       // CmdDgram UDP
	CapClockless                   // auth without client clock
	CapTicket                      // Status ticket is the session sid for 0x04 attach
	CapDecoy                       // Status lists this node's decoy origins
	CapH2                          // HTTP/2 CONNECT front
	CapH3                          // HTTP/3 (ALPN h3) front
	CapMask                        // decoy mask: borrowed origin cert on loft path
	CapRelay                       // Hello: this session is a relay trunk (ExtraCaps only; not a member default)
	CapRoster                      // Status may carry a signed relay roster
	CapLists                       // Status may advertise address-list serial/hash
	CapP2P                         // peer-to-peer overlay when mesh
	CapDNS                         // Status may carry DNS servers; gateway:53 is a resolver
	CapConnect                     // HTTP/2 CONNECT splice; TCP without inner mux
	CapResin                       // CmdReport + resin/draft GET uplink
	CapHold                        // mux streams survive a brief front drop
	CapQUICMark                    // quic front on a decoy node: the Initial must carry the loft mark
	CapH3Mark                      // h3 front on a decoy node: the Initial must carry the loft mark
	CapL3                          // HTTP/2 CONNECT carrying AEAD IP datagrams (no mux)
	CapListsInline                 // Lists command inside the session (§6, §10.3)
	capSentinel
)

// CapL3Shape is an experimental bit (branch experimentos, bits 48–63 are
// the experimental class). A client sets it in Hello to ask for l3 shaping;
// a node that implements it echoes it in Status only when the Hello had it,
// and then both sides shape what they send on that l3 session: the start of
// each TCP flow is corked and padded, and the first data of a flow from the
// peer is answered at once with a padding-only message (transport/l3shape.go).
// It is not in NodeCaps: a node must not set an experimental bit toward a
// client that did not ask for it.
const CapL3Shape Cap = 1 << 48

// NodeCaps is every capability this implementation advertises on Status.
// CapRelay stays here so a controller still shows it accepts trunks.
// Members MUST NOT copy this bit into Hello (see MemberCaps).
const NodeCaps = CapMulti | CapResume | CapStickyIP | CapVLAN | CapRules |
	CapGET | CapWS | CapStatus | CapTimers | CapHelloCaps | CapReset |
	CapQUIC | CapDgram | CapClockless | CapTicket | CapDecoy |
	CapH2 | CapH3 | CapMask | CapRelay | CapRoster | CapLists | CapP2P | CapDNS |
	CapConnect | CapResin | CapHold | CapL3 | CapListsInline

// MemberCaps is the default Hello offer for loft-front / Supervise.
// CapRelay is omitted: a member must not self-assert a trunk (hub:* / ctl:1).
// loft-node in role relay ORs it back via ClientConfig.ExtraCaps.
// CapHold stays so a path drop still parks the mux (fresh 0x02, same instance_id).
const MemberCaps = NodeCaps &^ CapRelay

// capName is the name of bit i of Cap, in protocol order. The order must
// match the iota list above, because bit numbers are fixed on the wire.
var capName = [...]string{
	"multi", "resume", "sticky_ip", "vlan", "rules",
	"get", "ws", "status", "timers", "hello_caps", "reset",
	"quic", "dgram", "clockless", "ticket", "decoy",
	"h2", "h3", "mask", "relay", "roster", "lists", "p2p", "dns",
	"connect", "resin", "hold", "quic_mark", "h3_mark", "l3",
	"lists_inline",
}

// Has reports whether all bits in x are set.
func (c Cap) Has(x Cap) bool { return c&x == x }

// Names returns the set bit names in protocol order (multi, resume, …).
func (c Cap) Names() []string {
	var out []string
	for i := 0; i < len(capName); i++ {
		if c.Has(Cap(1) << i) {
			out = append(out, capName[i])
		}
	}
	return out
}

// Offer is one side of version+caps. No software identity.
type Offer struct {
	Min  uint16
	Max  uint16
	Caps Cap
}

// NodeOffer is this node's version range (1.0) and full cap set.
func NodeOffer() Offer {
	return Offer{Min: ProtoMin, Max: Proto, Caps: NodeCaps}
}

// Negotiate picks a version both speak. Caps in the result are the node's
// advertised set (what it can do), not the intersection: the client decides
// what to use. Empty peer (old hello) is treated as 1.0 with unknown caps.
func Negotiate(peer, node Offer) (selected uint16, err error) {
	if peer.Max == 0 {
		peer.Min, peer.Max = ProtoMin, ProtoMin
	}
	if node.Max == 0 {
		node = NodeOffer()
	}
	lo := peer.Min
	if node.Min > lo {
		lo = node.Min
	}
	hi := peer.Max
	if node.Max < hi {
		hi = node.Max
	}
	if hi < lo {
		return 0, Fail(Protocol, "no common protocol "+ProtoString(peer.Min)+"-"+ProtoString(peer.Max)+
			" vs "+ProtoString(node.Min)+"-"+ProtoString(node.Max))
	}
	return hi, nil
}
