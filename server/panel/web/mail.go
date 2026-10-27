package web

// mail.go is the panel's Mail screen: mail domains, mailboxes and aliases
// for the server's own mail system (Postfix sends and receives, Dovecot
// keeps the boxes). panel-adm, the root helper reached through admproto,
// keeps mail.json and writes the mail daemons' files. The panel side does
// the parts that need no root: it hashes mailbox passwords, checks the
// domain's DNS records, creates missing ones through the zone's DNS
// provider (see domains.go and dns.go), and hands the helper the
// certificates of mail.<domain> issued on the Domains screen.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/auth"
)

// mailCache is the panel's in-memory mail state: the domains seen last (so
// certificates can be synced without asking the helper again), a checksum
// of the certificates the helper already has, and recent statistics.
type mailCache struct {
	mu      sync.Mutex
	domains []string // the last mail domains seen, for the certificate sync
	certSum string   // what the helper last got
	stats   admproto.MailStats
	statsAt time.Time
}

// mailHash is a Dovecot {ARGON2ID} hash. p=1: libsodium, which verifies
// it, takes no other parallelism. 19 MiB, t=2 is OWASP's floor: IMAP
// clients log in often, and every login pays it.
func mailHash(pw string) (string, error) {
	if len([]rune(pw)) < auth.MinPassword {
		return "", fmt.Errorf("password: at least %d characters", auth.MinPassword)
	}
	if strings.ContainsAny(pw, "\n\r:") {
		return "", errors.New("password: no line breaks or colons")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	key := argon2.IDKey([]byte(pw), salt, 2, 19456, 1, 32)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("{ARGON2ID}$argon2id$v=%d$m=19456,t=2,p=1$%s$%s", argon2.Version, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// mailJSON is the Mail screen's data: the helper's view plus the panel
// logins (to pick a box owner) and the zones known on the Domains screen.
type mailJSON struct {
	admproto.MailView
	Users []string `json:"users"` // panel logins, for the owner list
	Zones []string `json:"zones"` // zones on the Domains screen
	// After a domain removal: DNS records deleted, and what was left and why.
	DNSRemoved int    `json:"dns_removed,omitempty"`
	Note       string `json:"note,omitempty"`
}

// mailRemember stores the current list of mail domain names for later
// certificate syncs.
func (s *Server) mailRemember(v admproto.MailView) {
	var ds []string
	for _, d := range v.Domains {
		ds = append(ds, d.Name)
	}
	s.mail.mu.Lock()
	s.mail.domains = ds
	s.mail.mu.Unlock()
}

// mailOut replies with the mail view, adding panel logins and zone names.
func (s *Server) mailOut(w http.ResponseWriter, v admproto.MailView) {
	s.mailOutWith(w, v, 0, "")
}

// mailOutWith is mailOut after a domain was removed: dnsRemoved is how many
// of its DNS records went, note what was left behind and why.
func (s *Server) mailOutWith(w http.ResponseWriter, v admproto.MailView, dnsRemoved int, note string) {
	s.mailRemember(v)
	out := mailJSON{MailView: v, Users: []string{}, Zones: []string{}, DNSRemoved: dnsRemoved, Note: note}
	for _, u := range s.Store.List() {
		out.Users = append(out.Users, u.Login)
	}
	s.dom.mu.Lock()
	s.dom.load()
	for _, z := range s.dom.f.Zones {
		out.Zones = append(out.Zones, z.Name)
	}
	s.dom.mu.Unlock()
	ok(w, out)
}

// mailGet returns the mail state. If certificates changed since the helper
// last got them, it sends them first and reads the state again, so the
// screen shows them as installed.
func (s *Server) mailGet(w http.ResponseWriter, r *http.Request) {
	var v admproto.MailView
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpMail, nil, &v); err != nil {
		admFail(w, err)
		return
	}
	if changed := s.syncMailCerts(r.Context(), userOf(r).Login, v.Domains); changed {
		s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpMail, nil, &v)
	}
	s.mailOut(w, v)
}

// mailSave runs a save and answers with the new state. It is shared by the
// domain, box, alias, HELO and apply handlers; target names the change in the
// audit log. Certificates are synced afterwards, since a new domain may now
// have one.
func (s *Server) mailSave(w http.ResponseWriter, r *http.Request, op string, body any, target string) {
	auditTarget(r, target)
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	adm := s.Adm(userOf(r).Login)
	var v admproto.MailView
	if err := adm.Call(ctx, op, body, &v); err != nil {
		admFail(w, err)
		return
	}
	if s.syncMailCerts(ctx, userOf(r).Login, v.Domains) {
		adm.Call(ctx, admproto.OpMail, nil, &v)
	}
	s.mailOut(w, v)
}

// mailDomain adds, changes or removes a mail domain. Removing it can also
// clean up after the domain outside the mail server: with drop_dns the DNS
// records the panel itself made for it are deleted at the zone's provider,
// and its mail.<domain> certificate is always forgotten (mailForget).
func (s *Server) mailDomain(w http.ResponseWriter, r *http.Request) {
	var in struct {
		admproto.MailDomainSave
		DropDNS bool `json:"drop_dns"`
	}
	if !readJSON(w, r, 16<<10, &in) {
		return
	}
	// The helper refuses unknown fields, so it gets the save without drop_dns.
	if !in.Delete {
		s.mailSave(w, r, admproto.OpMailDomain, in.MailDomainSave, in.Name)
		return
	}
	auditTarget(r, in.Name)
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	by := userOf(r).Login
	adm := s.Adm(by)
	var v admproto.MailView
	if err := adm.Call(ctx, admproto.OpMailDomain, in.MailDomainSave, &v); err != nil {
		admFail(w, err)
		return
	}
	// The domain is gone from the mail server; what follows only tidies up,
	// so a failure there is reported next to the result, not as an error.
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in.Name)), ".")
	removed, note := s.mailForget(ctx, name, v.Helo, in.DropDNS)
	if s.syncMailCerts(ctx, by, v.Domains) {
		adm.Call(ctx, admproto.OpMail, nil, &v)
	}
	s.mailOutWith(w, v, removed, note)
}

// mailForget removes what the panel keeps for a deleted mail domain d: the
// certificate issued for mail.d alone for mail, and, when dropDNS is set,
// the DNS records the panel created for the domain (mailOwnRec) in the zone
// that holds d. Records someone added by hand are never touched: only those
// in the zone's list of the panel's own records, and only when the
// provider's current copy still looks like that mail record. It returns how
// many records it deleted and, when something was left, a note why.
func (s *Server) mailForget(ctx context.Context, d, helo string, dropDNS bool) (int, string) {
	s.dom.mu.Lock()
	defer s.dom.mu.Unlock()
	s.dom.load()
	host := "mail." + d
	s.dom.f.Certs = slices.DeleteFunc(s.dom.f.Certs, func(c certRec) bool {
		return c.Use == "mail" && len(c.Names) == 1 && c.Names[0] == host
	})
	removed, note := 0, ""
	if dropDNS {
		removed, note = s.mailDropDNS(ctx, d, helo)
	}
	if err := s.dom.save(); err != nil && note == "" {
		note = err.Error()
	}
	return removed, note
}

// mailDropDNS deletes the panel's own mail records of domain d (see
// mailForget). s.dom.mu must be held.
func (s *Server) mailDropDNS(ctx context.Context, d, helo string) (int, string) {
	z := s.dom.zoneFor(d)
	if z == nil {
		return 0, ""
	}
	p := s.dom.prov(z.Provider)
	if p == nil {
		return 0, "zone " + z.Name + " is managed by hand: remove the mail records there"
	}
	api := s.providerAPI(*p)
	have, err := api.Records(ctx, z.Name)
	if err != nil {
		return 0, "DNS records not removed: " + err.Error()
	}
	z.reconcileOurs(have)
	byID := map[string]Record{}
	for _, rec := range have {
		byID[rec.ID] = rec
	}
	// Pick the targets. An entry whose record is no longer at the provider is
	// dropped from the list; one whose record now looks different (the zone
	// was edited by hand since) is left alone, record and entry both.
	var del []Record
	skipped := 0
	keep := z.Ours[:0:0]
	for _, o := range z.Ours {
		if !mailOwnRec(o.Type, fqdn(o.Name, z.Name), "", d, helo, false) {
			keep = append(keep, o)
			continue
		}
		rec, ok := byID[o.ID]
		switch {
		case o.ID == "":
			// Not found unambiguously (reconcileOurs): leave it be.
			skipped++
			keep = append(keep, o)
		case !ok:
		case rec.Type == o.Type && rec.Name == o.Name && mailOwnRec(rec.Type, fqdn(rec.Name, z.Name), rec.Value, d, helo, true):
			del = append(del, rec)
		default:
			skipped++
			keep = append(keep, o)
		}
	}
	removed := 0
	note := ""
	for _, rec := range del {
		if err := api.Delete(ctx, z.Name, rec.ID); err != nil {
			note = fmt.Sprintf("%s %s: %v", rec.Type, fqdn(rec.Name, z.Name), err)
			// Not deleted: it stays one of the panel's records.
			keep = append(keep, ourRec{Type: rec.Type, Name: rec.Name, ID: rec.ID})
			continue
		}
		removed++
	}
	z.Ours = keep
	if note == "" && skipped > 0 {
		note = fmt.Sprintf("%d DNS record(s) changed by hand since the panel made them were left in place", skipped)
	}
	return removed, note
}

// mailOwnRec reports whether a record (type, full name, value) is one of the
// records the panel writes for mail domain d (mailNeeds): MX at d, A/AAAA at
// mail.d, SPF at d and at the HELO name when that name is under d, DKIM at
// any selector of d, DMARC at d. With checkValue false only type and name
// count (the value is not known); with it, a TXT must also start like the
// panel's record.
func mailOwnRec(typ, name, value, d, helo string, checkValue bool) bool {
	host := "mail." + d
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	helo = strings.TrimSuffix(strings.ToLower(helo), ".")
	v := strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"`))
	txt := func(prefix string) bool { return !checkValue || strings.HasPrefix(v, prefix) }
	switch {
	case typ == "MX":
		return name == d
	case typ == "A" || typ == "AAAA":
		return name == host
	case typ != "TXT":
		return false
	case name == d || name == host || (helo != "" && name == helo && nameUnder(helo, d)):
		return txt("v=spf1")
	case strings.HasSuffix(name, "._domainkey."+d):
		return txt("v=DKIM1")
	case name == "_dmarc."+d:
		return txt("v=DMARC1")
	}
	return false
}

// nameUnder reports whether name is d or a name under d.
func nameUnder(name, d string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	d = strings.TrimSuffix(strings.ToLower(d), ".")
	return name == d || strings.HasSuffix(name, "."+d)
}

// mailBox adds, changes or deletes a mailbox. A new password is hashed here,
// so the helper never sees it in plain text; an empty password keeps the old
// one. The owner, if set, must be an existing panel login.
func (s *Server) mailBox(w http.ResponseWriter, r *http.Request) {
	var in struct {
		admproto.MailBox
		Password string `json:"password"`
		Delete   bool   `json:"delete"`
		DropMail bool   `json:"drop_mail"`
	}
	if !readJSON(w, r, 16<<10, &in) {
		return
	}
	req := admproto.MailBoxSave{MailBox: in.MailBox, Delete: in.Delete, DropMail: in.DropMail}
	req.Hash = ""
	if in.Password != "" && !in.Delete {
		h, err := mailHash(in.Password)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		req.Hash = h
	}
	if req.Owner != "" {
		if _, err := s.Store.Get(req.Owner); err != nil {
			fail(w, http.StatusBadRequest, "no panel user "+req.Owner)
			return
		}
	}
	s.mailSave(w, r, admproto.OpMailBox, req, req.Address)
}

// mailAlias adds, changes or deletes an alias (an address that forwards to
// other addresses).
func (s *Server) mailAlias(w http.ResponseWriter, r *http.Request) {
	var in admproto.MailAliasSave
	if !readJSON(w, r, 64<<10, &in) {
		return
	}
	s.mailSave(w, r, admproto.OpMailAlias, in, in.From)
}

// mailHelo sets the name Postfix greets with (EHLO). Empty returns to
// mail.<first enabled domain>.
func (s *Server) mailHelo(w http.ResponseWriter, r *http.Request) {
	var in admproto.MailHeloSave
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	s.mailSave(w, r, admproto.OpMailHelo, in, in.Helo)
}

// mailWebmail returns a short-lived Roundcube URL already signed in as the
// mailbox. The helper mints it; the panel never sees the mailbox password.
func (s *Server) mailWebmail(w http.ResponseWriter, r *http.Request) {
	var in admproto.MailWebmail
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	auditTarget(r, in.Address)
	var out admproto.MailWebmailURL
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpMailWebmail, in, &out); err != nil {
		admFail(w, err)
		return
	}
	ok(w, out)
}

// mailApply asks the helper to write all mail files again from mail.json.
func (s *Server) mailApply(w http.ResponseWriter, r *http.Request) {
	s.mailSave(w, r, admproto.OpMailApply, nil, "")
}

// mailStats returns queue and traffic counters, asking the helper at most
// once a minute; the rest of the time it answers from the cache.
func (s *Server) mailStats(w http.ResponseWriter, r *http.Request) {
	s.mail.mu.Lock()
	st, at := s.mail.stats, s.mail.statsAt
	s.mail.mu.Unlock()
	if time.Since(at) > time.Minute {
		if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpMailStats, nil, &st); err != nil {
			admFail(w, err)
			return
		}
		s.mail.mu.Lock()
		s.mail.stats, s.mail.statsAt = st, time.Now()
		s.mail.mu.Unlock()
	}
	ok(w, st)
}

// mailCertsFor picks, for every mail.<domain>, the certificate from the
// Domains screen that covers it and lasts longest.
func (s *Server) mailCertsFor(domains []string) []admproto.MailCert {
	s.dom.mu.Lock()
	s.dom.load()
	defer s.dom.mu.Unlock()
	var out []admproto.MailCert
	for _, d := range domains {
		host := "mail." + d
		var best *certRec
		for i, c := range s.dom.f.Certs {
			if c.Use != "mail" || !nameCovers(c.Names, host) || time.Until(c.Until) < 0 {
				continue
			}
			if best == nil || c.Until.After(best.Until) {
				best = &s.dom.f.Certs[i]
			}
		}
		if best != nil {
			out = append(out, admproto.MailCert{Host: host, Fullchain: best.Fullchain, Key: best.Key})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// syncMailCerts gives the helper the current certificates when they differ
// from what it got last; true when it was called.
func (s *Server) syncMailCerts(ctx context.Context, by string, ds []admproto.MailDomain) bool {
	var names []string
	for _, d := range ds {
		names = append(names, d.Name)
	}
	if ds == nil {
		s.mail.mu.Lock()
		names = s.mail.domains
		s.mail.mu.Unlock()
		if names == nil {
			return false
		}
	}
	certs := s.mailCertsFor(names)
	h := sha256.New()
	for _, c := range certs {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", c.Host, c.Fullchain, c.Key)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	// What the helper has may differ from the last sum (a restart of the
	// panel): installed names without a certificate here also count.
	have := 0
	for _, d := range ds {
		if d.Cert {
			have++
		}
	}
	s.mail.mu.Lock()
	same := s.mail.certSum == sum && (ds == nil || have == len(certs))
	s.mail.mu.Unlock()
	if same {
		return false
	}
	if err := s.Adm(by).Call(ctx, admproto.OpMailCerts, admproto.MailCerts{Certs: certs}, nil); err != nil {
		s.logf("mail certificates: %v", err)
		return false
	}
	s.mail.mu.Lock()
	s.mail.certSum = sum
	s.mail.mu.Unlock()
	return true
}

// mailCert issues a certificate for mail.<domain> (a job): DNS-01 when the
// zone has a provider, else HTTP-01 through port 80.
func (s *Server) mailCert(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Domain string `json:"domain"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	d := strings.ToLower(strings.TrimSpace(in.Domain))
	if !zoneRe.MatchString(d) {
		fail(w, http.StatusBadRequest, "bad domain")
		return
	}
	host := "mail." + d
	method := "http-01"
	s.dom.mu.Lock()
	s.dom.load()
	if z := s.dom.zoneFor(host); z != nil && s.dom.prov(z.Provider) != nil {
		method = "dns-01"
	}
	s.dom.mu.Unlock()
	auditTarget(r, host)
	st, _ := s.Settings.Get()
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		until, err := s.issueCert(ctx, by, []string{host}, method, "mail", st.ACMEEmail, "")
		if err != nil {
			return nil, err
		}
		return map[string]any{"until": until}, nil
	})
}

/* ── DNS ── */

// mailRec is one record a mail domain needs, and what DNS says now.
type mailRec struct {
	Key   string `json:"key"` // mx | a | spf | helo | dkim | dmarc | ptr
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	Prio  int    `json:"prio,omitempty"`
	State string `json:"state"`          // ok | missing | differs
	Now   string `json:"now,omitempty"`  // what is there when it differs
	Auto  bool   `json:"auto,omitempty"` // the panel can create it
}

// mailNeeds lists the records of domain d. helo is the name Postfix greets
// with; its SPF is listed when that name is under d (and is not d itself,
// which already has the domain SPF). SPF and DMARC count as present when
// the domain has its own: the panel never overwrites them.
func mailNeeds(d admproto.MailDomain, ips []net.IP, helo string) []mailRec {
	host := "mail." + d.Name
	helo = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(helo)), ".")
	if helo == "" {
		helo = host
	}
	out := []mailRec{{Key: "mx", Name: d.Name, Type: "MX", Value: host, Prio: 10}}
	for _, ip := range ips {
		t := "AAAA"
		if ip.To4() != nil {
			t = "A"
		}
		out = append(out, mailRec{Key: "a", Name: host, Type: t, Value: ip.String()})
	}
	out = append(out, mailRec{Key: "spf", Name: d.Name, Type: "TXT", Value: "v=spf1 mx -all"})
	// SPF of the name the server greets with: checked apart from the
	// domain's, and without it reports say "HELO does not publish SPF".
	if helo != d.Name && nameUnder(helo, d.Name) {
		out = append(out, mailRec{Key: "helo", Name: helo, Type: "TXT", Value: "v=spf1 a -all"})
	}
	if d.DKIM && d.DKIMRecord != "" {
		out = append(out, mailRec{Key: "dkim", Name: firstOf(d.Selector, "mail") + "._domainkey." + d.Name, Type: "TXT", Value: d.DKIMRecord})
	}
	out = append(out, mailRec{Key: "dmarc", Name: "_dmarc." + d.Name, Type: "TXT", Value: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + d.Name})
	return out
}

// lookupState asks public DNS for r and sets its State: ok, missing, or
// differs (with what is there now in Now). For SPF, HELO and DMARC only
// records of that kind count, and any policy of the domain's own is accepted.
func lookupState(ctx context.Context, r *mailRec) {
	rv := publicDNS
	var got []string
	var err error
	switch r.Type {
	case "MX":
		var mx []*net.MX
		mx, err = rv.LookupMX(ctx, r.Name)
		for _, m := range mx {
			got = append(got, strings.TrimSuffix(m.Host, "."))
		}
	case "A", "AAAA":
		var ips []net.IPAddr
		ips, err = rv.LookupIPAddr(ctx, r.Name)
		for _, ip := range ips {
			got = append(got, ip.IP.String())
		}
	case "TXT":
		got, err = rv.LookupTXT(ctx, r.Name)
		if r.Key == "spf" || r.Key == "helo" || r.Key == "dmarc" {
			prefix := "v=spf1"
			if r.Key == "dmarc" {
				prefix = "v=DMARC1"
			}
			got = slices.DeleteFunc(got, func(v string) bool { return !strings.HasPrefix(strings.TrimSpace(v), prefix) })
		}
	}
	norm := func(v string) string {
		return strings.Join(strings.Fields(strings.TrimSuffix(strings.ToLower(v), ".")), " ")
	}
	switch {
	case err != nil || len(got) == 0:
		r.State = "missing"
	case slices.ContainsFunc(got, func(v string) bool { return norm(v) == norm(r.Value) }):
		r.State = "ok"
	case r.Key == "spf" || r.Key == "helo" || r.Key == "dmarc":
		// Their own policy: counted as present, shown as it is.
		r.State, r.Now = "ok", got[0]
	default:
		r.State, r.Now = "differs", strings.Join(got, ", ")
	}
}

// mailDomainOf fetches the mail view from the helper and picks the domain
// called name from it.
func (s *Server) mailDomainOf(ctx context.Context, by, name string) (admproto.MailDomain, admproto.MailView, error) {
	var v admproto.MailView
	if err := s.Adm(by).Call(ctx, admproto.OpMail, nil, &v); err != nil {
		return admproto.MailDomain{}, v, err
	}
	for _, d := range v.Domains {
		if d.Name == name {
			return d, v, nil
		}
	}
	return admproto.MailDomain{}, v, errors.New("no such mail domain")
}

// mailDNS checks a mail domain's records and the reverse names of the
// server's addresses.
func (s *Server) mailDNS(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	d, v, err := s.mailDomainOf(ctx, userOf(r).Login, r.URL.Query().Get("domain"))
	if err != nil {
		admFail(w, err)
		return
	}
	// 1. Build the wanted records and find the zone and its provider, if any.
	ips := ServerIPs()
	recs := mailNeeds(d, ips, v.Helo)
	s.dom.mu.Lock()
	s.dom.load()
	zone, api := "", false
	var dns DNSProvider
	if z := s.dom.zoneFor(d.Name); z != nil {
		zone = z.Name
		if p := s.dom.prov(z.Provider); p != nil {
			api, dns = true, s.providerAPI(*p)
		}
	}
	s.dom.mu.Unlock()
	// 2. Look every record up in public DNS at once.
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		go func() { defer wg.Done(); lookupState(ctx, &recs[i]) }()
	}
	wg.Wait()
	// 3. With a provider, its own record list decides what still needs
	// writing (public DNS can lag or show registrar forwarding).
	var have []Record
	if dns != nil {
		have, _ = dns.Records(ctx, zone)
	}
	for i := range recs {
		if api && have != nil && !mailNeedWrite(have, recs[i], zone) {
			recs[i].State, recs[i].Now, recs[i].Auto = "ok", "", false
			continue
		}
		recs[i].Auto = mailAuto(api, recs[i])
	}
	// 4. The reverse name (PTR) of each server address should be the HELO
	// name, because many receivers distrust mail from an address whose PTR
	// does not match. Only the hosting provider can set it, so this is a
	// check only.
	var ptr []map[string]string
	for _, ip := range ips {
		names, _ := net.DefaultResolver.LookupAddr(ctx, ip.String())
		got := ""
		if len(names) > 0 {
			got = strings.TrimSuffix(names[0], ".")
		}
		st := "missing"
		if got != "" {
			st = "differs"
		}
		if strings.EqualFold(got, v.Helo) {
			st = "ok"
		}
		ptr = append(ptr, map[string]string{"ip": ip.String(), "want": v.Helo, "now": got, "state": st})
	}
	auditSkip(r)
	ok(w, map[string]any{"records": recs, "zone": zone, "api": api, "ptr": ptr})
}

// mailAuto is whether the panel can write rec: anything still missing, and a
// foreign MX / A / DKIM. SPF and DMARC with some value of their own stay.
func mailAuto(api bool, r mailRec) bool {
	if !api {
		return false
	}
	if r.State == "missing" {
		return true
	}
	return r.State == "differs" && (r.Key == "mx" || r.Key == "a" || r.Key == "dkim")
}

// dnsValEq compares two record values ignoring case, a trailing dot and
// extra spaces.
func dnsValEq(a, b string) bool {
	norm := func(v string) string {
		return strings.Join(strings.Fields(strings.TrimSuffix(strings.ToLower(v), ".")), " ")
	}
	return norm(a) == norm(b)
}

// sameNameType returns the records in have with rec's type and name.
func sameNameType(have []Record, rec Record) []Record {
	var out []Record
	for _, x := range have {
		if strings.EqualFold(x.Type, rec.Type) && strings.EqualFold(x.Name, rec.Name) {
			out = append(out, x)
		}
	}
	return out
}

// hasTXTPrefix reports whether name has a TXT record starting with prefix
// (such as "v=spf1"), whatever the rest says.
func hasTXTPrefix(have []Record, name, prefix string) bool {
	for _, x := range have {
		if x.Type == "TXT" && strings.EqualFold(x.Name, name) && strings.HasPrefix(strings.TrimSpace(x.Value), prefix) {
			return true
		}
	}
	return false
}

// recIDDesc orders records by provider ID, highest first; IDs that are both
// numbers are compared as numbers, others as text.
func recIDDesc(a, b Record) bool {
	ia, ea := strconv.Atoi(a.ID)
	ib, eb := strconv.Atoi(b.ID)
	if ea == nil && eb == nil {
		return ia > ib
	}
	return a.ID > b.ID
}

// mailNeedWrite decides from the provider's record list whether rec must be
// written: SPF/HELO/DMARC only when the name has none of that kind; MX and
// DKIM unless exactly one record with the right value exists; A/AAAA unless
// the address is among those at the name.
func mailNeedWrite(have []Record, rec mailRec, zone string) bool {
	dr := Record{Type: rec.Type, Name: relName(rec.Name, zone), Value: rec.Value, Priority: rec.Prio}
	same := sameNameType(have, dr)
	switch rec.Key {
	case "spf", "helo", "dmarc":
		prefix := "v=spf1"
		if rec.Key == "dmarc" {
			prefix = "v=DMARC1"
		}
		return !hasTXTPrefix(have, dr.Name, prefix)
	case "mx":
		return !(len(same) == 1 && dnsValEq(same[0].Value, dr.Value) && same[0].Priority == dr.Priority)
	case "dkim":
		return !(len(same) == 1 && slices.ContainsFunc(same, func(x Record) bool { return dnsValEq(x.Value, dr.Value) }))
	default: // A / AAAA: another address at the same name is kept
		return !slices.ContainsFunc(same, func(x Record) bool { return dnsValEq(x.Value, dr.Value) })
	}
}

// syncMailRec writes rec through the provider. Public DNS is not the source:
// Namecheap forwarding MX lives outside setHosts, so lookup can say "differs"
// while getHosts has no MX at all. SPF/DMARC/HELO with any own TXT stay.
// MX and DKIM replace the first existing record of that name and delete the
// extra ones; other records are added. It returns 1 when it wrote something.
func (s *Server) syncMailRec(ctx context.Context, z *zoneRec, api DNSProvider, have []Record, rec mailRec) (int, error) {
	if !mailNeedWrite(have, rec, z.Name) {
		return 0, nil
	}
	dr := Record{Type: rec.Type, Name: relName(rec.Name, z.Name), Value: rec.Value, Priority: rec.Prio}
	auto := rec.Key == "a"
	same := sameNameType(have, dr)
	replace := rec.Key == "mx" || rec.Key == "dkim"
	if replace && len(same) > 0 {
		dr.ID = same[0].ID
		if err := api.Update(ctx, z.Name, dr); err != nil {
			return 0, err
		}
		z.remember(dr, auto)
		extras := append([]Record(nil), same[1:]...)
		sort.Slice(extras, func(i, j int) bool { return recIDDesc(extras[i], extras[j]) })
		for _, x := range extras {
			if err := api.Delete(ctx, z.Name, x.ID); err != nil {
				return 0, err
			}
			z.Ours = slices.DeleteFunc(z.Ours, func(o ourRec) bool { return o.ID == x.ID })
		}
		return 1, nil
	}
	if err := s.putRecord(ctx, z, api, dr, auto); err != nil {
		return 0, err
	}
	return 1, nil
}

// mailDNSCreate writes missing mail records, and replaces a foreign MX / A /
// DKIM, through the zone's provider. It looks at the provider's list, not
// public DNS: registrar forwarding MX is not in that list and would be skipped
// as "already there". Own SPF and DMARC stay.
func (s *Server) mailDNSCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Domain string `json:"domain"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	d, v, err := s.mailDomainOf(ctx, userOf(r).Login, in.Domain)
	if err != nil {
		admFail(w, err)
		return
	}
	auditTarget(r, d.Name)
	recs := mailNeeds(d, ServerIPs(), v.Helo)
	s.dom.mu.Lock()
	defer s.dom.mu.Unlock()
	s.dom.load()
	z := s.dom.zoneFor(d.Name)
	if z == nil {
		fail(w, http.StatusConflict, d.Name+" is in no zone on the Domains screen")
		return
	}
	p := s.dom.prov(z.Provider)
	if p == nil {
		fail(w, http.StatusConflict, "zone "+z.Name+" is managed by hand")
		return
	}
	api := s.providerAPI(*p)
	have, err := api.Records(ctx, z.Name)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	z.reconcileOurs(have)
	// Write each record, re-reading the provider's list after every change so
	// the next decision sees it.
	made := 0
	for _, rec := range recs {
		n, err := s.syncMailRec(ctx, z, api, have, rec)
		if err != nil {
			s.dom.save()
			fail(w, http.StatusBadGateway, fmt.Sprintf("%s %s: %v", rec.Type, rec.Name, err))
			return
		}
		if n == 0 {
			continue
		}
		made += n
		if have, err = api.Records(ctx, z.Name); err != nil {
			s.dom.save()
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	if err := s.dom.save(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, map[string]int{"created": made})
}
