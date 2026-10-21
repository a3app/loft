package transport

// Client roster fetch: GET public manifest, verify Ed25519, pin controller_pub.

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
	for _, o := range cfg.Outbounds {
		if o.CDN == "" || o.URL == "" {
			continue
		}
		base := httpOriginURL(o.URL)
		if base == "" {
			continue
		}
		urls = append(urls, strings.TrimRight(base, "/")+path)
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

// decoyNodeHosts is host:port of non-CDN outbounds. Unauthenticated TLS to
// those addresses is spliced to origin; a public GET times out.
func decoyNodeHosts(cfg *config.ClientConfig) map[string]struct{} {
	m := map[string]struct{}{}
	if cfg == nil {
		return m
	}
	for _, o := range cfg.Outbounds {
		if o.CDN != "" || o.URL == "" {
			continue
		}
		if hp := originHostPort(o.URL); hp != "" {
			m[hp] = struct{}{}
		}
	}
	return m
}

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

func applyRelayIdentity(addr string, e wire.RelayInfo) {
	if e.Decoy != "" {
		RememberDecoy(addr, []string{e.Decoy})
	}
	if len(e.MaskPub) == 32 {
		RememberMaskPub(addr, e.MaskPub)
	}
}
