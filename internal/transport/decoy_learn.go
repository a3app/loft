package transport

// Decoy learning caches node identity, path canary, replicas, and roster.
//
// This is the client side. Each Status the node sends (the encrypted answer
// to the client's handshake) tells the client things it should reuse on the
// next dial: the node's decoy site names, its mask_pub, the fronts it
// serves, the canary policy, replica addresses and the signed roster.
// Everything here is kept in memory per node address (host:port); the
// decoy names and mask_pub also go to a disk cache (front_cache.go) so a new
// process can mark its very first ClientHello.
//
// The canary checks that nothing on the path rewrote the TLS handshake; its
// policy says how to shape the next ClientHello so it passes. The roster is
// a controller-signed list of live fronts and relays the client can fail
// over to; the client accepts only rosters signed by the pinned controller
// key.

import (
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/wire"
)

// learned is this process's view of each node's decoy identity, keyed by
// outbound host:port. The node is the source of truth (Status.Decoy); a
// probe of the IP (no HMAC) is only a bootstrap so the first ClientHello
// already uses the right SNI.
var learned sync.Map // string -> []string; empty slice = decoy off

// RememberDecoy stores origins for addr. Empty slice means decoy is off.
func RememberDecoy(addr string, origins []string) {
	if origins == nil {
		origins = []string{}
	}
	learned.Store(addr, append([]string(nil), origins...))
}

// CachedDecoy returns a copy of stored origins for addr.
func CachedDecoy(addr string) (origins []string, known bool) {
	v, ok := learned.Load(addr)
	if !ok {
		return nil, false
	}
	return append([]string(nil), v.([]string)...), true
}

// clearDecoyCache drops every learned decoy entry.
func clearDecoyCache() {
	learned.Range(func(k, _ any) bool { learned.Delete(k); return true })
	frontDiskOnce.Range(func(k, _ any) bool { frontDiskOnce.Delete(k); return true })
}

// canaryPol and replicaMap cache Status canary and unsigned replicas per addr.
var canaryPol sync.Map  // addr -> CanaryPolicy
var replicaMap sync.Map // addr -> []string

// RememberCanary stores Status canary_flags and canary_pad for addr.
// Bit 0 asks for a fat (browser-sized) ClientHello, bit 1 for a ClientHello
// split across TCP segments.
func RememberCanary(addr string, flags byte, pad uint16) {
	canaryPol.Store(addr, CanaryPolicy{Fat: flags&1 != 0, Split: flags&2 != 0, Pad: pad})
}

// CachedCanary returns the stored policy, or Fat ClientHello by default.
func CachedCanary(addr string) CanaryPolicy {
	v, ok := canaryPol.Load(addr)
	if !ok {
		return CanaryPolicy{Fat: true}
	}
	return v.(CanaryPolicy)
}

// RememberReplicas stores unsigned replica hosts from Status: extra node
// host:port values the dialer may try for failover.
func RememberReplicas(addr string, hosts []string) {
	replicaMap.Store(addr, append([]string(nil), hosts...))
}

// rosterMu guards heldRoster, pinnedPub, and tofuOK.
var rosterMu sync.Mutex
var heldRoster wire.Roster
var pinnedPub []byte
var tofuOK bool

// AllowTOFU lets RememberRoster pin the first valid roster signer when
// client.yaml has no controller_pub. Default off: empty pub refuses roster.
func AllowTOFU(ok bool) {
	rosterMu.Lock()
	tofuOK = ok
	rosterMu.Unlock()
}

// PinControllerPub rejects later rosters signed by a different key.
func PinControllerPub(pub []byte) {
	rosterMu.Lock()
	pinnedPub = append([]byte(nil), pub...)
	rosterMu.Unlock()
}

// PinnedControllerPub is the 32-byte Ed25519 pin, or nil.
func PinnedControllerPub() []byte {
	rosterMu.Lock()
	defer rosterMu.Unlock()
	return append([]byte(nil), pinnedPub...)
}

// RememberRoster stores a verified roster with a non-decreasing serial.
// Without a pin it is a no-op unless AllowTOFU is set (YAML tofu: true).
// TOFU is "trust on first use": the signer of the first valid roster
// becomes the pin. A roster signed by another key, or with an older serial,
// is refused, so an old roster cannot replace a newer one.
func RememberRoster(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	r, err := wire.DecodeRoster(raw)
	if err != nil {
		return false
	}
	if err := r.Verify(); err != nil {
		return false
	}
	rosterMu.Lock()
	defer rosterMu.Unlock()
	if !trustSignerLocked(r.Pub) {
		return false
	}
	if r.Serial < heldRoster.Serial {
		return false
	}
	heldRoster = r
	return true
}

// trustSignerLocked reports whether pub is the controller's signing key: the
// pin, or the key of the roster already held. The roster and the address
// lists are signed by the same key, so a document signed by any other key is
// refused even if its own signature is valid. With neither a pin nor a roster,
// TOFU (when allowed) pins pub. Caller holds rosterMu.
func trustSignerLocked(pub []byte) bool {
	if len(pinnedPub) == 0 && len(heldRoster.Pub) == 0 {
		if !tofuOK {
			return false
		}
		pinnedPub = append([]byte(nil), pub...)
		persistPinnedPubLocked(pinnedPub)
	}
	if len(pinnedPub) > 0 && string(pinnedPub) != string(pub) {
		return false
	}
	if len(heldRoster.Pub) > 0 && string(heldRoster.Pub) != string(pub) {
		return false
	}
	return true
}

// persistPinnedPubLocked writes the TOFU pin next to client.instance.
// Caller holds rosterMu; instanceDir uses a different lock.
func persistPinnedPubLocked(pub []byte) {
	if len(pub) != 32 {
		return
	}
	dir := instanceDir()
	if dir == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "controller.pub"), []byte(hex.EncodeToString(pub)+"\n"), 0o600)
}

// loadPersistedPub reads the TOFU pin written by persistPinnedPubLocked,
// or nil when there is none.
func loadPersistedPub() []byte {
	dir := instanceDir()
	if dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "controller.pub"))
	if err != nil {
		return nil
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != 32 {
		return nil
	}
	return raw
}

func resetRosterForTest() {
	rosterMu.Lock()
	heldRoster = wire.Roster{}
	pinnedPub = nil
	tofuOK = false
	rosterMu.Unlock()
}

// CachedRoster returns live (non-drain) relays from the held roster.
func CachedRoster() []wire.RelayInfo {
	rosterMu.Lock()
	defer rosterMu.Unlock()
	out := make([]wire.RelayInfo, 0, len(heldRoster.Relays))
	for _, e := range heldRoster.Relays {
		if e.Drain || e.URL == "" {
			continue
		}
		out = append(out, e)
	}
	return out
}

// CachedReplicas returns a copy of replica hosts for addr.
func CachedReplicas(addr string) []string {
	v, ok := replicaMap.Load(addr)
	if !ok {
		return nil
	}
	return append([]string(nil), v.([]string)...)
}

// CanaryPolicy is Status canary_flags (bit 0 fat ClientHello, bit 1 split) and canary_pad.
type CanaryPolicy struct {
	Fat   bool
	Split bool
	Pad   uint16
}

// AdaptError: ClientHello SNI did not match the node's identity. Start retries
// once with the cached origin. ("Origin" is the decoy site name.)
type AdaptError struct {
	Addr string
	SNI  string
}

// Error implements error.
func (e *AdaptError) Error() string {
	return "decoy: node identity is " + e.SNI
}

// outboundAddr is host:port of o.URL.
func outboundAddr(o config.Outbound) (string, error) {
	u, err := parseURL(o.URL)
	if err != nil {
		return "", err
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http", "ws":
			port = "80"
		default:
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

// resolveTLS picks ClientHello SNI. CDN and direct fronts keep the URL host
// so the SNI matches the dialed IP. Explicit o.Decoy opts into dest SNI.
// "Dest SNI" is the decoy site's name sent to the node's IP; that hello
// gets mask TLS. It also loads the disk cache and the held roster for addr
// so a known mask_pub is ready for sid v3 before the first dial.
// insecure is true for plain http/ws URLs, for o.Insecure, and for dest SNI
// (the masked leaf is checked by the serial bind, not by a CA).
func resolveTLS(o config.Outbound) (addr, sni string, insecure bool, err error) {
	u, err := parseURL(o.URL)
	if err != nil {
		return "", "", false, err
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http", "ws":
			port = "80"
		default:
			port = "443"
		}
	}
	addr = net.JoinHostPort(u.Hostname(), port)
	loadFrontCache(addr)
	if pub := ParseMaskPub(o.MaskPub); len(pub) == 32 {
		RememberMaskPub(addr, pub)
	}
	sni = o.SNI
	if sni == "" {
		sni = u.Hostname()
	}
	insecure = o.Insecure || u.Scheme == "http" || u.Scheme == "ws"
	if o.CDN != "" || u.Scheme == "http" || u.Scheme == "ws" {
		return addr, sni, insecure, nil
	}
	host := u.Hostname()
	if !loopbackAddr(addr) && net.ParseIP(host) == nil {
		// Disk cache / a held roster teach mask_pub for sid v3. Do not GET
		// the node's public manifest here: without sid the 1.0.18 node
		// splices that TLS to decoy and the client waits out an 8s timeout.
		applyRosterIdentity(addr, host)
		saveFrontCache(addr)
	}
	// Dest SNI to the node IP is SNI/IP mismatch; some paths hold it.
	// Explicit -decoy still opts in. Roster/cache teach mask_pub for sid v3.
	if o.Decoy != "" {
		RememberDecoy(addr, []string{o.Decoy})
		saveFrontCache(addr)
		return addr, decoyHost(o.Decoy), true, nil
	}
	return addr, sni, insecure, nil
}

// adoptDecoy caches Status identity fields. A live session is kept: dest SNI
// retry after a working own-SNI handshake is held on SNI/IP-mismatch paths.
// ("Held" means some networks stall a hello whose SNI does not belong to
// the dialed IP.) usedSNI and apply are unused; it always returns nil.
func adoptDecoy(addr, usedSNI string, st wire.Status, apply bool) error {
	_ = usedSNI
	_ = apply
	RememberDecoy(addr, st.Decoy)
	RememberFronts(addr, st.Caps)
	RememberMaskPub(addr, st.MaskPub)
	publishMask(st.MaskPub)
	RememberReplicas(addr, st.Replicas)
	RememberCanary(addr, st.CanaryFlags, st.CanaryPad)
	RememberRoster(st.Roster)
	maybeRefreshLists(st)
	RememberVis(st)
	saveFrontCache(addr)
	return nil
}

// peerAddrs are every outbound host:port of this process (CDN and node).
// Status mask_pub is a property of the node, not of the dial that learned it.
var peerAddrs sync.Map

// NoteOutbound remembers o's host:port so a later Status mask_pub is stored
// for every front, not only the address that completed the handshake.
func NoteOutbound(o config.Outbound) {
	addr, err := outboundAddr(o)
	if err != nil || addr == "" {
		return
	}
	peerAddrs.Store(addr, struct{}{})
}

// publishMask copies pub onto every noted outbound and the disk cache.
// A GET through the CDN otherwise leaves quic/h3 on the node address unmarked.
func publishMask(pub []byte) {
	if len(pub) != 32 {
		return
	}
	peerAddrs.Range(func(k, _ any) bool {
		addr, _ := k.(string)
		if addr == "" {
			return true
		}
		RememberMaskPub(addr, pub)
		saveFrontCache(addr)
		return true
	})
}

// DiscoverDecoy dials addr without a session HMAC so the node splices to its
// origin (or presents its own cert if decoy is off). Returns the name a
// stranger would see, and whether that name is not the URL host.
func DiscoverDecoy(addr, hint, fingerprint string) (name string, foreign bool, err error) {
	raw, err := protect.Timeout("tcp4", addr, 5*time.Second)
	if err != nil {
		return "", false, err
	}
	_ = raw.SetDeadline(time.Now().Add(4 * time.Second))
	cfg := &utls.Config{
		ServerName:         hint,
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
	}
	u := utls.UClient(raw, cfg, helloID(fingerprint))
	if err := u.Handshake(); err != nil {
		raw.Close()
		return "", false, err
	}
	state := u.ConnectionState()
	_ = u.Close()
	return peerName(state.PeerCertificates, hint)
}

// peerName picks a certificate DNS name; foreign is true when it is not hint.
func peerName(certs []*x509.Certificate, hint string) (string, bool, error) {
	if len(certs) == 0 {
		return hint, false, fmt.Errorf("decoy: no peer certificate")
	}
	c := certs[0]
	names := append([]string{}, c.DNSNames...)
	if c.Subject.CommonName != "" {
		names = append(names, c.Subject.CommonName)
	}
	hint = strings.ToLower(strings.TrimSpace(hint))
	for _, n := range names {
		if matchHost(n, hint) {
			return hint, false, nil
		}
	}
	if len(c.DNSNames) > 0 {
		return c.DNSNames[0], true, nil
	}
	if c.Subject.CommonName != "" {
		return c.Subject.CommonName, true, nil
	}
	return hint, false, nil
}

// matchHost reports whether host matches pattern, including a single-label *.
func matchHost(pattern, host string) bool {
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:]) {
		rest := strings.TrimSuffix(host, pattern[1:])
		return rest != "" && !strings.Contains(rest, ".")
	}
	return false
}
