package wire

// Hello and Status handshake payloads. Neither is a mux command.
//
// Hello rides inside the client's 0x02 message. Status is the node's first
// encrypted answer. Both have a fixed head and a long optional tail; each
// tail field may only be written if every field before it is written too,
// so an old peer that stops reading early never misreads a newer field.

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
	Software   string // client release, e.g. 1.0.22 — not the protocol
	InnerSet   bool   // one trailing byte is present; false means the client did not ask
	Inner      byte   // 0 clear, 1 ChaCha20-Poly1305, 2 AES-256-GCM. Meaningful when InnerSet
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
//
// Some terms used in the field notes:
//   - lease: the overlay IP the node gives this member, kept LeaseSec
//     after a disconnect so a reconnect gets the same address;
//   - ticket: the session id (sid) other fronts present to attach;
//   - decoy / mask: the real website behind the node, and a mode in which
//     the node shows a certificate shaped like that site's;
//   - canary: TLS path checks (padded or split ClientHello) that make a
//     handshake rewritten by a middlebox fail instead of working silently;
//   - roster: the controller-signed list of live fronts and relays;
//   - lists: signed domain/subnet sets the client uses for routing.
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
	Ticket       []byte   // session sid; other fronts present it in 0x04 to attach
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
	DNS          []string  // resolver IPs for the client TUN; empty = do not change OS DNS
	IP6          string    // overlay IPv6; empty = IPv4-only (optional tail)
	Gateway6     string    // overlay IPv6 gateway; empty = none
	HeaderBudget uint16    // GET encoded Cookie budget; 0 = client keeps its own
	Inflight     uint8     // GET inflight; 0 = client keeps its own
	H2Conns      uint8     // GET HTTP/2 connections (1–4); 0 = client keeps one
	UplinkEnc    uint8     // 0 base64url, 1 Cookie Z85; 0 = client keeps base64url
	GapHoldMS    uint32    // mux/stream hold after a front drop; 0 = client default
	Routes       []Route   // managed CIDRs (lab LAN behind a member); empty = none
	Policy       Policy    // compact ACL for P2P; omitted when empty
	SessionTTLMS uint32    // live session max; 0 = off. Distinct from LeaseSec (IP hold).
	RateBPS      uint32    // bulk Data/Dgram cap, both ways; 0 = off
	Fronts       []FrontEP // mux fronts this node accepts; empty = client keeps URI/defaults
	Edge         byte      // 0 plain, 1 tight; 0 = client keeps its opening GET limits
	InnerSet     bool      // inner byte is present. Absent means ChaCha20-Poly1305
	Inner        byte      // 0 clear, 1 ChaCha20-Poly1305, 2 AES-256-GCM
	KeyRoll      []byte    // encoded KeyRoll while the controller key changes; empty otherwise
}

// FrontEP is one Status front: name (get/ws/h2/h3/quic), HTTP path, optional URL.
// Empty URL is the host:port of the front that did 0x02. quic has an empty path.
type FrontEP struct {
	Name string
	Path string
	URL  string
}

// Policy is compact ACL pushed so a client can enforce the same Allow on
// direct P2P. Present is true when the node sent this tail. Old Status
// blobs omit it; the client then skips P2P ACL (hub splice still checks).
type Policy struct {
	Present    bool
	OwnTags    []string
	ACLDefault byte // 0 peers/accept, 1 drop
	Rules      []PolicyRule
	Peers      []PeerTags
}

// PolicyRule is one compact ACL line. Zero VLAN pointers and empty
// strings are wildcards. Proto: 0 any, 1 tcp, 2 udp, 3 icmp.
type PolicyRule struct {
	Action  byte // 0 drop, 1 accept
	SrcVLAN *uint16
	DstVLAN *uint16
	Src     string
	Dst     string
	SrcTag  string
	DstTag  string
	Proto   byte
	Dport   []uint16
	Sport   []uint16
}

// PeerTags is overlay IP → member tags for P2P Allow.
type PeerTags struct {
	IP   string
	Tags []string
}

// Route is one Status managed CIDR. Via is the overlay IP of the member
// that owns the prefix, or empty/gateway when the node itself is next hop.
type Route struct {
	Dst string
	Via string
}

// PeerHint is one Status p2p hint: overlay IP, TLS-observed host, listen
// port, VLAN, and the peer's 32-byte X25519 pub for LFT. LFT is the short
// key proof two members exchange when they connect to each other directly
// (peer-to-peer) instead of through the node.
type PeerHint struct {
	IP   string
	Host string
	Port uint16
	VLAN uint16
	Pub  []byte // 32-byte X25519; empty if the peer did not announce
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
	b = append(b, putStr(h.Software)...)
	if h.InnerSet {
		b = append(b, h.Inner)
	}
	return b
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
			var m int
			h.Program, n = getStr(extra)
			h.Software, m = getStr(extra[n:])
			if end := n + m; end < len(extra) {
				h.Inner = extra[end]
				h.InnerSet = true
			}
		}
	}
	return h, nil
}

// Encode writes Status fields in protocol order. IPv6 is omitted when both addresses are empty.
//
// The nested ifs below follow the tail rule: to send a later field (for
// example Inner), every earlier optional field is written too, as zero or
// empty if it is not set. The need* flags compute how deep the tail goes.
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
		if len(p.Pub) == 32 {
			tail = append(tail, 32)
			tail = append(tail, p.Pub...)
		}
	}
	nd := len(s.DNS)
	if nd > 8 {
		nd = 8
	}
	tail = append(tail, byte(nd))
	for i := 0; i < nd; i++ {
		tail = append(tail, putStr(s.DNS[i])...)
	}
	needRoutes := len(s.Routes) > 0
	needPolicy := s.Policy.Present || len(s.Policy.OwnTags) > 0 || s.Policy.ACLDefault > 0 || len(s.Policy.Rules) > 0 || len(s.Policy.Peers) > 0
	needTTL := s.SessionTTLMS > 0
	needRoll := len(s.KeyRoll) > 0
	needInner := s.InnerSet || needRoll
	needFronts := len(s.Fronts) > 0 || s.Edge > 0 || needInner
	needRate := s.RateBPS > 0 || needFronts
	needAfterGap := needRoutes || needPolicy || needTTL || needRate
	if s.IP6 != "" || s.Gateway6 != "" || s.HeaderBudget > 0 || s.Inflight > 0 || s.H2Conns > 0 || s.UplinkEnc > 0 || s.GapHoldMS > 0 || needAfterGap {
		tail = append(tail, putStr(s.IP6)...)
		tail = append(tail, putStr(s.Gateway6)...)
		if s.HeaderBudget > 0 || s.Inflight > 0 || s.H2Conns > 0 || s.UplinkEnc > 0 || s.GapHoldMS > 0 || needAfterGap {
			var hb [3]byte
			binary.BigEndian.PutUint16(hb[0:2], s.HeaderBudget)
			hb[2] = s.Inflight
			tail = append(tail, hb[:]...)
			if s.H2Conns > 0 || s.UplinkEnc > 0 || s.GapHoldMS > 0 || needAfterGap {
				tail = append(tail, s.H2Conns, s.UplinkEnc)
				if s.GapHoldMS > 0 || needAfterGap {
					var gh [4]byte
					binary.BigEndian.PutUint32(gh[:], s.GapHoldMS)
					tail = append(tail, gh[:]...)
					if needAfterGap {
						n := len(s.Routes)
						if n > 16 {
							n = 16
						}
						tail = append(tail, byte(n))
						for i := 0; i < n; i++ {
							tail = append(tail, putStr(s.Routes[i].Dst)...)
							tail = append(tail, putStr(s.Routes[i].Via)...)
						}
						if needPolicy || needTTL || needRate {
							tail = append(tail, encodePolicy(s.Policy)...)
							if needTTL || needRate {
								var tt [4]byte
								binary.BigEndian.PutUint32(tt[:], s.SessionTTLMS)
								tail = append(tail, tt[:]...)
								if needRate {
									var rb [4]byte
									binary.BigEndian.PutUint32(rb[:], s.RateBPS)
									tail = append(tail, rb[:]...)
									if needFronts {
										tail = append(tail, encodeFronts(s.Fronts)...)
										if s.Edge > 0 || needInner {
											tail = append(tail, s.Edge)
											if needInner {
												// Without a negotiated cipher the byte is
												// ChaCha20-Poly1305, which needs no switch.
												in := s.Inner
												if !s.InnerSet {
													in = InnerChaCha
												}
												tail = append(tail, in)
												if needRoll {
													var rl [2]byte
													binary.BigEndian.PutUint16(rl[:], uint16(len(s.KeyRoll)))
													tail = append(tail, rl[:]...)
													tail = append(tail, s.KeyRoll...)
												}
											}
										}
									}
								}
							}
						}
					}
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
					var pub []byte
					// Optional u8 32 || pub. Other values are the next field
					// (older blobs, or a peer that has not announced a pub).
					if off < len(b) {
						n := int(b[off])
						if n == 32 && off+33 <= len(b) {
							pub = append([]byte(nil), b[off+1:off+33]...)
							off += 33
						}
					}
					// Count is a u8, clipped above. codeql[go/allocation-size-overflow]
					s.PeerHints = append(s.PeerHints, PeerHint{IP: ip, Host: host, Port: port, VLAN: vlan, Pub: pub})
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
						off += 4
						if off < len(b) {
							n := int(b[off])
							off++
							if n > 16 {
								n = 16
							}
							for i := 0; i < n && off < len(b); i++ {
								dst, k := getStr(b[off:])
								off += k
								via, k := getStr(b[off:])
								off += k
								if dst != "" {
									s.Routes = append(s.Routes, Route{Dst: dst, Via: via})
								}
							}
							if off < len(b) {
								pol, n := decodePolicy(b[off:])
								if n > 0 {
									s.Policy = pol
									off += n
								}
								if off+4 <= len(b) {
									s.SessionTTLMS = binary.BigEndian.Uint32(b[off : off+4])
									off += 4
									if off+4 <= len(b) {
										s.RateBPS = binary.BigEndian.Uint32(b[off : off+4])
										off += 4
										if off < len(b) {
											eps, n := decodeFronts(b[off:])
											s.Fronts = eps
											off += n
											if off < len(b) {
												s.Edge = b[off]
												off++
												if off < len(b) {
													s.Inner = b[off]
													s.InnerSet = true
													off++
													if off+2 <= len(b) {
														n := int(binary.BigEndian.Uint16(b[off : off+2]))
														off += 2
														if off+n <= len(b) {
															s.KeyRoll = append([]byte(nil), b[off:off+n]...)
															off += n
														}
													}
												}
											}
										}
									}
								}
							}
						}
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

// encodeFronts is u8 n ≤ 8, then n × (name, path, url) as putStr. Empty URL is the 0x02 host.
func encodeFronts(eps []FrontEP) []byte {
	n := len(eps)
	if n > 8 {
		n = 8
	}
	b := []byte{byte(n)}
	for i := 0; i < n; i++ {
		b = append(b, putStr(eps[i].Name)...)
		b = append(b, putStr(eps[i].Path)...)
		b = append(b, putStr(eps[i].URL)...)
	}
	return b
}

// decodeFronts reads the Status fronts tail. Truncation returns what was parsed.
func decodeFronts(b []byte) ([]FrontEP, int) {
	if len(b) < 1 {
		return nil, 0
	}
	n := int(b[0])
	off := 1
	if n > 8 {
		n = 8
	}
	var out []FrontEP
	for i := 0; i < n && off < len(b); i++ {
		name, k := getStr(b[off:])
		off += k
		path, k := getStr(b[off:])
		off += k
		u, k := getStr(b[off:])
		off += k
		if name != "" {
			out = append(out, FrontEP{Name: name, Path: path, URL: u})
		}
	}
	return out, off
}

// encodePolicy is own tags, acl_default, rules, peer tags. Always at least 4 bytes.
func encodePolicy(p Policy) []byte {
	n := len(p.OwnTags)
	if n > 16 {
		n = 16
	}
	b := []byte{byte(n)}
	for i := 0; i < n; i++ {
		b = append(b, putStr(p.OwnTags[i])...)
	}
	b = append(b, p.ACLDefault)
	nr := len(p.Rules)
	if nr > 32 {
		nr = 32
	}
	b = append(b, byte(nr))
	for i := 0; i < nr; i++ {
		b = append(b, encodePolicyRule(p.Rules[i])...)
	}
	np := len(p.Peers)
	if np > 32 {
		np = 32
	}
	b = append(b, byte(np))
	for i := 0; i < np; i++ {
		pt := p.Peers[i]
		b = append(b, putStr(pt.IP)...)
		nt := len(pt.Tags)
		if nt > 8 {
			nt = 8
		}
		b = append(b, byte(nt))
		for j := 0; j < nt; j++ {
			b = append(b, putStr(pt.Tags[j])...)
		}
	}
	return b
}

// encodePolicyRule is action || flags || only the fields whose flag bit is
// set. Flags: 1 src VLAN, 2 dst VLAN, 4 src, 8 dst, 16 src tag, 32 dst tag,
// 64 proto, 128 port lists. Unset fields cost no bytes.
func encodePolicyRule(r PolicyRule) []byte {
	var flags byte
	if r.SrcVLAN != nil {
		flags |= 1
	}
	if r.DstVLAN != nil {
		flags |= 2
	}
	if r.Src != "" {
		flags |= 4
	}
	if r.Dst != "" {
		flags |= 8
	}
	if r.SrcTag != "" {
		flags |= 16
	}
	if r.DstTag != "" {
		flags |= 32
	}
	if r.Proto != 0 {
		flags |= 64
	}
	if len(r.Dport) > 0 || len(r.Sport) > 0 {
		flags |= 128
	}
	b := []byte{r.Action, flags}
	put16 := func(v uint16) {
		var x [2]byte
		binary.BigEndian.PutUint16(x[:], v)
		b = append(b, x[:]...)
	}
	if r.SrcVLAN != nil {
		put16(*r.SrcVLAN)
	}
	if r.DstVLAN != nil {
		put16(*r.DstVLAN)
	}
	if r.Src != "" {
		b = append(b, putStr(r.Src)...)
	}
	if r.Dst != "" {
		b = append(b, putStr(r.Dst)...)
	}
	if r.SrcTag != "" {
		b = append(b, putStr(r.SrcTag)...)
	}
	if r.DstTag != "" {
		b = append(b, putStr(r.DstTag)...)
	}
	if r.Proto != 0 {
		b = append(b, r.Proto)
	}
	if flags&128 != 0 {
		nd := len(r.Dport)
		if nd > 8 {
			nd = 8
		}
		b = append(b, byte(nd))
		for i := 0; i < nd; i++ {
			put16(r.Dport[i])
		}
		ns := len(r.Sport)
		if ns > 8 {
			ns = 8
		}
		b = append(b, byte(ns))
		for i := 0; i < ns; i++ {
			put16(r.Sport[i])
		}
	}
	return b
}

// decodePolicy reads a Policy tail. Truncation returns what was parsed and consumed.
func decodePolicy(b []byte) (Policy, int) {
	if len(b) < 4 {
		return Policy{}, 0
	}
	off := 0
	n := int(b[off])
	off++
	if n > 16 {
		n = 16
	}
	var p Policy
	p.Present = true
	for i := 0; i < n && off < len(b); i++ {
		s, k := getStr(b[off:])
		off += k
		if s != "" {
			p.OwnTags = append(p.OwnTags, s)
		}
	}
	if off >= len(b) {
		return p, off
	}
	p.ACLDefault = b[off]
	off++
	if off >= len(b) {
		return p, off
	}
	nr := int(b[off])
	off++
	if nr > 32 {
		nr = 32
	}
	for i := 0; i < nr && off < len(b); i++ {
		r, n := decodePolicyRule(b[off:])
		if n == 0 {
			break
		}
		off += n
		p.Rules = append(p.Rules, r)
	}
	if off >= len(b) {
		return p, off
	}
	np := int(b[off])
	off++
	if np > 32 {
		np = 32
	}
	for i := 0; i < np && off < len(b); i++ {
		ip, k := getStr(b[off:])
		off += k
		if off >= len(b) {
			break
		}
		nt := int(b[off])
		off++
		if nt > 8 {
			nt = 8
		}
		var tags []string
		for j := 0; j < nt && off < len(b); j++ {
			t, k := getStr(b[off:])
			off += k
			if t != "" {
				tags = append(tags, t)
			}
		}
		if ip != "" {
			p.Peers = append(p.Peers, PeerTags{IP: ip, Tags: tags})
		}
	}
	return p, off
}

// decodePolicyRule reads one encodePolicyRule. It returns the bytes
// consumed, or 0 when even the action and flags are missing.
func decodePolicyRule(b []byte) (PolicyRule, int) {
	if len(b) < 2 {
		return PolicyRule{}, 0
	}
	r := PolicyRule{Action: b[0]}
	flags := b[1]
	off := 2
	take16 := func() (uint16, bool) {
		if off+2 > len(b) {
			return 0, false
		}
		v := binary.BigEndian.Uint16(b[off : off+2])
		off += 2
		return v, true
	}
	if flags&1 != 0 {
		v, ok := take16()
		if !ok {
			return r, off
		}
		r.SrcVLAN = &v
	}
	if flags&2 != 0 {
		v, ok := take16()
		if !ok {
			return r, off
		}
		r.DstVLAN = &v
	}
	if flags&4 != 0 {
		s, k := getStr(b[off:])
		off += k
		r.Src = s
	}
	if flags&8 != 0 {
		s, k := getStr(b[off:])
		off += k
		r.Dst = s
	}
	if flags&16 != 0 {
		s, k := getStr(b[off:])
		off += k
		r.SrcTag = s
	}
	if flags&32 != 0 {
		s, k := getStr(b[off:])
		off += k
		r.DstTag = s
	}
	if flags&64 != 0 {
		if off >= len(b) {
			return r, off
		}
		r.Proto = b[off]
		off++
	}
	if flags&128 != 0 {
		if off >= len(b) {
			return r, off
		}
		nd := int(b[off])
		off++
		if nd > 8 {
			nd = 8
		}
		for i := 0; i < nd; i++ {
			v, ok := take16()
			if !ok {
				return r, off
			}
			r.Dport = append(r.Dport, v)
		}
		if off >= len(b) {
			return r, off
		}
		ns := int(b[off])
		off++
		if ns > 8 {
			ns = 8
		}
		for i := 0; i < ns; i++ {
			v, ok := take16()
			if !ok {
				return r, off
			}
			r.Sport = append(r.Sport, v)
		}
	}
	return r, off
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
