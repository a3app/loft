package transport

// Auth helpers verify member keys and stamp overlay identity into Status.
//
// A member is a device or person holding an lt_… key. The node checks a
// client twice: first by the TLS ClientHello session id (a "sid" mark that
// only loft clients produce, so the decoy listener can tell loft from a
// browser), then by the 0x02 handshake (nonce, ephemeral X25519 key and an
// HMAC that proves the member key). After that, the member's Hello is bound
// to the overlay: it gets its virtual address and the node learns which
// member it is.

import (
	"net"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// AuthKeys is the node’s member keyring. Non-empty: clients authenticate with
// a member key; LOFT_TOKEN stays the node secret (mask).
var AuthKeys *identity.Ring

// RelayKeys is the controller's relay keyring: one key per relay, issued for
// its id. A relay key opens only a relay trunk, never a member session, and
// a member key never opens a trunk.
var RelayKeys *identity.Ring

// AuthShared accepts LOFT_TOKEN as the client secret when the ring is empty.
// Default false (issue per-member keys). Node config: `auth shared enable`.
// A non-empty ring never mixes the node token in, even with this flag on.
var AuthShared bool

// clientTokens is the member keyring, or LOFT_TOKEN only when AuthShared and
// the ring is empty. Overlay HMAC follows whichever secret matched.
func clientTokens(fallback []byte) [][]byte {
	var out [][]byte
	if AuthKeys != nil && AuthKeys.Len() > 0 {
		out = AuthKeys.Tokens()
	} else if AuthShared && len(fallback) > 0 {
		out = [][]byte{fallback}
	}
	if RelayKeys != nil && RelayKeys.Len() > 0 {
		out = append(out, RelayKeys.Tokens()...)
	}
	return out
}

// relayIDFor is the relay id a key was issued for, or "" for any other key.
func relayIDFor(token []byte) string {
	if RelayKeys == nil {
		return ""
	}
	return RelayKeys.IDFor(token)
}

// frontAuth checks 0x02 (nonce || eph_c || HMAC) against every accepted
// client secret. seen rejects a replayed nonce.
func frontAuth(fallback, blob []byte, seen *ecrypto.Nonces) (ecrypto.Auth, error) {
	return ecrypto.FinishAuth(clientTokens(fallback), blob, seen)
}

// verifyAny reports whether hello.SessionID is loft: sid v3 under the node
// X25519 (fallback is LOFT_TOKEN), then sid v2 under a member key.
// sid v3 needs the node's private key to check, so a stolen member key cannot
// forge it; sid v2 is kept for clients that have not learned mask_pub yet.
// The decoy listener does not use this for a mask node's decoy SNI: there
// only sid v3 is admitted.
func verifyAny(fallback []byte, h clientHello) bool {
	if verifySID3(fallback, h) {
		return true
	}
	return verifyMemberV2(fallback, h)
}

// verifyMemberV2 reports whether hello.SessionID is sid v2 under a member key.
func verifyMemberV2(fallback []byte, h clientHello) bool {
	for _, tok := range clientTokens(fallback) {
		if verifySessionID(tok, h) {
			return true
		}
	}
	return false
}

// stampOverlayAuth binds the session to the overlay after Hello decode.
// With a member keyring, the member id comes from the key that passed 0x02,
// not from what the client wrote in Hello, so a member cannot claim another
// member's identity. An unknown key turns Status into AuthBadKey.
func stampOverlayAuth(st *wire.Status, rest []byte, sess *mux.Session, remote net.Addr, token []byte) {
	ov := NetSwitch()
	if ov == nil || st == nil {
		return
	}
	if sess != nil && sess.Relay() {
		return // a relay trunk has no overlay address
	}
	if relayIDFor(token) != "" {
		st.Code = wire.AuthBadKey // a relay key is not a member key
		return
	}
	h, _ := wire.DecodeHello(rest)
	if AuthKeys != nil && AuthKeys.Len() > 0 {
		id := AuthKeys.IDFor(token)
		if id == "" {
			st.Code = wire.AuthBadKey
			return
		}
		h.MemberID = id
	}
	ov.Bind(st, h, sess, remote)
}

// stampOverlayAuthL3 binds an L3 IP pump the same way Bind binds a mux session.
func stampOverlayAuthL3(st *wire.Status, rest []byte, sink l3Sink, remote net.Addr, token []byte) {
	ov := NetSwitch()
	if ov == nil || st == nil {
		return
	}
	if relayIDFor(token) != "" {
		st.Code = wire.AuthBadKey // a relay key is not a member key
		return
	}
	h, _ := wire.DecodeHello(rest)
	if AuthKeys != nil && AuthKeys.Len() > 0 {
		id := AuthKeys.IDFor(token)
		if id == "" {
			st.Code = wire.AuthBadKey
			return
		}
		h.MemberID = id
	}
	ov.BindL3(st, h, sink, remote)
}

// finishStatusL3 stamps overlay identity onto st for an L3 session.
func finishStatusL3(st *wire.Status, token []byte, rest []byte, sink l3Sink, remote net.Addr) {
	if st == nil {
		return
	}
	stampOverlayAuthL3(st, rest, sink, remote, token)
	if st.Code.OK() && st.IP == "" {
		st.IP = netconf.FallbackClientIP
	}
	if st.Code.OK() && st.Gateway == "" {
		st.Gateway = netconf.FallbackGateway
	}
}

// finishStatus stamps overlay identity onto st. It runs once per session,
// after 0x02 passed and Hello was read. If the overlay gave no address, the
// fallback client IP and gateway are used so the client still has a
// working TUN.
func finishStatus(st *wire.Status, token []byte, sid string, rest []byte, sess *mux.Session, remote net.Addr) {
	if st == nil {
		return
	}
	// Trunk role needs the authenticated token; attachHello only saw Hello bytes.
	applyRelayRole(sess, rest, token)
	stampOverlayAuth(st, rest, sess, remote, token)
	if st.Code.OK() && st.IP == "" {
		st.IP = netconf.FallbackClientIP
	}
	if st.Code.OK() && st.Gateway == "" {
		st.Gateway = netconf.FallbackGateway
	}
}

// applyRelayRole makes sess a relay trunk when the key that passed 0x02 is a
// relay key and Hello asks for the trunk (CapRelay). The trunk carries the id
// the key was issued for, so ctl:1 can hold the relay to that id. A relay
// that is drained or no longer listed is refused. Any other key, or a Hello
// without CapRelay, leaves an ordinary session.
// Hold / instance_id are not touched: a gap still parks the mux.
func applyRelayRole(ms *mux.Session, rest, token []byte) {
	if ms == nil {
		return
	}
	ms.ClearRelay()
	id := relayIDFor(token)
	if id == "" {
		return
	}
	h, err := wire.DecodeHello(rest)
	if err != nil || !h.Caps.Has(wire.CapRelay) {
		return
	}
	if FleetCtl == nil || !FleetCtl.allowed(id) {
		return
	}
	ms.SetRelay(id)
}
