package transport

// Mesh P2P: Status peer hints, loft.peer announce, LFT2 TCP preface, hub-splice fallback.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// p2pAllow, vis*, and p2pTok are the client overlay view and LFT2 token.
var (
	p2pAllow atomic.Bool
	visMu    sync.Mutex
	visMode  byte
	visP2P   byte
	visHint  []wire.PeerHint
	visIP    string
	visGW    string
	visIP6   string
	visDNS   []string
	p2pTok   []byte
)

// SetP2PClient enables direct TCP attempts and stores the member token for the LFT2 MAC.
func SetP2PClient(on bool, token []byte) {
	p2pAllow.Store(on)
	p2pTok = append([]byte(nil), token...)
}

// RememberVis caches overlay IP/DNS, peers mode, P2P flag, and peer hints from Status.
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
	visDNS = append([]string(nil), st.DNS...)
	visMu.Unlock()
	rememberHold(st)
	if st.InstanceID != "" {
		RememberInstance(st.InstanceID)
	}
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

// tryP2P dials a matching peer hint with the LFT2 preface; nil means use hub splice.
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
	for _, h := range hints {
		if h.IP != host || h.Host == "" || h.Port == 0 {
			continue
		}
		c, err := net.DialTimeout("tcp", net.JoinHostPort(h.Host, strconv.Itoa(int(h.Port))), 800*time.Millisecond)
		if err != nil {
			continue
		}
		if err := p2pWriteHello(c, port); err != nil {
			c.Close()
			continue
		}
		return c
	}
	return nil
}

// p2pWriteHello sends LFT2 || HMAC(token, nonce||port)[0:16] || nonce || dest_port.
func p2pWriteHello(c net.Conn, destPort string) error {
	_ = c.SetDeadline(time.Now().Add(time.Second))
	p := wire.Port(destPort)
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], p)
	mac := hmac.New(sha256.New, p2pTok)
	mac.Write(nonce[:])
	mac.Write(pb[:])
	sum := mac.Sum(nil)[:16]
	buf := make([]byte, 4+16+16+2)
	copy(buf[0:4], "LFT2")
	copy(buf[4:20], sum)
	copy(buf[20:36], nonce[:])
	copy(buf[36:38], pb[:])
	_, err := c.Write(buf)
	_ = c.SetDeadline(time.Time{})
	return err
}

// p2pReadHello verifies the LFT2 preface and returns dest_port.
func p2pReadHello(c net.Conn) (uint16, bool) {
	_ = c.SetDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 38)
	if _, err := io.ReadFull(c, buf); err != nil {
		return 0, false
	}
	_ = c.SetDeadline(time.Time{})
	if string(buf[0:4]) != "LFT2" {
		return 0, false
	}
	mac := hmac.New(sha256.New, p2pTok)
	mac.Write(buf[20:36])
	mac.Write(buf[36:38])
	if !hmac.Equal(mac.Sum(nil)[:16], buf[4:20]) {
		return 0, false
	}
	return binary.BigEndian.Uint16(buf[36:38]), true
}

// acceptMember splices incoming overlay Open to the local target (hub splice).
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

// announceP2P listens on TCP, Opens loft.peer:<port>, and serves LFT2 inbound.
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
	port, ok := p2pReadHello(c)
	if !ok {
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
