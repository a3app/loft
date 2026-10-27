// Package transport implements Loft 1.0 fronts, TLS camouflage, decoy splice,
// borrowed-cert mask, CONNECT splice, and client dialing.
//
// A front is one way to carry the mux (many user connections inside one
// session) over something that looks like ordinary HTTPS: get (all traffic
// rides plain GET requests, so it can pass through a CDN), ws (WebSocket),
// h2 and h3 (one HTTP CONNECT stream), quic, and l3 (raw IP packets). The
// inner protocol is the same on every front; only the packaging changes.
//
// Both sides live here. The client picks a front, opens TLS with a
// browser-like ClientHello, sends the 0x02 handshake (proof of the member
// key), reads Status, and keeps the session alive across network changes
// (dial.go, tls.go, get.go). The node accepts those connections, checks the
// key, and hands every mux stream to the overlay or the internet (hub.go).
// The rest protects against detection: the decoy (a real website that gets
// every non-loft visitor), the mask (a certificate shaped like the decoy's),
// and the canary (a check that no middlebox rewrote the TLS handshake).
package transport

// This file remembers, per node address, what the last Status said (which
// fronts the node serves and its mask_pub key), and builds the capability
// bits each side advertises.

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

// nodeView is what the last Status from one node address said: the fronts
// it serves and mask_pub, the node's X25519 public key for mask mode.
// Without mask_pub the client cannot build a mask-ready ClientHello.
type nodeView struct {
	fronts  []string
	maskPub []byte
}

// views caches Status fronts and mask_pub per node address. rotateN counts
// failed or dropped sessions; filterOutbounds uses it to start the next
// attempt from a different front, so a front that keeps failing does not
// stay first forever.
var views sync.Map // addr -> nodeView
var rotateN atomic.Uint64

// RotateCount is how many times Supervise has moved to the next front.
// loft-bench uses it so a forced switch lands on the front it asked for.
func RotateCount() uint64 { return rotateN.Load() }

// RememberFronts stores the fronts the node at addr listed in its Status
// caps. Later dials skip configured fronts the node does not serve.
func RememberFronts(addr string, caps wire.Cap) {
	fronts := caps.Fronts()
	v, _ := views.LoadOrStore(addr, nodeView{})
	cur := v.(nodeView)
	cur.fronts = append([]string(nil), fronts...)
	views.Store(addr, cur)
}

// RememberMaskPub stores Status mask_pub for addr.
func RememberMaskPub(addr string, pub []byte) {
	v, _ := views.LoadOrStore(addr, nodeView{})
	cur := v.(nodeView)
	cur.maskPub = append([]byte(nil), pub...)
	views.Store(addr, cur)
}

// CachedFronts returns a copy of stored fronts for addr.
func CachedFronts(addr string) ([]string, bool) {
	v, ok := views.Load(addr)
	if !ok {
		return nil, false
	}
	f := v.(nodeView).fronts
	if f == nil {
		return nil, false
	}
	return append([]string(nil), f...), true
}

// CachedMaskPub returns a copy of stored mask_pub for addr.
func CachedMaskPub(addr string) []byte {
	v, ok := views.Load(addr)
	if !ok {
		return nil
	}
	return append([]byte(nil), v.(nodeView).maskPub...)
}

// ParseMaskPub decodes a 32-byte X25519 public key from hex or base64
// (URL-safe with or without padding, or standard). Anything else, including
// a key of the wrong length, gives nil.
func ParseMaskPub(s string) []byte {
	if s == "" {
		return nil
	}
	if b := parseHexPub(s); len(b) == 32 {
		return b
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b
	}
	return nil
}

// filterOutbounds keeps the outbounds whose front the client config allows
// and, once a Status from this node is cached, that the node advertised.
// It then rotates the list by rotateN so each retry starts one front later.
// The node address is taken from the first outbound.
func filterOutbounds(cfg *config.ClientConfig, list []config.Outbound) []config.Outbound {
	addr := ""
	if len(list) > 0 {
		addr, _ = outboundAddr(list[0])
	}
	server, known := CachedFronts(addr)
	var out []config.Outbound
	for _, o := range list {
		if !cfg.Allows(o.Type) {
			continue
		}
		if known {
			ok := false
			for _, f := range server {
				if f == o.Type {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		out = append(out, o)
	}
	if len(out) == 0 {
		return nil
	}
	n := int(rotateN.Load()) % len(out)
	if n == 0 {
		return out
	}
	return append(append([]config.Outbound{}, out[n:]...), out[:n]...)
}

// SessionLine is the post-handshake debug line: the next hello's sid,
// whether mask_pub is known, the group of the handshake just finished,
// the decoy name, and the fronts Status listed.
func SessionLine(rawURL string) string {
	o := config.Outbound{URL: rawURL, Type: "get"}
	addr, err := outboundAddr(o)
	next := "v2"
	mask := "off"
	decoy := "-"
	pbk := "-"
	if err == nil {
		if pub := CachedMaskPub(addr); len(pub) == 32 {
			next = "v3"
			mask = "on"
			pbk = hex.EncodeToString(pub)
		}
		if origins, ok := CachedDecoy(addr); ok && len(origins) > 0 {
			decoy = decoyHost(origins[0])
		}
	}
	kex := LastKEX()
	if kex == "" {
		kex = "-"
	}
	fronts := FormatFronts(LearnedFronts())
	if fronts == "" {
		fronts = "-"
	}
	return fmt.Sprintf("next_sid=%s mask=%s kex=%s decoy=%s pbk=%s fronts=%s", next, mask, kex, decoy, pbk, fronts)
}

// helloCaps is the capability bits the client sends in Hello: its enabled
// fronts, ExtraCaps from the config, and P2P unless turned off.
// The base is MemberCaps, which leaves out CapRelay (the bit that marks a
// relay trunk to the controller). A relay loft-node sets ExtraCaps to put the
// bit back; stock loft-front does not, so a member cannot Open the service
// addresses hub:* / ctl:1. CapHold (the node may park the mux over a short
// drop) is still in MemberCaps, so Wi-Fi / LTE / GET gaps behave as before.
func helloCaps(cfg *config.ClientConfig) wire.Cap {
	fronts, err := wire.ParseFronts(cfg.EnabledFronts())
	if err != nil {
		fronts = cfg.EnabledFronts()
	}
	c := wire.CapsWithFronts(wire.MemberCaps, fronts)
	c |= wire.Cap(cfg.ExtraCaps)
	if cfg.P2P == nil || *cfg.P2P {
		c |= wire.CapP2P
	}
	return c
}

// ServerCaps is the capability bits the node puts in Status, derived from
// the server config: the enabled fronts, plus decoy, mask, roster and lists
// only when they are configured. The base is NodeCaps (includes CapRelay),
// so Status still says the node accepts relay trunks. That bit is not
// copied into member Hello; see helloCaps.
func ServerCaps(cfg *config.ServerConfig, maskOn bool) wire.Cap {
	fronts, err := wire.ParseFronts(cfg.EnabledFronts())
	if err != nil {
		fronts = cfg.EnabledFronts()
	}
	c := wire.CapsWithFronts(wire.NodeCaps, fronts)
	if len(cfg.Decoy) == 0 {
		c &^= wire.CapDecoy
	}
	if !maskOn {
		c &^= wire.CapMask
	}
	if len(cfg.Decoy) > 0 {
		// With a decoy, UDP on this IP belongs to the decoy site unless the
		// QUIC Initial carries the loft mark (quicmark.go). So the node
		// advertises only the marked variants. Old clients do not mark:
		// they see no quic/h3 and do not try it.
		if c.Has(wire.CapQUIC) {
			c |= wire.CapQUICMark
		}
		if c.Has(wire.CapH3) {
			c |= wire.CapH3Mark
		}
		c &^= wire.CapQUIC | wire.CapH3
	}
	if len(cfg.Relays) == 0 && cfg.Advertise == "" {
		c &^= wire.CapRoster
	}
	if len(cfg.Lists) == 0 {
		c &^= wire.CapLists | wire.CapListsInline
	}
	return c
}
