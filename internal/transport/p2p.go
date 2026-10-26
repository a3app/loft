package transport

// Mesh P2P: Status peer hints, loft.peer announce, LFT TCP preface, hub-splice fallback.
//
// Normally a connection from one member to another goes client → node →
// client ("hub splice"). In mesh or vlan mode with p2p allowed, members can
// also connect directly:
//  1. Each client listens on a TCP port and announces it to the node by
//     opening the pseudo-target loft.peer:<port> and writing its X25519
//     public key.
//  2. The node puts the live announcements into every Status as peer hints:
//     overlay IP, public address as the node saw it, port, VLAN, key.
//  3. To reach a member, a client first dials that member's hint directly
//     and sends a 42-byte "LFT" preface: its overlay IP plus an HMAC keyed
//     by X25519 between the two members' keys. The receiver checks the HMAC
//     and the ACL, then connects the TCP to its own local port.
//  4. If the direct dial fails, the stream goes through the node as usual.
//
// This file also keeps the client's view of the overlay from the last
// Status (the vis* variables), which the rest of the client reads.

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// p2pAllow and vis* are the client overlay view. LFT HMAC uses X25519
// against the peer pub from Status hints. vis* are written by RememberVis
// after each OK handshake and read under visMu.
var (
	p2pAllow   atomic.Bool
	visMu      sync.Mutex
	visMode    byte
	visP2P     byte
	visHint    []wire.PeerHint
	visIP      string
	visGW      string
	visIP6     string
	visVLAN    uint16
	visDNS     []string
	visRoutes  []string
	visPolicy  wire.Policy
	visTTLMS   uint32
	visFronts  []wire.FrontEP
	visMaskPub []byte

	p2pKeyMu sync.Mutex
	p2pKey   *ecdh.PrivateKey
)

// SetP2PClient enables direct TCP attempts (LFT). HMAC is pairwise ECDH
// with the peer pub from Status.
func SetP2PClient(on bool) {
	p2pAllow.Store(on)
	if on {
		_, _ = EnsureP2PKey()
	}
}

// EnsureP2PKey loads or mints a persistent X25519 seed next to client.instance.
// The key must survive restarts, because other members learn it through
// Status hints and use it to check this client's LFT preface.
func EnsureP2PKey() (*ecdh.PrivateKey, error) {
	p2pKeyMu.Lock()
	defer p2pKeyMu.Unlock()
	if p2pKey != nil {
		return p2pKey, nil
	}
	seed := make([]byte, 32)
	if dir := instanceDir(); dir != "" {
		path := filepath.Join(dir, "p2p.seed")
		if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
			copy(seed, b[:32])
		} else {
			if _, err := io.ReadFull(rand.Reader, seed); err != nil {
				return nil, err
			}
			_ = os.WriteFile(path, seed, 0o600)
		}
	} else if _, err := io.ReadFull(rand.Reader, seed); err != nil {
		return nil, err
	}
	k, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil {
		return nil, err
	}
	p2pKey = k
	return k, nil
}

// p2pPublic is this client's P2P X25519 public key.
func p2pPublic() []byte {
	k, err := EnsureP2PKey()
	if err != nil || k == nil {
		return nil
	}
	return k.PublicKey().Bytes()
}

// p2pShared is the X25519 shared secret with a peer; both members compute
// the same value, so it can key the LFT HMAC in either direction.
func p2pShared(peerPub []byte) ([]byte, error) {
	k, err := EnsureP2PKey()
	if err != nil {
		return nil, err
	}
	pk, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, err
	}
	return k.ECDH(pk)
}

func resetP2PKeyForTest() {
	p2pKeyMu.Lock()
	p2pKey = nil
	p2pKeyMu.Unlock()
}

// RememberVis caches overlay IP/DNS, peers mode, P2P flag, and peer hints from Status.
// It also stores the hold settings and the instance_id the node assigned.
func RememberVis(st wire.Status) {
	visMu.Lock()
	visMode = st.Peers
	visP2P = st.P2P
	visHint = append([]wire.PeerHint(nil), st.PeerHints...)
	if st.IP != "" {
		visIP = st.IP
	}
	visGW = st.Gateway
	visIP6 = st.IP6
	visVLAN = st.VLAN
	visDNS = append([]string(nil), st.DNS...)
	visRoutes = visRoutes[:0]
	for _, r := range st.Routes {
		if r.Dst != "" {
			visRoutes = append(visRoutes, r.Dst)
		}
	}
	visPolicy = st.Policy
	visTTLMS = st.SessionTTLMS
	visFronts = append([]wire.FrontEP(nil), st.Fronts...)
	if len(st.MaskPub) == 32 {
		visMaskPub = append([]byte(nil), st.MaskPub...)
	}
	visMu.Unlock()
	rememberHold(st)
	if st.InstanceID != "" {
		RememberInstance(st.InstanceID)
	}
}

// LearnedFronts is the Status front map from the last OK handshake. Empty = URI/defaults.
func LearnedFronts() []wire.FrontEP {
	visMu.Lock()
	defer visMu.Unlock()
	return append([]wire.FrontEP(nil), visFronts...)
}

// LearnedMaskPub is the 64-hex node mask_pub from the last OK Status. Empty = none yet.
func LearnedMaskPub() string {
	visMu.Lock()
	defer visMu.Unlock()
	if len(visMaskPub) != 32 {
		return ""
	}
	return hex.EncodeToString(visMaskPub)
}

// FormatFronts is a compact STATUS token: get=/static/v3/build,ws=/api/v1/sync,quic
func FormatFronts(eps []wire.FrontEP) string {
	var b strings.Builder
	for _, e := range eps {
		if e.Name == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(e.Name)
		if e.Path != "" {
			b.WriteByte('=')
			b.WriteString(e.Path)
		}
	}
	return b.String()
}

// ParseFrontEPs reads FormatFronts output.
func ParseFrontEPs(s string) []wire.FrontEP {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []wire.FrontEP
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, path, ok := strings.Cut(p, "=")
		if !ok {
			out = append(out, wire.FrontEP{Name: p})
			continue
		}
		out = append(out, wire.FrontEP{Name: name, Path: path})
	}
	return out
}

// OverlayIP is the session overlay IPv4 from the last Status.
func OverlayIP() string {
	visMu.Lock()
	defer visMu.Unlock()
	return visIP
}

// OverlayGateway is the overlay/PPP gateway IPv4 from the last Status.
func OverlayGateway() string {
	visMu.Lock()
	defer visMu.Unlock()
	return visGW
}

// OverlayRoutes is managed CIDRs from the last Status (lab LAN behind a member).
func OverlayRoutes() []string {
	visMu.Lock()
	defer visMu.Unlock()
	return append([]string(nil), visRoutes...)
}

// RememberGateway stores the overlay/PPP gateway (SSTP peer, or a Status gw).
func RememberGateway(gw string) {
	visMu.Lock()
	visGW = gw
	visMu.Unlock()
}

// RememberOverlay sets the session IPv4 (and optional gateway) for non-loft paths.
func RememberOverlay(ip, gw string) {
	visMu.Lock()
	if ip != "" {
		visIP = ip
	}
	if gw != "" {
		visGW = gw
	}
	visMu.Unlock()
}

// OverlayIPv6 is the session overlay IPv6 address.
// Empty means IPv6 is not configured: inbound IPv6 is dropped so
// Happy Eyeballs falls back to IPv4, same as an IPv4-only tunnel.
func OverlayIPv6() string {
	visMu.Lock()
	defer visMu.Unlock()
	return visIP6
}

// OverlayDNS is Status dns (resolver IPs); empty means the node did not advertise any.
func OverlayDNS() []string {
	visMu.Lock()
	defer visMu.Unlock()
	return append([]string(nil), visDNS...)
}

// sessionTTLMS is Status live-session max; 0 means the session does not expire on its own.
func sessionTTLMS() uint32 {
	visMu.Lock()
	defer visMu.Unlock()
	return visTTLMS
}

// p2pPermit is Network.Allow reconstructed from Status policy. Old nodes
// omit policy: Present is false and this returns true (hub splice still checks).
// A direct connection never passes the node, so both ends run this check:
// the dialer before it dials, the receiver before it connects locally.
func p2pPermit(src, dst net.IP, proto string, dport, srcVLAN, dstVLAN int) bool {
	if src == nil || dst == nil {
		return false
	}
	visMu.Lock()
	pol := visPolicy
	mode := visMode
	own := visIP
	gw := visGW
	routes := append([]string(nil), visRoutes...)
	visMu.Unlock()
	if !pol.Present {
		return true
	}
	n := &netconf.Network{Peers: visPeers(mode)}
	if pol.ACLDefault == 1 {
		n.ACLDefault = "drop"
	}
	for _, r := range pol.Rules {
		n.Rules = append(n.Rules, netconfRule(r))
	}
	p := netconf.Packet{
		Src: src, Dst: dst, Proto: proto, Dport: dport,
		SrcVLAN: srcVLAN, DstVLAN: dstVLAN,
	}
	srcS, dstS := src.String(), dst.String()
	if srcS == own {
		p.SrcTags = append([]string(nil), pol.OwnTags...)
	} else {
		p.SrcTags = peerTags(pol, srcS)
	}
	if dstS == own {
		p.DstTags = append([]string(nil), pol.OwnTags...)
	} else {
		p.DstTags = peerTags(pol, dstS)
	}
	if gw != "" && dstS == gw {
		p.ToGW = true
	}
	for _, cidr := range routes {
		if inVisCIDR(cidr, dst) {
			p.Routed = true
			break
		}
	}
	return n.Allow(p)
}

// visPeers maps the Status peers byte to the netconf mode name.
func visPeers(mode byte) string {
	switch mode {
	case 1:
		return netconf.PeersMesh
	case 2:
		return netconf.PeersVLAN
	default:
		return netconf.PeersHub
	}
}

// peerTags is the tag list the policy carries for overlay ip.
func peerTags(pol wire.Policy, ip string) []string {
	for _, p := range pol.Peers {
		if p.IP == ip {
			return append([]string(nil), p.Tags...)
		}
	}
	return nil
}

func inVisCIDR(cidr string, ip net.IP) bool {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil || n == nil {
		return false
	}
	return n.Contains(ip)
}

// netconfRule is the reverse of policyRuleOf: wire rule back to netconf.
func netconfRule(r wire.PolicyRule) netconf.Rule {
	out := netconf.Rule{Src: r.Src, Dst: r.Dst, SrcTag: r.SrcTag, DstTag: r.DstTag}
	if r.Action == 1 {
		out.Action = "accept"
	} else {
		out.Action = "drop"
	}
	if r.SrcVLAN != nil {
		v := int(*r.SrcVLAN)
		out.SrcVLAN = &v
	}
	if r.DstVLAN != nil {
		v := int(*r.DstVLAN)
		out.DstVLAN = &v
	}
	switch r.Proto {
	case 1:
		out.Proto = "tcp"
	case 2:
		out.Proto = "udp"
	case 3:
		out.Proto = "icmp"
	}
	for _, p := range r.Dport {
		out.Dport = append(out.Dport, int(p))
	}
	for _, p := range r.Sport {
		out.Sport = append(out.Sport, int(p))
	}
	return out
}

// tryP2P dials a matching peer hint with the LFT preface; nil means use hub splice.
// It is tried only for an IP-literal target, with p2p enabled on both the
// client and the node, in mesh or vlan mode. The dial timeout is short
// (800 ms) so a peer behind NAT costs little before the fallback.
func tryP2P(target string) net.Conn {
	if !p2pAllow.Load() {
		return nil
	}
	visMu.Lock()
	mode, p2p, hints := visMode, visP2P, visHint
	visMu.Unlock()
	if mode == 0 || p2p == 0 {
		return nil
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || net.ParseIP(host) == nil {
		return nil
	}
	srcIP := overlayIPv4()
	dstIP := net.ParseIP(host)
	dport := int(wire.Port(port))
	for _, h := range hints {
		if h.IP != host || h.Host == "" || h.Port == 0 || len(h.Pub) != 32 {
			continue
		}
		srcVLAN, dstVLAN := int(visVLANOf()), int(h.VLAN)
		if !p2pPermit(srcIP, dstIP, "tcp", dport, srcVLAN, dstVLAN) {
			continue
		}
		c, err := net.DialTimeout("tcp", net.JoinHostPort(h.Host, strconv.Itoa(int(h.Port))), 800*time.Millisecond)
		if err != nil {
			continue
		}
		if err := p2pWriteHello(c, port, h.Pub); err != nil {
			c.Close()
			continue
		}
		return c
	}
	return nil
}

const lftSize = 4 + 4 + 16 + 16 + 2 // "LFT\x00" || src_ipv4 || mac || nonce || port

var lftMagic = [4]byte{'L', 'F', 'T', 0}

// overlayIPv4 is this client's overlay IPv4, or nil.
func overlayIPv4() net.IP {
	visMu.Lock()
	s := visIP
	visMu.Unlock()
	return net.ParseIP(s).To4()
}

func visVLANOf() uint16 {
	visMu.Lock()
	defer visMu.Unlock()
	return visVLAN
}

// hintPub is the P2P public key in the hint for overlay ip, or nil.
func hintPub(ip string) []byte {
	if h := hintOf(ip); h != nil && len(h.Pub) == 32 {
		return append([]byte(nil), h.Pub...)
	}
	return nil
}

func hintOf(ip string) *wire.PeerHint {
	visMu.Lock()
	defer visMu.Unlock()
	for i := range visHint {
		if visHint[i].IP == ip {
			h := visHint[i]
			return &h
		}
	}
	return nil
}

// lftMAC is the 16-byte HMAC-SHA256 that proves the LFT sender holds the
// P2P key of the member at src4. It also covers the nonce and destination
// port, so the port cannot be changed in transit.
func lftMAC(shared, nonce, src4 []byte, port uint16) []byte {
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], port)
	mac := hmac.New(sha256.New, shared)
	mac.Write([]byte("lft"))
	mac.Write(nonce)
	mac.Write(pb[:])
	mac.Write(src4)
	return mac.Sum(nil)[:16]
}

// p2pWriteHello sends LFT || src_ipv4 || HMAC(ECDH, lft||nonce||port||src) || nonce || dest_port.
func p2pWriteHello(c net.Conn, destPort string, peerPub []byte) error {
	src := overlayIPv4()
	if src == nil || len(peerPub) != 32 {
		return io.ErrUnexpectedEOF
	}
	shared, err := p2pShared(peerPub)
	if err != nil {
		return err
	}
	_ = c.SetDeadline(time.Now().Add(time.Second))
	p := wire.Port(destPort)
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	buf := make([]byte, lftSize)
	copy(buf[0:4], lftMagic[:])
	copy(buf[4:8], src)
	copy(buf[8:24], lftMAC(shared, nonce, src, p))
	copy(buf[24:40], nonce)
	binary.BigEndian.PutUint16(buf[40:42], p)
	_, err = c.Write(buf)
	_ = c.SetDeadline(time.Time{})
	return err
}

// p2pReadHello verifies LFT using the initiator pub from Status hints.
// A sender that is not in this client's hints is refused, since there is no
// key to check it against.
func p2pReadHello(c net.Conn) (port uint16, src net.IP, ok bool) {
	_ = c.SetDeadline(time.Now().Add(time.Second))
	buf := make([]byte, lftSize)
	if _, err := io.ReadFull(c, buf); err != nil {
		return 0, nil, false
	}
	_ = c.SetDeadline(time.Time{})
	if string(buf[0:4]) != string(lftMagic[:]) {
		return 0, nil, false
	}
	src = net.IP(append([]byte(nil), buf[4:8]...))
	pub := hintPub(src.String())
	if len(pub) != 32 {
		return 0, nil, false
	}
	shared, err := p2pShared(pub)
	if err != nil {
		return 0, nil, false
	}
	port = binary.BigEndian.Uint16(buf[40:42])
	want := lftMAC(shared, buf[24:40], buf[4:8], port)
	if !hmac.Equal(want, buf[8:24]) {
		return 0, nil, false
	}
	return port, src, true
}

// acceptMember splices incoming overlay Open to the local target (hub splice).
// These are streams the node opens on this client's mux on behalf of another
// member (see Overlay.forwardMember): the target is 127.0.0.1:port, or a
// host in a subnet this member routes.
func acceptMember(d Dialer) {
	sess := d.Session()
	if sess == nil {
		return
	}
	go func() {
		for {
			st, err := sess.Accept()
			if err != nil {
				return
			}
			go handleIncoming(st)
		}
	}()
}

// handleIncoming dials st.Target locally and copies bytes.
func handleIncoming(st *mux.Stream) {
	defer st.Close()
	c, err := net.DialTimeout("tcp", st.Target(), 10*time.Second)
	if err != nil {
		st.Reset(wire.StreamRefused)
		return
	}
	defer c.Close()
	go func() { _, _ = io.Copy(c, st); c.Close() }()
	_, _ = io.Copy(st, c)
}

// announceP2P listens on TCP, Opens loft.peer:<port>, writes the X25519 pub, and serves LFT inbound.
func announceP2P(d Dialer) {
	if !p2pAllow.Load() {
		return
	}
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port
	sess := d.Session()
	if sess != nil {
		st, err := sess.Open(net.JoinHostPort("loft.peer", strconv.Itoa(port)))
		if err == nil {
			if pub := p2pPublic(); len(pub) == 32 {
				_, _ = st.Write(pub)
			}
			_ = st.Close()
		}
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go serveP2P(c)
	}
}

// serveP2P splices a verified P2P conn to 127.0.0.1:dest_port.
func serveP2P(c net.Conn) {
	defer c.Close()
	port, src, ok := p2pReadHello(c)
	if !ok {
		return
	}
	own := net.ParseIP(OverlayIP())
	srcVLAN := 0
	if h := hintOf(src.String()); h != nil {
		srcVLAN = int(h.VLAN)
	}
	if !p2pPermit(src, own, "tcp", int(port), srcVLAN, int(visVLANOf())) {
		return
	}
	local, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))), 10*time.Second)
	if err != nil {
		return
	}
	defer local.Close()
	go func() { _, _ = io.Copy(local, c); local.Close() }()
	_, _ = io.Copy(c, local)
}
