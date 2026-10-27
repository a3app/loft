package web

// This file holds the Domains screen: DNS providers (accounts at
// Cloudflare, DigitalOcean or Namecheap, see dns.go), zones (the domains
// the panel manages, through a provider or "manual" = edited by hand at the
// registrar), their records, and TLS certificates (issued by acme.go or
// uploaded). Certificates for the node (loft-node, the server that clients
// connect to) and the panel are installed by the helper (loft-panel-adm, via admproto);
// extra ones are kept here for mail. It also keeps the panel's own A/AAAA
// records in step when the server's addresses change.

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/store"
)

// Domains: zones (through a DNS provider's API or by hand), their records,
// and certificates. The provider secrets and stored certificates live in
// domains.json in the panel's data directory (0600); the page never gets
// a secret back.

// dnsProv is one saved DNS provider account and its secret token.
type dnsProv struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // cloudflare | digitalocean | namecheap
	Token string `json:"token"`
	User  string `json:"user,omitempty"` // Namecheap API user
}

// ourRec marks a record the panel created in a zone, so the page can show
// it as the panel's and followIPs can update it. ID is the provider's
// record id (empty for old entries, then type+name identify it).
type ourRec struct {
	Type string `json:"type"`
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
	Auto bool   `json:"auto,omitempty"` // follows the server's addresses
}

// zoneRec is one zone on the Domains screen: who serves it, which records
// are the panel's, the hand-kept records of a manual zone, and the
// registration end date learnt from RDAP (the public whois successor).
type zoneRec struct {
	Name       string    `json:"name"`
	Provider   string    `json:"provider"` // provider id or "manual"
	Ours       []ourRec  `json:"ours,omitempty"`
	Manual     []Record  `json:"manual,omitempty"` // records kept by hand (manual zones)
	RegUntil   time.Time `json:"reg_until,omitzero"`
	RegChecked time.Time `json:"reg_checked,omitzero"`
}

// certRec is an extra certificate kept by the panel (today: for mail),
// with its PEM chain and key so it can be renewed and handed to the mail
// server. The node's and the panel's own certificates live in the settings
// instead.
type certRec struct {
	ID        string    `json:"id"`
	Names     []string  `json:"names"`
	Method    string    `json:"method"` // http-01 | dns-01 | upload
	Use       string    `json:"use"`    // node | panel | mail
	Until     time.Time `json:"until"`
	Issuer    string    `json:"issuer"`
	Fullchain string    `json:"fullchain"`
	Key       string    `json:"key"`
}

// domainsFile is the whole content of domains.json.
type domainsFile struct {
	Providers []dnsProv `json:"providers"`
	Zones     []zoneRec `json:"zones"`
	Certs     []certRec `json:"certs"`
	IPs       []string  `json:"ips,omitempty"` // the addresses auto records point at
}

// domains is the in-memory copy of domains.json. mu guards f; the file is
// read lazily on first use (load) and written after each change (save).
type domains struct {
	mu   sync.Mutex
	path string
	f    domainsFile
	read bool
	// provider replaces real APIs (tests)
	provider func(p dnsProv) DNSProvider
	tried    map[string]time.Time // renewals tried, so a failing one waits 12 h
}

// SetDomainsFile is where zones and certificates are kept.
func (s *Server) SetDomainsFile(dir string) { s.dom.path = filepath.Join(dir, "domains.json") }

// load reads domains.json once; later calls do nothing. A missing or broken
// file leaves the lists empty. The caller holds d.mu.
func (d *domains) load() {
	if d.read {
		return
	}
	d.read = true
	if d.path == "" {
		return
	}
	if b, err := os.ReadFile(d.path); err == nil {
		json.Unmarshal(b, &d.f)
	}
}

// save writes domains.json atomically with mode 0600 (it holds tokens and
// private keys). The caller holds d.mu.
func (d *domains) save() error {
	if d.path == "" {
		return nil
	}
	b, _ := json.MarshalIndent(d.f, "", "  ")
	return store.WriteAtomic(d.path, append(b, '\n'), 0o600)
}

// providerAPI builds the client for a saved provider. Namecheap also needs
// the server's public IPv4, which must be on the account's API allowlist.
// Tests replace the real APIs through s.dom.provider.
func (s *Server) providerAPI(p dnsProv) DNSProvider {
	if s.dom.provider != nil {
		return s.dom.provider(p)
	}
	switch p.Kind {
	case "cloudflare":
		return &Cloudflare{Token: p.Token, HTTP: s.HTTP}
	case "digitalocean":
		return &DigitalOcean{Token: p.Token, HTTP: s.HTTP}
	case "namecheap":
		ip := ""
		for _, x := range ServerIPs() {
			if x.To4() != nil {
				ip = x.String()
				break
			}
		}
		return &Namecheap{User: p.User, Key: p.Token, ClientIP: ip, HTTP: s.HTTP}
	}
	return nil
}

// zone finds a zone by exact name and returns a pointer into the list, or
// nil. The caller holds d.mu.
func (d *domains) zone(name string) *zoneRec {
	for i := range d.f.Zones {
		if d.f.Zones[i].Name == name {
			return &d.f.Zones[i]
		}
	}
	return nil
}

// prov finds a provider by id, or nil (also for "manual" zones).
func (d *domains) prov(id string) *dnsProv {
	for i := range d.f.Providers {
		if d.f.Providers[i].ID == id {
			return &d.f.Providers[i]
		}
	}
	return nil
}

// reconcileOurs checks the zone's list of the panel's own records against
// have, the provider's current records, and repairs ids that point at no
// record: such an entry takes the id of the one record of its type and
// name, or an empty id when there is none or several (the panel then goes by
// type and name, as for a record whose id it never learned). This repairs
// Namecheap ids saved by earlier versions, which were positions in the
// list, and any record recreated by hand. It reports whether it changed
// anything, so the caller can save.
func (z *zoneRec) reconcileOurs(have []Record) bool {
	ids := map[string]bool{}
	for _, r := range have {
		ids[r.ID] = true
	}
	changed := false
	for i, o := range z.Ours {
		if o.ID == "" || ids[o.ID] {
			continue
		}
		id := ""
		n := 0
		for _, r := range have {
			if r.Type == o.Type && strings.EqualFold(r.Name, o.Name) {
				id = r.ID
				n++
			}
		}
		if n != 1 {
			id = ""
		}
		z.Ours[i].ID = id
		changed = true
	}
	return changed
}

// remember records rec as one of the panel's own records in the zone,
// updating the existing entry when it is already known.
func (z *zoneRec) remember(rec Record, auto bool) {
	for i, o := range z.Ours {
		if o.ID == rec.ID || (o.ID == "" && o.Type == rec.Type && o.Name == rec.Name) {
			z.Ours[i].ID, z.Ours[i].Auto = rec.ID, auto
			return
		}
	}
	z.Ours = append(z.Ours, ourRec{Type: rec.Type, Name: rec.Name, ID: rec.ID, Auto: auto})
}

// zoneFor is the longest zone that name is in.
func (d *domains) zoneFor(name string) *zoneRec {
	name = strings.TrimPrefix(strings.ToLower(name), "*.")
	var best *zoneRec
	for i := range d.f.Zones {
		z := &d.f.Zones[i]
		if (name == z.Name || strings.HasSuffix(name, "."+z.Name)) && (best == nil || len(z.Name) > len(best.Name)) {
			best = z
		}
	}
	return best
}

// zoneRe matches a DNS host name with at least two labels, like example.com.
var zoneRe = regexp.MustCompile(`^(?i)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// provView is a provider as the page sees it: no token, only a hint.
type provView struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"`
	Hint  string   `json:"hint"` // a part of the token, to recognise it
	User  string   `json:"user,omitempty"`
	Zones []string `json:"zones"`
}

// zoneView is one zone in the zone list. Records is -1 for provider zones
// because counting them would need an API call per zone; the page asks
// for them when the zone is opened.
type zoneView struct {
	Name     string    `json:"name"`
	Provider string    `json:"provider"`
	Kind     string    `json:"kind"`
	Records  int       `json:"records"`
	RegUntil time.Time `json:"reg_until,omitzero"`
}

// certView is one certificate in the list, without its private key.
type certView struct {
	ID     string    `json:"id"`
	Names  []string  `json:"names"`
	Method string    `json:"method"`
	Use    string    `json:"use"`
	Until  time.Time `json:"until,omitzero"`
	Issuer string    `json:"issuer"`
	Key    string    `json:"key"`
	Fixed  bool      `json:"fixed,omitempty"` // the node's or the panel's: issued in the wizard
}

// tokenHint shows the first and last 4 characters of a token, enough to
// tell tokens apart without revealing them.
func tokenHint(t string) string {
	if len(t) < 8 {
		return "…"
	}
	return t[:4] + "…" + t[len(t)-4:]
}

// certInfo reads the first certificate of a PEM chain: expiry, issuer, key
// type and DNS names. All are zero when the PEM cannot be parsed.
func certInfo(fullchain string) (until time.Time, issuer, key string, names []string) {
	blk, _ := pem.Decode([]byte(fullchain))
	if blk == nil {
		return
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return
	}
	key = "RSA"
	if c.PublicKeyAlgorithm == x509.ECDSA {
		key = "ECDSA P-256"
	}
	return c.NotAfter, firstOf(strings.Join(c.Issuer.Organization, ", "), c.Issuer.CommonName), key, c.DNSNames
}

// domainsView builds the Domains page data: providers, zones, certificates
// (the node's and the panel's come from the settings, the extra ones from
// domains.json) and the server's public addresses.
func (s *Server) domainsView(ctx context.Context) map[string]any {
	st, _ := s.Settings.Get()
	s.dom.mu.Lock()
	s.dom.load()
	f := s.dom.f
	s.dom.mu.Unlock()
	provs := []provView{}
	for _, p := range f.Providers {
		pv := provView{ID: p.ID, Kind: p.Kind, Hint: tokenHint(p.Token), User: p.User, Zones: []string{}}
		for _, z := range f.Zones {
			if z.Provider == p.ID {
				pv.Zones = append(pv.Zones, z.Name)
			}
		}
		provs = append(provs, pv)
	}
	zones := []zoneView{}
	for _, z := range f.Zones {
		zv := zoneView{Name: z.Name, Provider: z.Provider, Kind: "manual", RegUntil: z.RegUntil, Records: len(z.Manual)}
		if p := s.dom.prov(z.Provider); p != nil {
			zv.Kind = p.Kind
			zv.Records = -1 // asked per zone
		}
		zones = append(zones, zv)
	}
	certs := []certView{}
	if st.Domain != "" {
		certs = append(certs, certView{ID: "node", Names: firstNames(st.NodeCertNames, st.Domain), Method: firstOf(st.NodeCertMethod, "http-01"), Use: "node", Until: st.CertUntil, Issuer: "Let’s Encrypt", Key: "ECDSA P-256", Fixed: true})
	}
	if st.PanelDomain != "" && st.PanelDomain != st.Domain {
		certs = append(certs, certView{ID: "panel", Names: firstNames(st.PanelCertNames, st.PanelDomain), Method: firstOf(st.PanelCertMethod, "http-01"), Use: "panel", Until: st.PanelCertUntil, Issuer: "Let’s Encrypt", Key: "ECDSA P-256", Fixed: true})
	}
	for _, c := range f.Certs {
		until, issuer, key, _ := certInfo(c.Fullchain)
		certs = append(certs, certView{ID: c.ID, Names: c.Names, Method: c.Method, Use: c.Use, Until: until, Issuer: issuer, Key: key})
	}
	return map[string]any{"providers": provs, "zones": zones, "certs": certs, "server_ips": append([]string{}, ipList2(ServerIPs())...), "node": st.Domain, "panel": firstOf(st.PanelDomain, st.Domain)}
}

// firstNames returns names, or just d when names is empty (settings saved
// before certificate names were recorded).
func firstNames(names []string, d string) []string {
	if len(names) > 0 {
		return names
	}
	return []string{d}
}

// domainsGet serves the Domains page data.
func (s *Server) domainsGet(w http.ResponseWriter, r *http.Request) {
	ok(w, s.domainsView(r.Context()))
}

// newID makes a short random id such as "p-1a2b3c4d" for saved items.
func newID(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// providerSave adds a DNS provider after checking its access.
func (s *Server) providerSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		Token     string `json:"token"`
		User      string `json:"user"`
		Delete    bool   `json:"delete"`
		CheckOnly bool   `json:"check_only"`
	}
	if !readJSON(w, r, 8<<10, &in) {
		return
	}
	auditTarget(r, in.Kind)
	s.dom.mu.Lock()
	defer s.dom.mu.Unlock()
	s.dom.load()
	if in.Delete {
		for _, z := range s.dom.f.Zones {
			if z.Provider == in.ID {
				fail(w, http.StatusConflict, "zone "+z.Name+" uses this provider: move or delete it first")
				return
			}
		}
		s.dom.f.Providers = slices.DeleteFunc(s.dom.f.Providers, func(p dnsProv) bool { return p.ID == in.ID })
		if err := s.dom.save(); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		ok(w, struct{}{})
		return
	}
	if !slices.Contains([]string{"cloudflare", "digitalocean", "namecheap"}, in.Kind) {
		fail(w, http.StatusBadRequest, "unknown DNS provider")
		return
	}
	p := dnsProv{ID: newID("p-"), Kind: in.Kind, Token: strings.TrimSpace(in.Token), User: strings.TrimSpace(in.User)}
	if p.Token == "" || (p.Kind == "namecheap" && p.User == "") {
		fail(w, http.StatusBadRequest, "the provider needs its token (and, for Namecheap, the API user)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.providerAPI(p).Check(ctx); err != nil {
		fail(w, http.StatusBadRequest, "access check failed: "+err.Error())
		return
	}
	if in.CheckOnly {
		ok(w, struct{}{})
		return
	}
	for _, x := range s.dom.f.Providers {
		if x.Kind == p.Kind && x.Token == p.Token && x.User == p.User {
			ok(w, provView{ID: x.ID, Kind: x.Kind, Hint: tokenHint(x.Token), User: x.User})
			return
		}
	}
	s.dom.f.Providers = append(s.dom.f.Providers, p)
	if err := s.dom.save(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, provView{ID: p.ID, Kind: p.Kind, Hint: tokenHint(p.Token), User: p.User})
}

// zoneSave adds, moves or deletes a zone. A new zone may get the server's
// A/AAAA records for its names and a CAA record for Let's Encrypt.
func (s *Server) zoneSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string   `json:"name"`
		Provider string   `json:"provider"`
		Delete   bool     `json:"delete"`
		Create   []string `json:"create"`          // "addr" (A/AAAA for the server's names in the zone), "caa"
		Hosts    []string `json:"hosts,omitempty"` // names to point at this server; empty = node and panel
	}
	if !readJSON(w, r, 8<<10, &in) {
		return
	}
	in.Name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in.Name)), ".")
	auditTarget(r, in.Name)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	s.dom.mu.Lock()
	defer s.dom.mu.Unlock()
	s.dom.load()
	if in.Delete {
		s.dom.f.Zones = slices.DeleteFunc(s.dom.f.Zones, func(z zoneRec) bool { return z.Name == in.Name })
		s.dom.save()
		ok(w, struct{}{})
		return
	}
	// 1. Check the name and, for a provider zone, that the provider really
	// serves it (by listing its records).
	if !zoneRe.MatchString(in.Name) {
		fail(w, http.StatusBadRequest, "zone: a domain like example.com")
		return
	}
	var api DNSProvider
	if in.Provider != "manual" {
		p := s.dom.prov(in.Provider)
		if p == nil {
			fail(w, http.StatusBadRequest, "unknown DNS provider")
			return
		}
		api = s.providerAPI(*p)
		if _, err := api.Records(ctx, in.Name); err != nil {
			fail(w, http.StatusBadRequest, "the provider does not serve "+in.Name+": "+err.Error())
			return
		}
	}
	// 2. Add the zone or move it to the new provider.
	z := s.dom.zone(in.Name)
	if z == nil {
		s.dom.f.Zones = append(s.dom.f.Zones, zoneRec{Name: in.Name})
		z = &s.dom.f.Zones[len(s.dom.f.Zones)-1]
	}
	z.Provider = in.Provider
	st, _ := s.Settings.Get()
	hosts := in.Hosts
	if len(hosts) == 0 {
		hosts = uniq([]string{st.Domain, st.PanelDomain})
	}
	// 3. Create the records asked for: A/AAAA for the server's names that
	// fall inside this zone (marked automatic, so followIPs keeps them up to
	// date), and a CAA record allowing only Let's Encrypt to issue.
	var want []Record
	if slices.Contains(in.Create, "addr") {
		for _, host := range uniq(hosts) {
			host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
			if host == in.Name || strings.HasSuffix(host, "."+in.Name) {
				want = append(want, addrRecords(relName(host, in.Name))...)
			}
		}
	}
	if slices.Contains(in.Create, "caa") {
		want = append(want, Record{Type: "CAA", Name: "@", Value: `0 issue "letsencrypt.org"`})
	}
	var errs []string
	for _, rec := range want {
		if err := s.putRecord(ctx, z, api, rec, rec.Type == "A" || rec.Type == "AAAA"); err != nil {
			errs = append(errs, rec.Type+" "+rec.Name+": "+err.Error())
		}
	}
	// 4. Save, and look up the registration end date in the background.
	s.dom.f.IPs = ipList2(ServerIPs())
	if err := s.dom.save(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	go s.refreshReg(context.Background(), in.Name)
	if len(errs) > 0 {
		fail(w, http.StatusBadGateway, "zone saved, but: "+strings.Join(errs, "; "))
		return
	}
	ok(w, struct{}{})
}

// uniq drops empty strings and duplicates, keeping the first order.
func uniq(l []string) []string {
	var out []string
	for _, x := range l {
		if x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// ipList2 formats addresses as strings (for JSON and comparisons).
func ipList2(ips []net.IP) []string {
	var out []string
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

// addrRecords are A and AAAA records of name for the server's addresses.
func addrRecords(name string) []Record {
	var out []Record
	for _, ip := range ServerIPs() {
		t := "AAAA"
		if ip.To4() != nil {
			t = "A"
		}
		out = append(out, Record{Type: t, Name: name, Value: ip.String()})
	}
	return out
}

// putRecord creates rec (or updates one of the same type and name the panel
// made) and remembers it as the panel's.
func (s *Server) putRecord(ctx context.Context, z *zoneRec, api DNSProvider, rec Record, auto bool) error {
	if api == nil { // manual: the operator creates it; the panel only lists and checks it
		z.Manual = slices.DeleteFunc(z.Manual, func(x Record) bool { return x.Type == rec.Type && x.Name == rec.Name && x.Value == rec.Value })
		rec.ID = newID("m-")
		z.Manual = append(z.Manual, rec)
		z.Ours = append(z.Ours, ourRec{Type: rec.Type, Name: rec.Name, ID: rec.ID, Auto: auto})
		return nil
	}
	id, err := api.Create(ctx, z.Name, rec)
	if err != nil {
		return err
	}
	z.Ours = append(z.Ours, ourRec{Type: rec.Type, Name: rec.Name, ID: id, Auto: auto})
	return nil
}

// recView is one record on the zone page, with flags for records the panel
// made (Ours) and that follow the server's addresses (Auto).
type recView struct {
	Record
	Ours bool `json:"ours,omitempty"`
	Auto bool `json:"auto,omitempty"`
	// manual zones: what DNS answers now
	Check string `json:"check,omitempty"` // ok | missing | differs
}

// zoneRecords lists a zone's records (from the provider, or the manual list).
func (s *Server) zoneRecords(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("zone")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	s.dom.mu.Lock()
	s.dom.load()
	z := s.dom.zone(name)
	if z == nil {
		s.dom.mu.Unlock()
		fail(w, http.StatusNotFound, "no such zone")
		return
	}
	zc := *z
	p := s.dom.prov(z.Provider)
	s.dom.mu.Unlock()
	var recs []Record
	if p != nil {
		var err error
		if recs, err = s.providerAPI(*p).Records(ctx, name); err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		s.dom.mu.Lock()
		if z := s.dom.zone(name); z != nil && z.reconcileOurs(recs) {
			zc.Ours = append([]ourRec(nil), z.Ours...)
			s.dom.save()
		}
		s.dom.mu.Unlock()
	} else {
		recs = zc.Manual
	}
	out := []recView{}
	for _, rec := range recs {
		v := recView{Record: rec}
		for _, o := range zc.Ours {
			if o.ID == rec.ID || (o.ID == "" && o.Type == rec.Type && o.Name == rec.Name) {
				v.Ours, v.Auto = true, o.Auto
			}
		}
		if p == nil && r.URL.Query().Get("check") == "1" {
			v.Check = checkRecord(ctx, name, rec)
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Type < out[j].Type
	})
	ok(w, map[string]any{"records": out})
}

// checkRecord asks DNS whether rec is published.
func checkRecord(ctx context.Context, zone string, rec Record) string {
	got, checked := lookupRecord(ctx, zone, rec)
	if !checked {
		return "" // CAA, SRV: not checked
	}
	if len(got) == 0 {
		return "missing"
	}
	if slices.Contains(got, strings.TrimSuffix(rec.Value, ".")) {
		return "ok"
	}
	return "differs"
}

// lookupRecord is what public DNS answers now for rec's name and type;
// checked is false for types the panel does not look up.
func lookupRecord(ctx context.Context, zone string, rec Record) (got []string, checked bool) {
	host := fqdn(rec.Name, zone)
	var err error
	rv := net.DefaultResolver
	switch rec.Type {
	case "A", "AAAA":
		var ips []net.IPAddr
		ips, err = rv.LookupIPAddr(ctx, host)
		for _, ip := range ips {
			got = append(got, ip.IP.String())
		}
	case "CNAME":
		var c string
		c, err = rv.LookupCNAME(ctx, host)
		got = []string{strings.TrimSuffix(c, ".")}
	case "MX":
		var mx []*net.MX
		mx, err = rv.LookupMX(ctx, host)
		for _, m := range mx {
			got = append(got, strings.TrimSuffix(m.Host, "."))
		}
	case "TXT":
		got, err = rv.LookupTXT(ctx, host)
	case "NS":
		var ns []*net.NS
		ns, err = rv.LookupNS(ctx, host)
		for _, n := range ns {
			got = append(got, strings.TrimSuffix(n.Host, "."))
		}
	default:
		return nil, false
	}
	if err != nil {
		return nil, true
	}
	return got, true
}

var labelRe = regexp.MustCompile(`^(@|\*|(\*\.)?([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?)(\.[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?)*)$`)

// recordSave creates, changes or deletes one record.
func (s *Server) recordSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Zone   string `json:"zone"`
		Record Record `json:"record"`
		Auto   bool   `json:"auto"`
		Delete bool   `json:"delete"`
	}
	if !readJSON(w, r, 16<<10, &in) {
		return
	}
	rec := in.Record
	rec.Name = strings.ToLower(strings.TrimSpace(firstOf(rec.Name, "@")))
	rec.Value = strings.TrimSpace(rec.Value)
	auditTarget(r, rec.Type+" "+fqdn(rec.Name, in.Zone))
	if !in.Delete {
		if !slices.Contains(RecordTypes, rec.Type) {
			fail(w, http.StatusBadRequest, "record type: A, AAAA, CNAME, MX, TXT, CAA, SRV or NS")
			return
		}
		if !labelRe.MatchString(rec.Name) {
			fail(w, http.StatusBadRequest, "record name: @ or a name inside the zone")
			return
		}
		if err := checkValue(rec); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	s.dom.mu.Lock()
	defer s.dom.mu.Unlock()
	s.dom.load()
	z := s.dom.zone(in.Zone)
	if z == nil {
		fail(w, http.StatusNotFound, "no such zone")
		return
	}
	var api DNSProvider
	if p := s.dom.prov(z.Provider); p != nil {
		api = s.providerAPI(*p)
	}
	var err error
	switch {
	case in.Delete:
		if api != nil {
			err = api.Delete(ctx, z.Name, rec.ID)
		} else {
			z.Manual = slices.DeleteFunc(z.Manual, func(x Record) bool { return x.ID == rec.ID })
		}
		z.Ours = slices.DeleteFunc(z.Ours, func(o ourRec) bool { return o.ID == rec.ID })
	case rec.ID == "":
		err = s.putRecord(ctx, z, api, rec, in.Auto && (rec.Type == "A" || rec.Type == "AAAA"))
	default:
		if api != nil {
			err = api.Update(ctx, z.Name, rec)
		} else {
			for i := range z.Manual {
				if z.Manual[i].ID == rec.ID {
					z.Manual[i] = rec
				}
			}
		}
		for i := range z.Ours {
			if z.Ours[i].ID == rec.ID {
				z.Ours[i].Auto = in.Auto && (rec.Type == "A" || rec.Type == "AAAA")
			}
		}
	}
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.dom.save(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, struct{}{})
}

// checkValue checks that a record's value fits its type (an IPv4 for A, a
// host name for CNAME, and so on) before it is sent to a provider.
func checkValue(r Record) error {
	v := r.Value
	switch r.Type {
	case "A":
		if ip := net.ParseIP(v); ip == nil || ip.To4() == nil {
			return errors.New("A: an IPv4 address")
		}
	case "AAAA":
		if ip := net.ParseIP(v); ip == nil || ip.To4() != nil {
			return errors.New("AAAA: an IPv6 address")
		}
	case "CNAME", "NS":
		if !zoneRe.MatchString(strings.TrimSuffix(v, ".")) {
			return fmt.Errorf("%s: a host name", r.Type)
		}
	case "MX":
		if !zoneRe.MatchString(strings.TrimSuffix(v, ".")) || r.Priority < 0 || r.Priority > 65535 {
			return errors.New("MX: a mail host and a priority 0–65535")
		}
	case "TXT":
		if v == "" || len(v) > 2048 {
			return errors.New("TXT: 1–2048 characters")
		}
	case "CAA":
		if len(strings.SplitN(v, " ", 3)) != 3 {
			return errors.New(`CAA: like 0 issue "letsencrypt.org"`)
		}
	case "SRV":
		if len(strings.Fields(v)) != 3 {
			return errors.New("SRV: weight port target")
		}
	}
	return nil
}

// followIPs points the panel's automatic records at the server's addresses
// when they change.
func (s *Server) followIPs(ctx context.Context) {
	ips := ipList2(ServerIPs())
	if len(ips) == 0 {
		return
	}
	s.dom.mu.Lock()
	defer s.dom.mu.Unlock()
	s.dom.load()
	if len(s.dom.f.IPs) == 0 || slices.Equal(s.dom.f.IPs, ips) {
		if len(s.dom.f.IPs) == 0 {
			s.dom.f.IPs = ips
			s.dom.save()
		}
		return
	}
	// The addresses changed: in every provider zone, delete the automatic
	// records and create new ones for the same names with the new addresses.
	// Manual zones are skipped; the admin edits those at the registrar.
	s.logf("server addresses changed %v → %v: updating the panel's DNS records", s.dom.f.IPs, ips)
	s.sysEvent("dns-ips", strings.Join(ips, ", "))
	for i := range s.dom.f.Zones {
		z := &s.dom.f.Zones[i]
		p := s.dom.prov(z.Provider)
		if p == nil {
			continue
		}
		api := s.providerAPI(*p)
		// Make sure every id still names the record it was saved for: a
		// stale one could otherwise delete someone else's record here.
		have, err := api.Records(ctx, z.Name)
		if err != nil {
			s.logf("DNS %s: %v: its records keep the old addresses", z.Name, err)
			continue
		}
		z.reconcileOurs(have)
		names := map[string]bool{}
		var keep []ourRec
		for _, o := range z.Ours {
			if o.Auto {
				names[o.Name] = true
				if o.ID != "" {
					api.Delete(ctx, z.Name, o.ID)
				}
				continue
			}
			keep = append(keep, o)
		}
		z.Ours = keep
		for n := range names {
			for _, rec := range addrRecords(n) {
				if err := s.putRecord(ctx, z, api, rec, true); err != nil {
					s.logf("DNS %s %s: %v", rec.Type, fqdn(n, z.Name), err)
				}
			}
		}
	}
	s.dom.f.IPs = ips
	s.dom.save()
}

// refreshReg asks RDAP when the zone's registration ends.
func (s *Server) refreshReg(ctx context.Context, zone string) {
	until := s.rdapUntil(ctx, zone)
	s.dom.mu.Lock()
	if z := s.dom.zone(zone); z != nil {
		z.RegChecked = time.Now().UTC()
		if !until.IsZero() {
			z.RegUntil = until
		}
		s.dom.save()
	}
	s.dom.mu.Unlock()
}

// rdapUntil is when zone's registration ends, by RDAP; zero if unknown.
func (s *Server) rdapUntil(ctx context.Context, zone string) time.Time {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://rdap.org/domain/"+zone, nil)
	req.Header.Set("Accept", "application/rdap+json")
	c := s.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	var until time.Time
	if resp, err := c.Do(req); err == nil {
		var d struct {
			Events []struct {
				Action string    `json:"eventAction"`
				Date   time.Time `json:"eventDate"`
			} `json:"events"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d)
		resp.Body.Close()
		for _, e := range d.Events {
			if e.Action == "expiration" {
				until = e.Date
			}
		}
	}
	return until
}

// watchDomains runs from Watch: addresses, registrations, extra certificates.
func (s *Server) watchDomains(ctx context.Context) {
	s.followIPs(ctx)
	s.dom.mu.Lock()
	s.dom.load()
	var stale []string
	for _, z := range s.dom.f.Zones {
		if time.Since(z.RegChecked) > 24*time.Hour {
			stale = append(stale, z.Name)
		}
	}
	// Extra certificates (not uploaded ones) are renewed when less than 30
	// days are left; a failed try waits 12 hours before the next one.
	var due []certRec
	if s.dom.tried == nil {
		s.dom.tried = map[string]time.Time{}
	}
	for _, c := range s.dom.f.Certs {
		if c.Method != "upload" && time.Since(s.dom.tried[c.ID]) > 12*time.Hour {
			if until, _, _, _ := certInfo(c.Fullchain); time.Until(until) < 30*24*time.Hour {
				due = append(due, c)
				s.dom.tried[c.ID] = time.Now()
			}
		}
	}
	s.dom.mu.Unlock()
	for _, z := range stale {
		s.refreshReg(ctx, z)
	}
	st, _ := s.Settings.Get()
	for _, c := range due {
		if _, err := s.issueCert(ctx, "renewal", c.Names, c.Method, c.Use, st.ACMEEmail, c.ID); err != nil {
			s.logf("certificate renewal for %s failed: %v", strings.Join(c.Names, ", "), err)
			s.sysEvent("cert-failed", strings.Join(c.Names, ", "))
		}
	}
}

// dns01For publishes ACME TXT records through the zone's provider.
func (s *Server) dns01For(names []string) (DNS01, error) {
	s.dom.mu.Lock()
	defer s.dom.mu.Unlock()
	s.dom.load()
	for _, n := range names {
		z := s.dom.zoneFor(n)
		if z == nil {
			return nil, fmt.Errorf("%s is in no zone on the Domains screen: add its zone with a DNS provider", n)
		}
		if s.dom.prov(z.Provider) == nil {
			return nil, fmt.Errorf("zone %s is managed by hand: DNS-01 needs a DNS provider", z.Name)
		}
	}
	return func(ctx context.Context, host, value string) (func(), error) {
		s.dom.mu.Lock()
		z := s.dom.zoneFor(strings.TrimPrefix(host, "_acme-challenge."))
		p := s.dom.prov(z.Provider)
		s.dom.mu.Unlock()
		api := s.providerAPI(*p)
		id, err := api.Create(ctx, z.Name, Record{Type: "TXT", Name: relName(host, z.Name), Value: value, TTL: 60})
		if err != nil {
			return nil, err
		}
		return func() { api.Delete(context.Background(), z.Name, id) }, nil
	}, nil
}

// issueCert gets a certificate for names and puts it to use: the node's or
// the panel's (installed by the helper), or kept for mail.
func (s *Server) issueCert(ctx context.Context, by string, names []string, method, use, email, id string) (time.Time, error) {
	if s.ACME == nil {
		return time.Time{}, errors.New("certificates are not configured")
	}
	var dns DNS01
	if method == "dns-01" {
		var err error
		if dns, err = s.dns01For(names); err != nil {
			return time.Time{}, err
		}
	} else {
		for _, n := range names {
			if strings.HasPrefix(n, "*.") {
				return time.Time{}, errors.New("a *. name needs DNS-01")
			}
			if err := s.ACME.Preflight(ctx, n); err != nil {
				return time.Time{}, err
			}
		}
	}
	is, err := s.ACME.IssueNames(ctx, names, email, dns)
	if err != nil {
		return time.Time{}, err
	}
	return is.NotAfter, s.useCert(ctx, by, names, method, use, id, is.Fullchain, is.Key, is.NotAfter)
}

// useCert installs or keeps a certificate.
func (s *Server) useCert(ctx context.Context, by string, names []string, method, use, id, fullchain, key string, until time.Time) error {
	st, _ := s.Settings.Get()
	switch use {
	case "node", "panel":
		need := st.Domain
		if use == "panel" {
			need = firstOf(st.PanelDomain, st.Domain)
		}
		if !nameCovers(names, need) {
			return fmt.Errorf("the certificate must cover %s", need)
		}
		if err := s.Adm(by).Call(ctx, admproto.OpCertInstall, admproto.CertInstall{Fullchain: fullchain, Key: key, Target: use}, nil); err != nil {
			return fmt.Errorf("install: %w", admErr(err))
		}
		s.Settings.Update(func(x *store.Settings) {
			if use == "node" {
				x.CertUntil, x.NodeCertNames, x.NodeCertMethod = until, names, method
			} else {
				x.PanelCertUntil, x.PanelCertNames, x.PanelCertMethod = until, names, method
			}
		})
	case "mail":
		s.dom.mu.Lock()
		s.dom.load()
		if id == "" {
			id = newID("c-")
		}
		i := slices.IndexFunc(s.dom.f.Certs, func(c certRec) bool { return c.ID == id })
		c := certRec{ID: id, Names: names, Method: method, Use: use, Until: until, Fullchain: fullchain, Key: key}
		if i >= 0 {
			s.dom.f.Certs[i] = c
		} else {
			s.dom.f.Certs = append(s.dom.f.Certs, c)
		}
		err := s.dom.save()
		s.dom.mu.Unlock()
		if err != nil {
			return err
		}
		s.syncMailCerts(ctx, by, nil)
	default:
		return fmt.Errorf("unknown use %q", use)
	}
	s.logf("certificate for %s (%s) until %s", strings.Join(names, ", "), use, until.Format(time.DateOnly))
	if by == "renewal" {
		s.sysEvent("cert", strings.Join(names, ", "))
	}
	return nil
}

// nameCovers reports whether a certificate for names is valid for host,
// either by exact name or by a wildcard "*.zone" one level above host.
func nameCovers(names []string, host string) bool {
	for _, n := range names {
		if n == host || (strings.HasPrefix(n, "*.") && strings.Count(host, ".") == strings.Count(n, ".") && strings.HasSuffix(host, n[1:])) {
			return true
		}
	}
	return false
}

// cleanNames normalises the names typed for a certificate (lower case, no
// trailing dot, no duplicates; "*." allowed) and allows 1 to 50 of them.
func cleanNames(in []string) ([]string, error) {
	var out []string
	for _, n := range in {
		n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
		if n == "" {
			continue
		}
		if !zoneRe.MatchString(strings.TrimPrefix(n, "*.")) {
			return nil, fmt.Errorf("%q is not a DNS name", n)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if len(out) == 0 || len(out) > 50 {
		return nil, errors.New("1–50 names")
	}
	return out, nil
}

// certIssueAny issues a certificate from the Domains screen (a job: the
// node may restart).
func (s *Server) certIssueAny(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Names  []string `json:"names"`
		Method string   `json:"method"`
		Use    string   `json:"use"`
		ID     string   `json:"id"`
	}
	if !readJSON(w, r, 8<<10, &in) {
		return
	}
	names, err := cleanNames(in.Names)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !slices.Contains([]string{"http-01", "dns-01"}, in.Method) || !slices.Contains([]string{"node", "panel", "mail"}, in.Use) {
		fail(w, http.StatusBadRequest, "check: http-01 or dns-01; use: node, panel or mail")
		return
	}
	auditTarget(r, strings.Join(names, ", "))
	st, _ := s.Settings.Get()
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		until, err := s.issueCert(ctx, by, names, in.Method, in.Use, st.ACMEEmail, in.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"until": until}, nil
	})
}

// certUpload installs a certificate from elsewhere (PEM).
func (s *Server) certUpload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fullchain string `json:"fullchain"`
		Key       string `json:"key"`
		Use       string `json:"use"`
	}
	if !readJSON(w, r, 256<<10, &in) {
		return
	}
	until, _, _, names := certInfo(in.Fullchain)
	if until.IsZero() || len(names) == 0 {
		fail(w, http.StatusBadRequest, "the certificate is not PEM with DNS names")
		return
	}
	if time.Until(until) < 0 {
		fail(w, http.StatusBadRequest, "the certificate has expired")
		return
	}
	blk, _ := pem.Decode([]byte(in.Key))
	if blk == nil || !strings.Contains(blk.Type, "PRIVATE KEY") {
		fail(w, http.StatusBadRequest, "the key is not a PEM private key")
		return
	}
	if !slices.Contains([]string{"node", "panel", "mail"}, in.Use) {
		fail(w, http.StatusBadRequest, "use: node, panel or mail")
		return
	}
	auditTarget(r, strings.Join(names, ", "))
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		return nil, s.useCert(ctx, by, names, "upload", in.Use, "", in.Fullchain, in.Key, until)
	})
}

// certDelete forgets an extra certificate and refreshes the mail server's
// certificate list.
func (s *Server) certDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	auditTarget(r, in.ID)
	s.dom.mu.Lock()
	defer s.dom.mu.Unlock()
	s.dom.load()
	s.dom.f.Certs = slices.DeleteFunc(s.dom.f.Certs, func(c certRec) bool { return c.ID == in.ID })
	if err := s.dom.save(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	go s.syncMailCerts(context.Background(), userOf(r).Login, nil)
	ok(w, struct{}{})
}

// providerZones lists the zones a provider serves, for the zone dialog.
func (s *Server) providerZones(w http.ResponseWriter, r *http.Request) {
	s.dom.mu.Lock()
	s.dom.load()
	p := s.dom.prov(r.URL.Query().Get("id"))
	s.dom.mu.Unlock()
	if p == nil {
		fail(w, http.StatusNotFound, "unknown DNS provider")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	z, err := s.providerAPI(*p).Zones(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	ok(w, map[string]any{"zones": z})
}
