package web

// This file gets TLS certificates from Let's Encrypt (or another ACME
// server; ACME is the protocol Let's Encrypt speaks). It answers HTTP-01
// challenges, runs a "preflight" self-check before asking for a certificate,
// and drives the whole order: account, proof of each name, CSR, download.
// domains.go decides which names need a certificate and stores the result;
// dns.go supplies the DNS-01 records when a name is proved through DNS.

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

// LetsEncrypt is the production ACME directory.
const LetsEncrypt = "https://acme-v02.api.letsencrypt.org/directory"

// ACME issues certificates with HTTP-01. nginx on :80 passes
// /.well-known/acme-challenge/ to the panel, which answers from tokens.
type ACME struct {
	Dir      string // data directory: acme-account.pem lives here
	URL      string // ACME directory; empty = Let's Encrypt
	mu       sync.Mutex
	tokens   map[string]string // token → key authorization
	lookup   func(ctx context.Context, host string) ([]net.IPAddr, error)
	localIPs func() []net.IP
	httpGet  func(ctx context.Context, url string) (string, error)
}

// Issued is a fresh certificate.
type Issued struct {
	Fullchain string
	Key       string
	NotAfter  time.Time
}

// challenge serves GET /.well-known/acme-challenge/{token}. The ACME server
// fetches this URL to check that the name really points at this server; the
// answer is the "key authorization" stored by put for that token. Unknown
// tokens get 404 so the panel does not leak anything to random visitors.
func (a *ACME) challenge(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	a.mu.Lock()
	ka, ok := a.tokens[tok]
	a.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	io.WriteString(w, ka)
}

// put remembers the answer ka for a challenge token until drop removes it.
func (a *ACME) put(tok, ka string) {
	a.mu.Lock()
	if a.tokens == nil {
		a.tokens = map[string]string{}
	}
	a.tokens[tok] = ka
	a.mu.Unlock()
}

// drop forgets a challenge token once its check is over.
func (a *ACME) drop(tok string) {
	a.mu.Lock()
	delete(a.tokens, tok)
	a.mu.Unlock()
}

// acmeName is a public DNS name: labels, a dot, no address, no port, no URL.
var acmeName = regexp.MustCompile(`^(?i)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// Preflight checks what Let's Encrypt will check, with clearer errors: the
// name points at this server, and http://name/.well-known/… reaches the panel.
func (a *ACME) Preflight(ctx context.Context, name string) error {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if len(name) == 0 || len(name) > 253 || !acmeName.MatchString(name) {
		return fmt.Errorf("%s is not a DNS name", name)
	}
	// 1. The name must resolve to at least one public address, and one of
	// them must belong to this server. a.lookup and a.localIPs are only set
	// by tests; in production the system resolver and ServerIPs are used.
	lookup := a.lookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	ips, err := lookup(ctx, name)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("%s does not resolve yet: add an A record for this server and wait a few minutes", name)
	}
	pub := publicAddrs(ips)
	if len(pub) == 0 {
		got := make([]string, len(ips))
		for i, ip := range ips {
			got[i] = ip.IP.String()
		}
		return fmt.Errorf("%s points at %s, which is not a public address", name, strings.Join(got, ", "))
	}
	local := ServerIPs()
	if a.localIPs != nil {
		local = a.localIPs()
	}
	if len(local) > 0 && !ipOverlap(pub, local) {
		return fmt.Errorf("%s points at %s, this server is %s", name, ipList(pub), ipList(local))
	}
	// 2. Publish a random probe token and fetch it back through the public
	// name, exactly as Let's Encrypt will, to prove port 80 reaches the panel.
	b := make([]byte, 16)
	rand.Read(b)
	tok := "loft-probe-" + hex.EncodeToString(b)
	a.put(tok, tok)
	defer a.drop(tok)
	u := url.URL{Scheme: "http", Host: name, Path: "/.well-known/acme-challenge/" + tok}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	get := a.httpGet
	if get == nil {
		get = func(ctx context.Context, raw string) (string, error) {
			return challengeGet(ctx, raw, pub)
		}
	}
	body, err := get(cctx, u.String())
	if err != nil || strings.TrimSpace(body) != tok {
		if err == nil {
			err = errors.New("another site answered")
		}
		return fmt.Errorf("http://%s/ does not reach the panel (%v): port 80 must be open to this server", name, err)
	}
	return nil
}

// publicAddrs keeps only the addresses that are reachable from the Internet
// (see publicIP), because Let's Encrypt can only check those.
func publicAddrs(ips []net.IPAddr) []net.IP {
	var out []net.IP
	for _, a := range ips {
		if publicIP(a.IP) {
			out = append(out, a.IP)
		}
	}
	return out
}

// publicIP reports whether ip is a normal Internet address: not private,
// loopback, link-local, multicast or the zero address.
func publicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast() && !ip.IsUnspecified()
}

// ipOverlap reports whether the two lists share at least one address.
func ipOverlap(a, b []net.IP) bool {
	for _, x := range a {
		for _, y := range b {
			if x.Equal(y) {
				return true
			}
		}
	}
	return false
}

// challengeGet is the HTTP client Preflight uses to fetch its probe token
// from http://name/.well-known/acme-challenge/…. The name comes from the
// admin, so the request is locked down to stop it being abused to reach
// other machines or internal services (SSRF):
//   - only plain http on port 80, no user:password in the URL;
//   - no proxy, and redirects are not followed (a 3xx counts as failure);
//   - the connection is opened only to the addresses in allow, which are
//     the public IPs the name resolved to and that Preflight already matched
//     to this server, so a DNS change between the check and the fetch cannot
//     send the request somewhere else.
//
// It returns the response body (at most 4 KiB) when the status is 200.
func challengeGet(ctx context.Context, raw string, allow []net.IP) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return "", fmt.Errorf("bad URL")
	}
	host := u.Hostname()
	if u.Port() != "" && u.Port() != "80" {
		return "", fmt.Errorf("bad URL")
	}
	// Build a one-off client instead of http.DefaultClient so the rules above
	// cannot be bypassed by global settings such as HTTP_PROXY.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	c := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: nil,
			// The dialer ignores what the Transport resolved and only accepts the
			// URL's own host (or one of the allowed IPs) on port 80. It then tries the
			// allowed IPs in order and returns the first connection that succeeds.
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				h, port, err := net.SplitHostPort(addr)
				if err != nil || port != "80" {
					return nil, fmt.Errorf("bad addr")
				}
				if !strings.EqualFold(h, host) {
					if ip := net.ParseIP(h); ip == nil || !ipIn(ip, allow) {
						return nil, fmt.Errorf("bad addr")
					}
				}
				d := net.Dialer{Timeout: 5 * time.Second}
				var last error
				for _, ip := range allow {
					c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), "80"))
					if err == nil {
						return c, nil
					}
					last = err
				}
				if last == nil {
					last = errors.New("no address")
				}
				return nil, last
			},
		},
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return string(b), err
}

// ipIn reports whether ip is one of the addresses in allow.
func ipIn(ip net.IP, allow []net.IP) bool {
	for _, a := range allow {
		if ip.Equal(a) {
			return true
		}
	}
	return false
}

// DNS01 publishes a TXT record value at fqdn for an ACME dns-01 check and
// returns how to take it away.
type DNS01 func(ctx context.Context, fqdn, value string) (cleanup func(), err error)

// Issue gets a certificate for name from the ACME directory (HTTP-01).
func (a *ACME) Issue(ctx context.Context, name, email string) (Issued, error) {
	return a.IssueNames(ctx, []string{name}, email, nil)
}

// IssueNames gets one certificate for names; with dns it proves them by
// DNS-01 (needed for *.names and names that do not point here), else by
// HTTP-01 through nginx on :80.
func (a *ACME) IssueNames(ctx context.Context, names []string, email string, dns DNS01) (Issued, error) {
	if len(names) == 0 {
		return Issued{}, errors.New("no names")
	}
	// 1. Log in to the ACME server with this panel's account key. Register is
	// safe to repeat: an existing account is simply reused.
	key, err := a.accountKey()
	if err != nil {
		return Issued{}, err
	}
	dir := a.URL
	if dir == "" {
		dir = LetsEncrypt
	}
	c := &acme.Client{Key: key, DirectoryURL: dir, UserAgent: "loft-panel"}
	acct := &acme.Account{}
	if email != "" {
		acct.Contact = []string{"mailto:" + email}
	}
	if _, err := c.Register(ctx, acct, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return Issued{}, fmt.Errorf("ACME account: %w", err)
	}
	// 2. Open an order and prove control of every name in it. Names the
	// account has proved recently are already valid and are skipped.
	o, err := c.AuthorizeOrder(ctx, acme.DomainIDs(names...))
	if err != nil {
		return Issued{}, fmt.Errorf("ACME order: %w", err)
	}
	for _, u := range o.AuthzURLs {
		z, err := c.GetAuthorization(ctx, u)
		if err != nil {
			return Issued{}, err
		}
		if z.Status == acme.StatusValid {
			continue
		}
		// Pick the challenge type. DNS-01: publish a TXT record at
		// _acme-challenge.<name> and wait until public resolvers see it.
		// HTTP-01: keep the answer in memory so challenge can serve it.
		// Both are removed when IssueNames returns (the deferred calls).
		want := "http-01"
		if dns != nil {
			want = "dns-01"
		}
		var ch *acme.Challenge
		for _, x := range z.Challenges {
			if x.Type == want {
				ch = x
			}
		}
		if ch == nil {
			return Issued{}, fmt.Errorf("ACME offers no %s challenge for %s", want, z.Identifier.Value)
		}
		if dns != nil {
			val, err := c.DNS01ChallengeRecord(ch.Token)
			if err != nil {
				return Issued{}, err
			}
			host := "_acme-challenge." + z.Identifier.Value
			cleanup, err := dns(ctx, host, val)
			if err != nil {
				return Issued{}, fmt.Errorf("DNS-01 record for %s: %w", z.Identifier.Value, err)
			}
			defer cleanup()
			if err := waitTXT(ctx, host, val); err != nil {
				return Issued{}, err
			}
		} else {
			ka, err := c.HTTP01ChallengeResponse(ch.Token)
			if err != nil {
				return Issued{}, err
			}
			a.put(ch.Token, ka)
			defer a.drop(ch.Token)
		}
		if _, err := c.Accept(ctx, ch); err != nil {
			return Issued{}, fmt.Errorf("ACME accept: %w", err)
		}
		if _, err := c.WaitAuthorization(ctx, z.URI); err != nil {
			if dns != nil {
				return Issued{}, fmt.Errorf("Let's Encrypt could not check the DNS-01 record of %s: %w", z.Identifier.Value, err)
			}
			return Issued{}, fmt.Errorf("Let's Encrypt could not check http://%s/: %w", z.Identifier.Value, err)
		}
	}
	// 3. Wait for the order to become ready, then send a CSR (certificate
	// signing request) signed with a fresh P-256 key for the certificate.
	if o, err = c.WaitOrder(ctx, o.URI); err != nil {
		return Issued{}, fmt.Errorf("ACME order: %w", err)
	}
	ck, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Issued{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: names}, ck)
	if err != nil {
		return Issued{}, err
	}
	ders, _, err := c.CreateOrderCert(ctx, o.FinalizeURL, csr, true)
	if err != nil {
		return Issued{}, fmt.Errorf("ACME finalize: %w", err)
	}
	// 4. Return the chain and key as PEM, plus the expiry date of the leaf.
	var chain []byte
	for _, d := range ders {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	leaf, err := x509.ParseCertificate(ders[0])
	if err != nil {
		return Issued{}, err
	}
	kd, err := x509.MarshalECPrivateKey(ck)
	if err != nil {
		return Issued{}, err
	}
	return Issued{Fullchain: string(chain), Key: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})), NotAfter: leaf.NotAfter}, nil
}

// accountKey loads or creates dir/acme-account.pem (0600).
func (a *ACME) accountKey() (crypto.Signer, error) {
	p := filepath.Join(a.Dir, "acme-account.pem")
	if b, err := os.ReadFile(p); err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, errors.New("acme-account.pem: not PEM")
		}
		return x509.ParseECPrivateKey(blk.Bytes)
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

// ServerIPs are this host's public unicast addresses.
func ServerIPs() []net.IP {
	var out []net.IP
	as, _ := net.InterfaceAddrs()
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok && n.IP.IsGlobalUnicast() && !n.IP.IsPrivate() {
			out = append(out, n.IP)
		}
	}
	return out
}

// ipList formats addresses as "a, b, c" for error messages.
func ipList(l []net.IP) string {
	s := make([]string, len(l))
	for i, ip := range l {
		s[i] = ip.String()
	}
	return strings.Join(s, ", ")
}

// waitTXT waits until public resolvers see value at host (up to 3 minutes).
func waitTXT(ctx context.Context, host, value string) error {
	r := publicDNS
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if txt, err := r.LookupTXT(ctx, host); err == nil {
			for _, t := range txt {
				if t == value {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the DNS-01 record %s did not show up in 3 minutes", host)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
