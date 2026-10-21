package transport

// Auth helpers verify member keys and stamp overlay identity into Status.

import (
	"net"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// AuthKeys is the node’s member keyring. Non-empty: clients authenticate with
// a member key; LOFT_TOKEN stays the node secret (mask, controller seed).
var AuthKeys *identity.Ring

// AuthShared accepts LOFT_TOKEN as the client secret when the ring is empty.
// Default false (issue per-member keys). Node config: `auth shared enable`.
// A non-empty ring never mixes the node token in, even with this flag on.
var AuthShared bool

// clientTokens is the member keyring, or LOFT_TOKEN only when AuthShared and
// the ring is empty. Overlay HMAC follows whichever secret matched.
func clientTokens(fallback []byte) [][]byte {
	if AuthKeys != nil && AuthKeys.Len() > 0 {
		return AuthKeys.Tokens()
	}
	if AuthShared && len(fallback) > 0 {
		return [][]byte{fallback}
	}
	return nil
}

// frontAuth checks 0x02 (nonce || eph_c || HMAC).
func frontAuth(fallback, blob []byte, seen *ecrypto.Nonces) (ecrypto.Auth, error) {
	return ecrypto.FinishAuth(clientTokens(fallback), blob, seen)
}

// verifyAny reports whether hello.SessionID is loft: sid v3 under the node
// X25519 (fallback is LOFT_TOKEN), then sid v2 under a member key.
func verifyAny(fallback []byte, h clientHello) bool {
	if verifySID3(fallback, h) {
		return true
	}
	for _, tok := range clientTokens(fallback) {
		if verifySessionID(tok, h) {
			return true
		}
	}
	return false
}

// stampOverlayAuth binds the session to the overlay after Hello decode.
func stampOverlayAuth(st *wire.Status, rest []byte, sess *mux.Session, remote net.Addr, token []byte) {
	ov := NetSwitch()
	if ov == nil || st == nil {
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

// finishStatus stamps overlay identity onto st.
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

// applyRelayRole sets mux.SetRelay only for a real relay trunk.
// Hold / instance_id are not touched: a gap still parks the mux.
func applyRelayRole(ms *mux.Session, rest, token []byte) {
	if ms == nil {
		return
	}
	h, err := wire.DecodeHello(rest)
	if err != nil || !h.Caps.Has(wire.CapRelay) {
		// Stock loft-front omits CapRelay (MemberCaps). Clear in case a
		// parked mux still had the bit from an older Hello.
		ms.ClearRelay()
		return
	}
	if !relayTrunkAllowed(h, token) {
		ms.ClearRelay()
		return
	}
	ms.SetRelay()
}

// relayTrunkAllowed is the enrol allowlist for CapRelay.
// enrol:open — any authenticated Hello with the bit (loft-node ExtraCaps).
// enrol:list — member id from the key ring (or Hello) must be in relays:.
// Empty id is the shared-token relay; ServeCtl still checks enrol_req id.
func relayTrunkAllowed(h wire.Hello, token []byte) bool {
	if FleetCtl == nil || FleetCtl.cfg == nil {
		return true
	}
	if FleetCtl.cfg.Enrol == "open" {
		return true
	}
	id := h.MemberID
	if AuthKeys != nil && AuthKeys.Len() > 0 {
		if found := AuthKeys.IDFor(token); found != "" {
			id = found
		}
	}
	if id == "" {
		return true
	}
	return FleetCtl.allowed(id)
}
