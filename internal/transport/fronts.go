// Package transport implements Loft 1.0 fronts, TLS camouflage, decoy splice,
// borrowed-cert mask, CONNECT splice, and client dialing.
package transport

import (
	"encoding/base64"
	"sync"
	"sync/atomic"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

// nodeView is the last Status fronts and mask_pub for one node address.
type nodeView struct {
	fronts  []string
	maskPub []byte
}

// views caches Status fronts and mask_pub; rotateN rotates filterOutbounds.
var views sync.Map // addr -> nodeView
var rotateN atomic.Uint64

// RememberFronts stores Status caps fronts for addr.
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

// ParseMaskPub decodes a 32-byte X25519 public from hex or base64url.
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

// filterOutbounds keeps cfg-allowed fronts that the node advertised, then rotates.
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

// helloCaps is Hello caps: enabled fronts, extra caps, and optional P2P.
// Base is MemberCaps (no CapRelay). loft-node relay sets ExtraCaps to put the
// bit back; stock loft-front does not, so a member cannot Open hub:* / ctl:1.
// CapHold is still in MemberCaps: Wi-Fi / LTE / GET gap is unchanged.
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

// ServerCaps is Status caps from server config, decoy, mask, roster, and lists.
// Base is NodeCaps (includes CapRelay): Status still says the node accepts trunks.
// That bit is not copied into member Hello; see helloCaps.
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
		// UDP on a decoy IP is dest's datagrams unless the Initial carries the
		// loft mark (quicmark.go). Old clients do not mark: they see no quic/h3.
		if c.Has(wire.CapQUIC) {
			c |= wire.CapQUICMark
		}
		if c.Has(wire.CapH3) {
			c |= wire.CapH3Mark
		}
		c &^= wire.CapQUIC | wire.CapH3
	}
	if len(cfg.Relays) == 0 && cfg.Advertise == "" && cfg.Enrol != "open" {
		c &^= wire.CapRoster
	}
	if len(cfg.Lists) == 0 {
		c &^= wire.CapLists
	}
	return c
}
