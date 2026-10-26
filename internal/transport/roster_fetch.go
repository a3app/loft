package transport

// Client roster fetch: GET public manifest, verify Ed25519, pin controller_pub.
//
// The roster is the controller-signed list of live fronts and relays. A
// client normally gets it inside Status, but a fresh client has no session
// yet. So the roster is also published as a plain JSON file ("manifest") on
// a public URL, typically behind a CDN. Trust comes from the signature, not
// from TLS: the client accepts only a roster signed by the pinned controller
// key (controller_pub), or, with tofu: true, by the first valid signer it
// sees (trust on first use).

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/wire"
)

// ParseControllerPub decodes a 32-byte controller Ed25519 key from hex or base64.
func ParseControllerPub(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("controller_pub: want 32 bytes")
}

// FetchRoster GETs a public roster JSON manifest, verifies it, and stores the roster.
func FetchRoster(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", chromeUA)
	cl := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			// Roster is signed; TLS CA is not the trust. codeql[go/disabled-certificate-check]
			InsecureSkipVerify: true,
		},
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			d := 8 * time.Second
			if deadline, ok := ctx.Deadline(); ok {
				if r := time.Until(deadline); r > 0 && r < d {
					d = r
				}
			}
			return protect.Timeout("tcp4", address, d)
		},
	}}
	res, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("roster fetch: %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return err
	}
	r, err := wire.ParseManifest(b)
	if err != nil {
		return err
	}
	if err := r.Verify(); err != nil {
		return err
	}
	if !RememberRoster(r.Encode()) {
		return fmt.Errorf("roster: controller_pub required (set controller_pub or tofu: true)")
	}
	return nil
}

// BootstrapRoster pins controller_pub then fetches from bootstrap and CDN
// manifest URLs. Direct (non-CDN) node origins are skipped: a GET without sid
// is spliced to decoy and hangs until the 8s timeout. A roster already held
// from Status is enough for reconnect.
// Empty controller_pub refuses the roster unless cfg.Tofu (trust first valid signer).
func BootstrapRoster(ctx context.Context, cfg *config.ClientConfig) {
	if cfg == nil {
		return
	}
	AllowTOFU(cfg.Tofu)
	if pub, err := ParseControllerPub(cfg.ControllerPub); err == nil && len(pub) == 32 {
		PinControllerPub(pub)
	} else if cfg.Tofu {
		if pub := loadPersistedPub(); len(pub) == 32 {
			PinControllerPub(pub)
		}
	}
	if len(CachedRoster()) > 0 {
		return
	}
	decoy := decoyNodeHosts(cfg)
	urls := []string{}
	if cfg.Bootstrap != "" && !skipPublicFetch(cfg.Bootstrap, decoy) {
		urls = append(urls, cfg.Bootstrap)
	}
	path := "/static/v3/manifest.json"
	seen := map[string]struct{}{}
	for _, u := range urls {
		seen[u] = struct{}{}
	}
	for _, o := range cfg.Outbounds {
		if o.CDN == "" || o.URL == "" {
			continue
		}
		base := httpOriginURL(o.URL)
		if base == "" {
			continue
		}
		urls = appendPublicURL(urls, seen, strings.TrimRight(base, "/")+path, decoy)
	}
	for _, u := range urls {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := FetchRoster(cctx, u)
		cancel()
		if err == nil {
			return
		}
	}
}

// httpOriginURL is the scheme://host[:port] of raw, with ws/wss mapped to
// http/https, or "" when raw has no host.
func httpOriginURL(raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "wss://"):
		s = "https://" + strings.TrimPrefix(s, "wss://")
	case strings.HasPrefix(s, "ws://"):
		s = "http://" + strings.TrimPrefix(s, "ws://")
	}
	u, err := parseURL(s)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + u.Host
}

// originHostPort is host:port of raw, defaulting https to 443 and http to 80.
func originHostPort(raw string) string {
	base := httpOriginURL(raw)
	if base == "" {
		return ""
	}
	u, err := parseURL(base)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	return net.JoinHostPort(host, port)
}

// decoyNodeHosts is host:port that must not take a sid-less GET.
// CDN == "" is the node. -cdn stamped on the node URL is still the node
// when another outbound (the edge) has a different host: directNodeURL
// is skipped too. Unauthenticated TLS to the node is spliced to decoy
// and the GET sits until the client timeout.
func decoyNodeHosts(cfg *config.ClientConfig) map[string]struct{} {
	m := map[string]struct{}{}
	if cfg == nil {
		return m
	}
	direct := originHostPort(directNodeURL())
	other := false
	for _, o := range cfg.Outbounds {
		hp := originHostPort(o.URL)
		if hp == "" {
			continue
		}
		if o.CDN == "" {
			m[hp] = struct{}{}
		}
		if direct != "" && hp != direct {
			other = true
		}
	}
	if direct != "" && other {
		m[direct] = struct{}{}
	}
	return m
}

// appendPublicURL adds raw unless it is empty, already listed, or a node origin.
func appendPublicURL(urls []string, seen map[string]struct{}, raw string, decoy map[string]struct{}) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || skipPublicFetch(raw, decoy) {
		return urls
	}
	if _, ok := seen[raw]; ok {
		return urls
	}
	seen[raw] = struct{}{}
	return append(urls, raw)
}

// skipPublicFetch reports whether url points at a node origin from decoy.
func skipPublicFetch(url string, decoy map[string]struct{}) bool {
	hp := originHostPort(url)
	if hp == "" {
		return false
	}
	_, ok := decoy[hp]
	return ok
}

// applyRosterIdentity copies decoy SNI and mask_pub from a matching roster
// relay onto addr so the first ClientHello is dest-shaped.
// "Dest-shaped" means the ClientHello uses the decoy site's name as SNI and
// carries the sid v3 mark for that node's mask key, so to the network it
// looks like a visit to the decoy site. With no host match, the first entry
// that has a decoy or mask key (or the controller itself, id "self") is used.
func applyRosterIdentity(addr, urlHost string) {
	urlHost = strings.ToLower(strings.TrimSpace(urlHost))
	var fallback wire.RelayInfo
	var found bool
	for _, e := range CachedRoster() {
		eh := ""
		if u, err := parseURL(e.URL); err == nil {
			eh = strings.ToLower(u.Hostname())
		}
		if eh != "" && eh == urlHost {
			applyRelayIdentity(addr, e)
			return
		}
		if !found && (e.ID == "self" || e.Decoy != "" || len(e.MaskPub) == 32) {
			fallback = e
			found = true
		}
	}
	if found {
		applyRelayIdentity(addr, fallback)
	}
}

// applyRelayIdentity remembers e's decoy name and mask key for addr.
func applyRelayIdentity(addr string, e wire.RelayInfo) {
	if e.Decoy != "" {
		RememberDecoy(addr, []string{e.Decoy})
	}
	if len(e.MaskPub) == 32 {
		RememberMaskPub(addr, e.MaskPub)
	}
}
