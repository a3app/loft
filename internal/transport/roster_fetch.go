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
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
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
	RememberRoster(r.Encode())
	return nil
}

// BootstrapRoster pins controller_pub then fetches from bootstrap and CDN manifest URLs.
func BootstrapRoster(ctx context.Context, cfg *config.ClientConfig) {
	if pub, err := ParseControllerPub(cfg.ControllerPub); err == nil && len(pub) == 32 {
		PinControllerPub(pub)
	}
	urls := []string{}
	if cfg.Bootstrap != "" {
		urls = append(urls, cfg.Bootstrap)
	}
	path := "/static/v3/manifest.json"
	for _, o := range cfg.Outbounds {
		if o.URL == "" {
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

func fetchDirectManifest(rawURL string) {
	base := httpOriginURL(rawURL)
	if base == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = FetchRoster(ctx, strings.TrimRight(base, "/")+"/static/v3/manifest.json")
}
