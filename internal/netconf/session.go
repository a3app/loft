package netconf

// Session timers and reconnect. Network sets defaults; a member may override.

import (
	"time"

	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/internal/wire"
)

// SessionPolicy is timers and reconnect. Network sets defaults, member may override.
type SessionPolicy struct {
	HandshakeMS        int `yaml:"handshake_ms,omitempty" json:"handshake_ms,omitempty"`
	IdleMS             int `yaml:"idle_ms,omitempty" json:"idle_ms,omitempty"`
	KeepaliveMS        int `yaml:"keepalive_ms,omitempty" json:"keepalive_ms,omitempty"`
	LeaseHoldMS        int `yaml:"lease_hold_ms,omitempty" json:"lease_hold_ms,omitempty"`
	ReconnectMinMS     int `yaml:"reconnect_min_ms,omitempty" json:"reconnect_min_ms,omitempty"`
	ReconnectMaxMS     int `yaml:"reconnect_max_ms,omitempty" json:"reconnect_max_ms,omitempty"`
	ReconnectFastN     int `yaml:"reconnect_fast_n,omitempty" json:"reconnect_fast_n,omitempty"`
	ReconnectFastForMS int `yaml:"reconnect_fast_for_ms,omitempty" json:"reconnect_fast_for_ms,omitempty"`
	GapHoldMS          int `yaml:"gap_hold_ms,omitempty" json:"gap_hold_ms,omitempty"`
}

// Fallback overlay identity when the node has no Overlay (GETServer tests,
// loft-probe against a standalone GET origin). Internet ICMP needs a src IP.
const (
	FallbackClientIP = "10.8.0.2"
	FallbackGateway  = "10.8.0.1"
)

// DefaultSession is handshake 10s, idle 120s, keepalive 15s, lease hold 45s,
// reconnect 200ms–8s with 5 fast tries in 8s, gap hold 8s.
func DefaultSession() SessionPolicy {
	return SessionPolicy{
		HandshakeMS:        10_000,
		IdleMS:             120_000,
		KeepaliveMS:        15_000,
		LeaseHoldMS:        45_000,
		ReconnectMinMS:     200,
		ReconnectMaxMS:     8_000,
		ReconnectFastN:     5,
		ReconnectFastForMS: 8_000,
		GapHoldMS:          8_000,
	}
}

// Merge copies positive fields from over onto p. Zero HandshakeMS on p uses DefaultSession first.
func (p SessionPolicy) Merge(over SessionPolicy) SessionPolicy {
	base := p
	if base.HandshakeMS == 0 {
		base = DefaultSession()
	}
	if over.HandshakeMS > 0 {
		base.HandshakeMS = over.HandshakeMS
	}
	if over.IdleMS > 0 {
		base.IdleMS = over.IdleMS
	}
	if over.KeepaliveMS > 0 {
		base.KeepaliveMS = over.KeepaliveMS
	}
	if over.LeaseHoldMS > 0 {
		base.LeaseHoldMS = over.LeaseHoldMS
	}
	if over.ReconnectMinMS > 0 {
		base.ReconnectMinMS = over.ReconnectMinMS
	}
	if over.ReconnectMaxMS > 0 {
		base.ReconnectMaxMS = over.ReconnectMaxMS
	}
	if over.ReconnectFastN > 0 {
		base.ReconnectFastN = over.ReconnectFastN
	}
	if over.ReconnectFastForMS > 0 {
		base.ReconnectFastForMS = over.ReconnectFastForMS
	}
	if over.GapHoldMS > 0 {
		base.GapHoldMS = over.GapHoldMS
	}
	return base
}

// Hold is LeaseHoldMS as a duration; zero or negative is 45s.
func (p SessionPolicy) Hold() time.Duration {
	if p.LeaseHoldMS <= 0 {
		return 45 * time.Second
	}
	return time.Duration(p.LeaseHoldMS) * time.Millisecond
}

// Gap is GapHoldMS as a duration; zero or negative is 8s. Capped at 10 min.
func (p SessionPolicy) Gap() time.Duration {
	ms := p.GapHoldMS
	if ms <= 0 {
		ms = 8_000
	}
	if ms > 600_000 {
		ms = 600_000
	}
	return time.Duration(ms) * time.Millisecond
}

// Status builds the wire Status for this policy, overlay IP, and instance.
func (p SessionPolicy) Status(code wire.Code, ip, gw, instance string, mtu, vlan int) wire.Status {
	st := wire.Status{
		Code:        code,
		IP:          ip,
		Gateway:     gw,
		InstanceID:  instance,
		MTU:         uint16(mtu),
		VLAN:        uint16(vlan),
		LeaseSec:    uint32(p.Hold() / time.Second),
		KeepaliveMS: uint32(p.KeepaliveMS),
		IdleMS:      uint32(p.IdleMS),
		ReMinMS:     uint32(p.ReconnectMinMS),
		ReMaxMS:     uint32(p.ReconnectMaxMS),
		ReFastN:     uint16(p.ReconnectFastN),
		ReFastForMS: uint32(p.ReconnectFastForMS),
		GapHoldMS:   uint32(p.Gap() / time.Millisecond),
	}
	version.StampNode(&st)
	return st
}
