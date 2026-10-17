package wire

// Hello and Status handshake payloads. Neither is a mux command.

import (
	"encoding/binary"
	"errors"
)

// Hello is the client's first inner payload after the key proof.
// Trailing proto/caps are optional: an old blob still decodes.
type Hello struct {
	Version    byte
	MemberID   string
	InstanceID string
	Min        uint16
	Max        uint16
	Caps       Cap
	Program    string // client binary name, e.g. loft
	Software   string // client release, e.g. 1.0.10 — not the protocol
}

// Offer is this Hello's version range and caps. Max 0 is treated as 1.0.
func (h Hello) Offer() Offer {
	o := Offer{Min: h.Min, Max: h.Max, Caps: h.Caps}
	if o.Max == 0 {
		o.Min, o.Max = ProtoMin, ProtoMin
	}
	return o
}

// Status is the node's reply: code, lease, timers, protocol offer.
type Status struct {
	Code         Code
	IP           string
	Gateway      string
	InstanceID   string
	MTU          uint16
	VLAN         uint16
	LeaseSec     uint32
	KeepaliveMS  uint32
	IdleMS       uint32
	ReMinMS      uint32
	ReMaxMS      uint32
	ReFastN      uint16
	ReFastForMS  uint32
	Proto        uint16   // selected protocol version
	Min          uint16   // node protocol min
	Max          uint16   // node protocol max
	Caps         Cap      // what this node implements
	Program      string   // node binary name, e.g. loft-node
	Software     string   // node release — not the protocol
	Ticket       []byte   // resume ticket for replicas / metro; empty if none
	Decoy        []string // this node's splice origins; first is identity (SNI); empty = off
	MaskPub      []byte   // 32-byte X25519 public for decoy mask; empty = mask off
	Replicas     []string // extra node host:port for failover; empty = this addr only
	CanaryFlags  byte     // bit0 fat hello, bit1 split ClientHello
	CanaryPad    uint16   // extra ClientHello padding
	Roster       []byte   // signed controller roster; empty = none
	ListsSerial  uint64   // 0 = no address lists
	ListsHash    []byte   // SHA-256 of signed lists blob; 32 bytes
	Peers        byte     // 0 hub, 1 mesh, 2 vlan
	P2P          byte     // 1 = node allows p2p
	PeerHints    []PeerHint
	DNS          []string // resolver IPs for the client TUN; empty = do not change OS DNS
	IP6          string   // overlay IPv6; empty = IPv4-only (optional tail)
	Gateway6     string   // overlay IPv6 gateway; empty = none
	HeaderBudget uint16   // GET encoded Cookie budget; 0 = client keeps its own
	Inflight     uint8    // GET inflight; 0 = client keeps its own
	H2Conns      uint8    // GET HTTP/2 connections (1–4); 0 = client keeps one
	UplinkEnc    uint8    // 0 base64url, 1 Cookie Z85; 0 = client keeps base64url
	GapHoldMS    uint32   // mux/stream hold after a front drop; 0 = client default
}

// PeerHint is one Status p2p hint: overlay IP, TLS-observed host, listen port, VLAN.
type PeerHint struct {
	IP   string
	Host string
	Port uint16
	VLAN uint16
}

// Encode is version, member_id, instance_id, then optional proto/caps/program/software.
// Strings are u8 length, at most 64 bytes. Max 0 fills ProtoMin–Proto and NodeCaps.
func (h Hello) Encode() []byte {
	if h.Version == 0 {
		h.Version = 1
	}
	if h.Max == 0 {
		h.Min, h.Max = ProtoMin, Proto
		h.Caps = NodeCaps
	}
	m, i := []byte(h.MemberID), []byte(h.InstanceID)
	if len(m) > idMax {
		m = m[:idMax]
	}
	if len(i) > idMax {
		i = i[:idMax]
	}
	b := make([]byte, 3+idMax+idMax+12)
	b[0] = h.Version
	b[1] = byte(len(m))
	copy(b[2:], m)
	b[2+len(m)] = byte(len(i))
	copy(b[3+len(m):], i)
	off := 3 + len(m) + len(i)
	binary.BigEndian.PutUint16(b[off:], h.Min)
	binary.BigEndian.PutUint16(b[off+2:], h.Max)
	binary.BigEndian.PutUint64(b[off+4:], uint64(h.Caps))
	b = b[:off+12]
	b = append(b, putStr(h.Program)...)
	return append(b, putStr(h.Software)...)
}

// DecodeHello reads a Hello. Trailing proto/caps are optional; unknown bytes are ignored.
func DecodeHello(b []byte) (Hello, error) {
	if len(b) < 3 {
		return Hello{}, ErrShort
	}
	h := Hello{Version: b[0]}
	ml := int(b[1])
	if 2+ml+1 > len(b) {
		return Hello{}, ErrShort
	}
	h.MemberID = string(b[2 : 2+ml])
	il := int(b[2+ml])
	end := 3 + ml + il
	if end > len(b) {
		return Hello{}, ErrShort
	}
	h.InstanceID = string(b[end-il : end])
	if rest := b[end:]; len(rest) >= 12 {
		h.Min = binary.BigEndian.Uint16(rest[0:2])
		h.Max = binary.BigEndian.Uint16(rest[2:4])
		h.Caps = Cap(binary.BigEndian.Uint64(rest[4:12]))
		if extra := rest[12:]; len(extra) >= 2 {
			var n int
			h.Program, n = getStr(extra)
			h.Software, _ = getStr(extra[n:])
		}
	}
	return h, nil
}

// Encode writes Status fields in protocol order. IPv6 is omitted when both addresses are empty.
func (s Status) Encode() []byte {
	ip, gw, inst := []byte(s.IP), []byte(s.Gateway), []byte(s.InstanceID)
	b := make([]byte, 28+3+len(ip)+len(gw)+len(inst))
	binary.BigEndian.PutUint16(b[0:2], uint16(s.Code))
	binary.BigEndian.PutUint32(b[2:6], s.LeaseSec)
	binary.BigEndian.PutUint16(b[6:8], s.MTU)
	binary.BigEndian.PutUint16(b[8:10], s.VLAN)
	binary.BigEndian.PutUint32(b[10:14], s.KeepaliveMS)
	binary.BigEndian.PutUint32(b[14:18], s.IdleMS)
	binary.BigEndian.PutUint32(b[18:22], s.ReMinMS)
	binary.BigEndian.PutUint32(b[22:26], s.ReMaxMS)
	binary.BigEndian.PutUint16(b[26:28], s.ReFastN)
	off := 28
	b[off] = byte(len(ip))
	off++
	copy(b[off:], ip)
	off += len(ip)
	b[off] = byte(len(gw))
	off++
	copy(b[off:], gw)
	off += len(gw)
	b[off] = byte(len(inst))
	off++
	copy(b[off:], inst)
	off += len(inst)
	if s.Max == 0 {
		s.Min, s.Max, s.Proto, s.Caps = ProtoMin, Proto, Proto, NodeCaps
	}
	tail := make([]byte, 4+14)
	binary.BigEndian.PutUint32(tail[0:4], s.ReFastForMS)
	binary.BigEndian.PutUint16(tail[4:6], s.Proto)
	binary.BigEndian.PutUint16(tail[6:8], s.Min)
	binary.BigEndian.PutUint16(tail[8:10], s.Max)
	binary.BigEndian.PutUint64(tail[10:18], uint64(s.Caps))
	tail = append(tail, putStr(s.Program)...)
	tail = append(tail, putStr(s.Software)...)
	var tl [2]byte
	binary.BigEndian.PutUint16(tl[:], uint16(len(s.Ticket)))
	tail = append(tail, tl[:]...)
	tail = append(tail, s.Ticket...)
	n := len(s.Decoy)
	if n > 255 {
		n = 255
	}
	tail = append(tail, byte(n))
	for i := 0; i < n; i++ {
		tail = append(tail, putStr(s.Decoy[i])...)
	}
	ml := len(s.MaskPub)
	if ml > 255 {
		ml = 255
	}
	tail = append(tail, byte(ml))
	tail = append(tail, s.MaskPub[:ml]...)
	rn := len(s.Replicas)
	if rn > 255 {
		rn = 255
	}
	tail = append(tail, byte(rn))
	for i := 0; i < rn; i++ {
		tail = append(tail, putStr(s.Replicas[i])...)
	}
	tail = append(tail, s.CanaryFlags)
	var pad [2]byte
	binary.BigEndian.PutUint16(pad[:], s.CanaryPad)
	tail = append(tail, pad[:]...)
	var rl [2]byte
	rlen := len(s.Roster)
	if rlen > 0xffff {
		rlen = 0xffff
	}
	binary.BigEndian.PutUint16(rl[:], uint16(rlen))
	tail = append(tail, rl[:]...)
	if rlen > 0 {
		tail = append(tail, s.Roster[:rlen]...)
	}
	var ls [8]byte
	binary.BigEndian.PutUint64(ls[:], s.ListsSerial)
	tail = append(tail, ls[:]...)
	h := s.ListsHash
	if len(h) > 32 {
		h = h[:32]
	}
	tail = append(tail, byte(len(h)))
	tail = append(tail, h...)
	tail = append(tail, s.Peers, s.P2P)
	nh := len(s.PeerHints)
	if nh > 16 {
		nh = 16
	}
	tail = append(tail, byte(nh))
	for i := 0; i < nh; i++ {
		p := s.PeerHints[i]
		tail = append(tail, putStr(p.IP)...)
		tail = append(tail, putStr(p.Host)...)
		var pp [4]byte
		binary.BigEndian.PutUint16(pp[0:2], p.Port)
		binary.BigEndian.PutUint16(pp[2:4], p.VLAN)
		tail = append(tail, pp[:]...)
	}
	nd := len(s.DNS)
	if nd > 8 {
		nd = 8
	}
	tail = append(tail, byte(nd))
	for i := 0; i < nd; i++ {
		tail = append(tail, putStr(s.DNS[i])...)
	}
	if s.IP6 != "" || s.Gateway6 != "" || s.HeaderBudget > 0 || s.Inflight > 0 || s.H2Conns > 0 || s.UplinkEnc > 0 || s.GapHoldMS > 0 {
		tail = append(tail, putStr(s.IP6)...)
		tail = append(tail, putStr(s.Gateway6)...)
		if s.HeaderBudget > 0 || s.Inflight > 0 || s.H2Conns > 0 || s.UplinkEnc > 0 || s.GapHoldMS > 0 {
			var hb [3]byte
			binary.BigEndian.PutUint16(hb[0:2], s.HeaderBudget)
			hb[2] = s.Inflight
			tail = append(tail, hb[:]...)
			if s.H2Conns > 0 || s.UplinkEnc > 0 || s.GapHoldMS > 0 {
				tail = append(tail, s.H2Conns, s.UplinkEnc)
				if s.GapHoldMS > 0 {
					var gh [4]byte
					binary.BigEndian.PutUint32(gh[:], s.GapHoldMS)
					tail = append(tail, gh[:]...)
				}
			}
		}
	}
	return append(b[:off], tail...)
}

// DecodeStatus reads a Status. Trailing unknown fields MUST be ignored; old blobs still decode.
func DecodeStatus(b []byte) (Status, error) {
	if len(b) < 28+3+4 {
		return Status{}, ErrShort
	}
	var s Status
	s.Code = Code(binary.BigEndian.Uint16(b[0:2]))
	s.LeaseSec = binary.BigEndian.Uint32(b[2:6])
	s.MTU = binary.BigEndian.Uint16(b[6:8])
	s.VLAN = binary.BigEndian.Uint16(b[8:10])
	s.KeepaliveMS = binary.BigEndian.Uint32(b[10:14])
	s.IdleMS = binary.BigEndian.Uint32(b[14:18])
	s.ReMinMS = binary.BigEndian.Uint32(b[18:22])
	s.ReMaxMS = binary.BigEndian.Uint32(b[22:26])
	s.ReFastN = binary.BigEndian.Uint16(b[26:28])
	off := 28
	take := func() (string, error) {
		if off >= len(b) {
			return "", ErrShort
		}
		n := int(b[off])
		off++
		if off+n > len(b) {
			return "", errors.New("wire: short string")
		}
		s := string(b[off : off+n])
		off += n
		return s, nil
	}
	var err error
	if s.IP, err = take(); err != nil {
		return s, err
	}
	if s.Gateway, err = take(); err != nil {
		return s, err
	}
	if s.InstanceID, err = take(); err != nil {
		return s, err
	}
	if off+4 > len(b) {
		return s, ErrShort
	}
	s.ReFastForMS = binary.BigEndian.Uint32(b[off : off+4])
	off += 4
	if off+14 <= len(b) {
		s.Proto = binary.BigEndian.Uint16(b[off : off+2])
		s.Min = binary.BigEndian.Uint16(b[off+2 : off+4])
		s.Max = binary.BigEndian.Uint16(b[off+4 : off+6])
		s.Caps = Cap(binary.BigEndian.Uint64(b[off+6 : off+14]))
		off += 14
		if off < len(b) {
			var n int
			s.Program, n = getStr(b[off:])
			off += n
			s.Software, n = getStr(b[off:])
			off += n
			if off+2 <= len(b) {
				ln := int(binary.BigEndian.Uint16(b[off : off+2]))
				off += 2
				if ln > 0 {
					if off+ln > len(b) {
						return s, nil
					}
					s.Ticket = append([]byte(nil), b[off:off+ln]...)
					off += ln
				}
			}
			if off < len(b) {
				n := int(b[off])
				off++
				for i := 0; i < n; i++ {
					d, k := getStr(b[off:])
					if k == 0 {
						break
					}
					off += k
					if d != "" {
						s.Decoy = append(s.Decoy, d)
					}
				}
			}
			if off < len(b) {
				ml := int(b[off])
				off++
				if ml > 0 {
					if off+ml > len(b) {
						return s, nil
					}
					s.MaskPub = append([]byte(nil), b[off:off+ml]...)
					off += ml
				}
			}
			if off < len(b) {
				n := int(b[off])
				off++
				for i := 0; i < n; i++ {
					d, k := getStr(b[off:])
					if k == 0 {
						break
					}
					off += k
					if d != "" {
						s.Replicas = append(s.Replicas, d)
					}
				}
			}
			if off < len(b) {
				s.CanaryFlags = b[off]
				off++
			}
			if off+2 <= len(b) {
				s.CanaryPad = binary.BigEndian.Uint16(b[off : off+2])
				off += 2
			}
			if off+2 <= len(b) {
				ln := int(binary.BigEndian.Uint16(b[off : off+2]))
				off += 2
				if ln > 0 && off+ln <= len(b) {
					s.Roster = append([]byte(nil), b[off:off+ln]...)
					off += ln
				}
			}
			if off+8 <= len(b) {
				s.ListsSerial = binary.BigEndian.Uint64(b[off : off+8])
				off += 8
			}
			if off < len(b) {
				hl := int(b[off])
				off++
				if hl > 0 && off+hl <= len(b) {
					s.ListsHash = append([]byte(nil), b[off:off+hl]...)
					off += hl
				}
			}
			if off+2 <= len(b) {
				s.Peers = b[off]
				s.P2P = b[off+1]
				off += 2
			}
			if off < len(b) {
				nh := int(b[off])
				off++
				if nh > 64 {
					nh = 64
				}
				for i := 0; i < nh && off < len(b); i++ {
					ip, k := getStr(b[off:])
					off += k
					host, k := getStr(b[off:])
					off += k
					if off+4 > len(b) {
						break
					}
					port := binary.BigEndian.Uint16(b[off : off+2])
					vlan := binary.BigEndian.Uint16(b[off+2 : off+4])
					off += 4
					// Count is a u8, clipped above. codeql[go/allocation-size-overflow]
					s.PeerHints = append(s.PeerHints, PeerHint{IP: ip, Host: host, Port: port, VLAN: vlan})
				}
			}
			if off < len(b) {
				nd := int(b[off])
				off++
				for i := 0; i < nd && off < len(b); i++ {
					d, k := getStr(b[off:])
					off += k
					if d != "" {
						s.DNS = append(s.DNS, d)
					}
				}
			}
			if off < len(b) {
				var n int
				s.IP6, n = getStr(b[off:])
				off += n
				if off < len(b) {
					s.Gateway6, n = getStr(b[off:])
					off += n
				}
				if off+3 <= len(b) {
					s.HeaderBudget = binary.BigEndian.Uint16(b[off : off+2])
					s.Inflight = b[off+2]
					off += 3
					if off+2 <= len(b) {
						s.H2Conns = b[off]
						s.UplinkEnc = b[off+1]
						off += 2
					} else if off < len(b) {
						s.H2Conns = b[off]
						off++
					}
					if off+4 <= len(b) {
						s.GapHoldMS = binary.BigEndian.Uint32(b[off : off+4])
					}
				}
			}
		}
	}
	return s, nil
}

// idMax is the protocol cap on u8-length strings (Hello, Status, most fleet records).
const idMax = 64

// putStr is u8 length || bytes, truncated to idMax.
func putStr(s string) []byte {
	b := []byte(s)
	if len(b) > idMax {
		b = b[:idMax]
	}
	out := make([]byte, 1+len(b))
	out[0] = byte(len(b))
	copy(out[1:], b)
	return out
}

// getStr reads u8 length || bytes. A truncated length returns consumed=1 and empty string.
func getStr(b []byte) (string, int) {
	if len(b) < 1 {
		return "", 0
	}
	n := int(b[0])
	if 1+n > len(b) {
		return "", 1
	}
	return string(b[1 : 1+n]), 1 + n
}
