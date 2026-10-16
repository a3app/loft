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
// Only protocol abilities, never build or OS version.
type Cap uint64

// Capability bits on Hello/Status. SOCKS, TUN, CDN vendor, OS, and origin names are not caps.
const (
	CapMulti     Cap = 1 << iota // several instances on one key
	CapResume                    // ok_resumed + lease_hold
	CapStickyIP                  // reserved overlay IP on the key
	CapVLAN                      // VLAN from the key
	CapRules                     // switch ACL
	CapGET                       // GET + header uplink
	CapWS                        // WebSocket
	CapStatus                    // Hello/Status with Code
	CapTimers                    // keepalive / idle / reconnect in Status
	CapHelloCaps                 // this cap block
	CapReset                     // CmdReset on streams
	CapQUIC                      // QUIC/UDP front (loss recovery, migration)
	CapDgram                     // CmdDgram UDP
	CapClockless                 // auth without client clock
	CapTicket                    // resume ticket across paths/replicas
	CapDecoy                     // Status lists this node's decoy origins
	CapH2                        // HTTP/2 CONNECT front
	CapH3                        // HTTP/3 (ALPN h3) front
	CapMask                      // decoy mask: borrowed origin cert on loft path
	CapRelay                     // this session is a relay trunk to the controller
	CapRoster                    // Status may carry a signed relay roster
	CapLists                     // Status may advertise address-list serial/hash
	CapP2P                       // peer-to-peer overlay when mesh
	CapDNS                       // Status may carry DNS servers; gateway:53 is a resolver
	CapConnect                   // HTTP/2 CONNECT splice; TCP without inner mux
	CapResin                     // CmdReport + resin/draft GET uplink
	CapHold                      // mux streams survive a brief front drop
	capSentinel
)

// NodeCaps is every capability this implementation advertises on Status.
const NodeCaps = CapMulti | CapResume | CapStickyIP | CapVLAN | CapRules |
	CapGET | CapWS | CapStatus | CapTimers | CapHelloCaps | CapReset |
	CapQUIC | CapDgram | CapClockless | CapTicket | CapDecoy |
	CapH2 | CapH3 | CapMask | CapRelay | CapRoster | CapLists | CapP2P | CapDNS |
	CapConnect | CapResin | CapHold

// capName is bit i of Cap in protocol order (see protocol.md §5.3).
var capName = [...]string{
	"multi", "resume", "sticky_ip", "vlan", "rules",
	"get", "ws", "status", "timers", "hello_caps", "reset",
	"quic", "dgram", "clockless", "ticket", "decoy",
	"h2", "h3", "mask", "relay", "roster", "lists", "p2p", "dns",
	"connect", "resin", "hold",
}

// Has reports whether all bits in x are set.
func (c Cap) Has(x Cap) bool { return c&x == x }

// Names returns the set bit names in protocol order (multi, resume, …).
func (c Cap) Names() []string {
	var out []string
	for i := 0; i < int(capSentinel); i++ {
		bit := Cap(1) << i
		if c.Has(bit) && i < len(capName) {
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
