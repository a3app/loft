package adm

// This file is the mail server part of loft-panel-adm (the root helper the
// panel talks to over the admproto socket): it validates the Mail screen's
// edits to domains, mailboxes, aliases and certificates, stores them in
// mail.json, and puts the matching files on the machine. The file contents
// themselves are rendered in mailcfg.go.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
)

// Mail: the operator's intent lives in /etc/loft-panel/mail.json.
// Adding a domain (or applying after a package) writes every file, checks
// them with postfix check and doveconf, then reloads. A mailbox or alias
// only rebuilds the maps and reloads Postfix: Dovecot rereads its users
// file, OpenDKIM and nginx stay. A failed full check puts every file back —
// a broken main.cf stops mail for every domain, not only the one edited.

// mailFile is the path of mail.json, the saved mail settings.
func (o *Ops) mailFile() string {
	return filepath.Join(firstNonEmpty(o.PanelEtc, "/etc/loft-panel"), "mail.json")
}

// mailTLSDir holds one .crt/.key pair per mail host (mail.<domain>).
func (o *Ops) mailTLSDir() string {
	return filepath.Join(firstNonEmpty(o.PanelEtc, "/etc/loft-panel"), "mail-tls")
}

// readMail loads mail.json; a missing file is an empty store, not an error.
func (o *Ops) readMail() (*mailStore, error) {
	s := &mailStore{}
	b, err := os.ReadFile(o.mailFile())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	return s, json.Unmarshal(b, s)
}

// writeMail saves mail.json, readable by root only, since it holds password
// hashes and the webmail session key.
func (o *Ops) writeMail(s *mailStore) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.MkdirAll(filepath.Dir(o.mailFile()), 0o755); err != nil {
		return err
	}
	return storeWrite(o.mailFile(), append(b, '\n'), 0o600)
}

// mailInstalled reports whether Postfix is installed (postconf is present).
func (o *Ops) mailInstalled() bool { return exists(o.sys("/usr/sbin/postconf")) }

var (
	mailDomainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	mailLocalRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9_+-]|\.[a-z0-9_+-]){0,63}$`)
	mailHashRe   = regexp.MustCompile(`^\{ARGON2ID\}\$argon2id\$v=19\$m=\d{4,7},t=\d{1,2},p=1\$[A-Za-z0-9+/]{16,}\$[A-Za-z0-9+/]{32,}$`)
	mailOwnerRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	dkimSelRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
)

// checkAddr is an address of any domain (forwards, catch-alls).
func checkAddr(a string) error {
	l, d := splitAddr(a)
	if d == "" || !mailLocalRe.MatchString(l) || !mailDomainRe.MatchString(d) || len(a) > 254 {
		return fmt.Errorf("%q is not an e-mail address", a)
	}
	return nil
}

// cleanAddrs lowercases, trims and dedupes a list of addresses.
func cleanAddrs(in []string, max int) ([]string, error) {
	var out []string
	for _, a := range in {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || slices.Contains(out, a) {
			continue
		}
		if err := checkAddr(a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if len(out) > max {
		return nil, fmt.Errorf("at most %d addresses", max)
	}
	return out, nil
}

// domain, box and alias return the index of an entry by name, or -1.
func (s *mailStore) domain(name string) int {
	return slices.IndexFunc(s.Domains, func(d admproto.MailDomain) bool { return d.Name == name })
}
func (s *mailStore) box(addr string) int {
	return slices.IndexFunc(s.Boxes, func(b admproto.MailBox) bool { return b.Address == addr })
}
func (s *mailStore) alias(from string) int {
	return slices.IndexFunc(s.Aliases, func(a admproto.MailAlias) bool { return a.From == from })
}

// localAddr checks an address of one of this server's mail domains.
func (s *mailStore) localAddr(a string) error {
	if err := checkAddr(a); err != nil {
		return err
	}
	if _, d := splitAddr(a); s.domain(d) < 0 {
		return fmt.Errorf("%s is not a mail domain of this server", d)
	}
	return nil
}

// Mail is the Mail screen.
func (o *Ops) Mail(ctx context.Context) (admproto.MailView, error) {
	o.mailMu.Lock()
	defer o.mailMu.Unlock()
	s, err := o.readMail()
	if err != nil {
		return admproto.MailView{}, err
	}
	return o.mailView(ctx, s), nil
}

// mailViewSnap caches the slow parts of the Mail screen: disk use per box
// (in MB), daemon states and the Dovecot version, taken at time at.
type mailViewSnap struct {
	used    map[string]float64
	svcs    []admproto.MailService
	dovecot string
	at      time.Time
}

const mailSnapFresh = 10 * time.Second

// mailView builds the Mail screen from the store. Password hashes are
// blanked so they never leave the helper. When Postfix is not installed it
// skips asking the host for usage and daemon states.
func (o *Ops) mailView(ctx context.Context, s *mailStore) admproto.MailView {
	v := admproto.MailView{Installed: o.mailInstalled(), Helo: s.helo(), Domains: []admproto.MailDomain{},
		Boxes: []admproto.MailBox{}, Aliases: []admproto.MailAlias{}, Services: []admproto.MailService{}, WebHosts: []string{}, Error: s.Error}
	e := o.mailEnvLite()
	for _, d := range s.Domains {
		h := mailHost(d.Name)
		d.Cert = e.Certs[h]
		if d.Cert {
			if until, err := certUntil(filepath.Join(o.mailTLSDir(), h+".crt")); err == nil {
				d.CertUntil = until.UTC().Format(time.RFC3339)
			}
		}
		v.Domains = append(v.Domains, d)
	}
	v.Aliases = append(v.Aliases, s.Aliases...)
	if !v.Installed {
		for _, b := range s.Boxes {
			b.Hash = ""
			v.Boxes = append(v.Boxes, b)
		}
		return v
	}
	sn := o.mailSnap(ctx)
	for _, b := range s.Boxes {
		b.Hash = ""
		b.UsedMB = sn.used[b.Address]
		v.Boxes = append(v.Boxes, b)
	}
	v.Webmail = e.Webmail
	v.WebHosts = webHosts(s, e)
	v.Dovecot = sn.dovecot
	v.Services = sn.svcs
	return v
}

// certUntil is the expiry of the first certificate in a PEM file.
func certUntil(path string) (time.Time, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return time.Time{}, errors.New("no certificate")
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return c.NotAfter, nil
}

// mailSnap is usage and daemon state; systemd-run is paid once per few seconds.
func (o *Ops) mailSnap(ctx context.Context) mailViewSnap {
	if o.mSnap.used != nil && time.Since(o.mSnap.at) < mailSnapFresh {
		return o.mSnap
	}
	// One host command, three parts split by "@@": disk use of every box,
	// Dovecot's version, and the state of each mail daemon.
	out, _ := o.host(ctx, "sh", "-c", "du -sk "+mailRoot+"/*/* 2>/dev/null; echo @@; dovecot --version 2>/dev/null; echo @@; systemctl is-active postfix dovecot opendkim rspamd")
	part := strings.SplitN(out, "@@", 3)
	for len(part) < 3 {
		part = append(part, "")
	}
	// du prints "<KB> /var/vmail/<domain>/<local>"; turn it into MB per address.
	used := map[string]float64{}
	for _, ln := range strings.Split(part[0], "\n") {
		f := strings.Fields(ln)
		if len(f) != 2 {
			continue
		}
		kb, err := strconv.ParseFloat(f[0], 64)
		rel, ok := strings.CutPrefix(f[1], mailRoot+"/")
		d, l, ok2 := strings.Cut(rel, "/")
		if err != nil || !ok || !ok2 {
			continue
		}
		used[l+"@"+d] = kb / 1024
	}
	dovecot := ""
	if f := strings.Fields(part[1]); len(f) > 0 {
		dovecot = f[0]
	}
	// systemctl prints one state per unit, in the order asked. rspamd is
	// optional, so a missing binary reads "absent" instead of "inactive".
	keys := []string{"postfix", "dovecot", "opendkim", "rspamd"}
	st := strings.Fields(part[2])
	var svcs []admproto.MailService
	for i, k := range keys {
		state := "unknown"
		if i < len(st) {
			state = st[i]
		}
		if k == "rspamd" && !exists(o.sys("/usr/bin/rspamd")) {
			state = "absent"
		}
		svcs = append(svcs, admproto.MailService{Key: k, State: state})
	}
	o.mSnap = mailViewSnap{used: used, svcs: svcs, dovecot: dovecot, at: time.Now()}
	return o.mSnap
}

// mailEnvLite is what the view needs without asking systemd: certificates
// and whether Roundcube is there.
func (o *Ops) mailEnvLite() mailEnv {
	e := mailEnv{Certs: map[string]bool{}}
	if ents, err := os.ReadDir(o.mailTLSDir()); err == nil {
		for _, en := range ents {
			if h, ok := strings.CutSuffix(en.Name(), ".crt"); ok && exists(filepath.Join(o.mailTLSDir(), h+".key")) {
				e.Certs[h] = true
			}
		}
	}
	e.Webmail = exists(o.sys(roundcubeRoot + "/index.php"))
	e.PHPSock = phpSocket(o.sys(""))
	return e
}

// phpSocket finds php-fpm's socket; its name carries the PHP version, which
// changes with the distribution, so it is looked up, never written down.
func phpSocket(root string) string {
	alt := filepath.Join(root, "/run/php/php-fpm.sock")
	if real, err := filepath.EvalSymlinks(alt); err == nil {
		return strings.TrimPrefix(real, strings.TrimSuffix(root, "/"))
	}
	found, _ := filepath.Glob(filepath.Join(root, "/run/php/php*-fpm.sock"))
	sort.Strings(found)
	if len(found) == 0 {
		return ""
	}
	return strings.TrimPrefix(found[len(found)-1], strings.TrimSuffix(root, "/"))
}

// MailDomainSave adds, changes or removes a mail domain.
func (o *Ops) MailDomainSave(ctx context.Context, req admproto.MailDomainSave) (admproto.MailView, error) {
	o.mailMu.Lock()
	defer o.mailMu.Unlock()
	s, err := o.readMail()
	if err != nil {
		return admproto.MailView{}, err
	}
	d := req.MailDomain
	d.Name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d.Name)), ".")
	if !mailDomainRe.MatchString(d.Name) || len(d.Name) > 200 {
		return admproto.MailView{}, fmt.Errorf("%q is not a domain name", d.Name)
	}
	i := s.domain(d.Name)
	// drop lists paths removed from disk only after the apply succeeds.
	var drop []string
	if req.Delete {
		// Removing a domain also removes its boxes and aliases; the stored
		// letters go only when the operator asked for it (DropMail).
		if i < 0 {
			return admproto.MailView{}, errors.New("no such mail domain")
		}
		// Its DKIM key goes with it: the record it answers is no longer
		// published, and a private key nobody uses is only a liability.
		drop = append(drop, dkimKeyPath(s.Domains[i]))
		s.Domains = slices.Delete(s.Domains, i, i+1)
		s.Boxes = slices.DeleteFunc(s.Boxes, func(b admproto.MailBox) bool { _, bd := splitAddr(b.Address); return bd == d.Name })
		s.Aliases = slices.DeleteFunc(s.Aliases, func(a admproto.MailAlias) bool { _, ad := splitAddr(a.From); return ad == d.Name })
		if req.DropMail {
			drop = append(drop, mailRoot+"/"+d.Name)
		}
	} else {
		// Add or change: validate, then keep the published DKIM record while
		// the selector (and so the key) stays the same.
		if d.CatchAll, err = cleanAddrs(d.CatchAll, 20); err != nil {
			return admproto.MailView{}, fmt.Errorf("catch-all: %w", err)
		}
		d.Selector = strings.ToLower(strings.TrimSpace(d.Selector))
		if d.Selector != "" && !dkimSelRe.MatchString(d.Selector) {
			return admproto.MailView{}, fmt.Errorf("%q is not a DKIM selector", d.Selector)
		}
		d.Cert, d.CertUntil = false, ""
		if i < 0 {
			if len(s.Domains) >= 100 {
				return admproto.MailView{}, errors.New("at most 100 mail domains")
			}
			s.Domains = append(s.Domains, d)
		} else {
			if dkimSel(s.Domains[i]) == dkimSel(d) {
				d.DKIMRecord = s.Domains[i].DKIMRecord
			}
			s.Domains[i] = d
		}
	}
	return o.mailCommit(ctx, s, drop)
}

// MailBoxSave adds, changes or removes a mailbox.
func (o *Ops) MailBoxSave(ctx context.Context, req admproto.MailBoxSave) (admproto.MailView, error) {
	o.mailMu.Lock()
	defer o.mailMu.Unlock()
	s, err := o.readMail()
	if err != nil {
		return admproto.MailView{}, err
	}
	b := req.MailBox
	b.Address = strings.ToLower(strings.TrimSpace(b.Address))
	if err := s.localAddr(b.Address); err != nil {
		return admproto.MailView{}, err
	}
	i := s.box(b.Address)
	var drop []string
	if req.Delete {
		if i < 0 {
			return admproto.MailView{}, errors.New("no such mailbox")
		}
		s.Boxes = slices.Delete(s.Boxes, i, i+1)
		if req.DropMail {
			l, d := splitAddr(b.Address)
			drop = append(drop, mailRoot+"/"+d+"/"+l)
		}
		return o.mailCommitMaps(ctx, s, drop)
	}
	// Add or change: an address is either a box or an alias, never both.
	if s.alias(b.Address) >= 0 {
		return admproto.MailView{}, fmt.Errorf("%s is an alias", b.Address)
	}
	if b.Owner != "" && !mailOwnerRe.MatchString(b.Owner) {
		return admproto.MailView{}, errors.New("bad owner")
	}
	if b.QuotaMB < 0 || b.QuotaMB > 1<<20 {
		return admproto.MailView{}, errors.New("quota: 0 to 1048576 MB")
	}
	if b.Forwards, err = cleanAddrs(b.Forwards, 20); err != nil {
		return admproto.MailView{}, fmt.Errorf("forwarding: %w", err)
	}
	if slices.Contains(b.Forwards, b.Address) {
		return admproto.MailView{}, errors.New("forwarding: a box cannot forward to itself")
	}
	b.UsedMB = 0
	// The panel sends only an Argon2id hash, never the password. An empty
	// hash on an existing box means "keep the old password".
	switch {
	case b.Hash != "" && !mailHashRe.MatchString(b.Hash):
		return admproto.MailView{}, errors.New("bad password hash")
	case b.Hash == "" && i < 0:
		return admproto.MailView{}, errors.New("a new mailbox needs a password")
	case b.Hash == "":
		b.Hash = s.Boxes[i].Hash
	}
	if i < 0 {
		if len(s.Boxes) >= 5000 {
			return admproto.MailView{}, errors.New("at most 5000 mailboxes")
		}
		b.Created = time.Now().UTC().Format(time.RFC3339)
		s.Boxes = append(s.Boxes, b)
	} else {
		b.Created = s.Boxes[i].Created
		s.Boxes[i] = b
	}
	return o.mailCommitMaps(ctx, s, nil)
}

// MailAliasSave adds, changes or removes an alias.
func (o *Ops) MailAliasSave(ctx context.Context, req admproto.MailAliasSave) (admproto.MailView, error) {
	o.mailMu.Lock()
	defer o.mailMu.Unlock()
	s, err := o.readMail()
	if err != nil {
		return admproto.MailView{}, err
	}
	a := req.MailAlias
	a.From = strings.ToLower(strings.TrimSpace(a.From))
	// Old is the alias's previous address when it is renamed. The old entry
	// is removed first and, unless this is a delete, the new one is added.
	old := strings.ToLower(strings.TrimSpace(firstNonEmpty(req.Old, a.From)))
	if i := s.alias(old); i >= 0 {
		s.Aliases = slices.Delete(s.Aliases, i, i+1)
	} else if req.Delete || req.Old != "" {
		return admproto.MailView{}, errors.New("no such alias")
	}
	if !req.Delete {
		if err := s.localAddr(a.From); err != nil {
			return admproto.MailView{}, err
		}
		if s.box(a.From) >= 0 {
			return admproto.MailView{}, fmt.Errorf("%s is a mailbox", a.From)
		}
		if s.alias(a.From) >= 0 {
			return admproto.MailView{}, fmt.Errorf("alias %s exists", a.From)
		}
		if a.To, err = cleanAddrs(a.To, 50); err != nil {
			return admproto.MailView{}, err
		}
		if len(a.To) == 0 {
			return admproto.MailView{}, errors.New("an alias needs somewhere to deliver")
		}
		if slices.Contains(a.To, a.From) {
			return admproto.MailView{}, errors.New("an alias cannot deliver to itself")
		}
		if len(s.Aliases) >= 5000 {
			return admproto.MailView{}, errors.New("at most 5000 aliases")
		}
		s.Aliases = append(s.Aliases, a)
		sort.Slice(s.Aliases, func(i, j int) bool { return s.Aliases[i].From < s.Aliases[j].From })
	}
	return o.mailCommitMaps(ctx, s, nil)
}

// MailCerts installs the certificates of the mail names; a name not listed
// loses its own. Nothing is rewritten when they are unchanged.
func (o *Ops) MailCerts(ctx context.Context, req admproto.MailCerts) (admproto.MailView, error) {
	o.mailMu.Lock()
	defer o.mailMu.Unlock()
	s, err := o.readMail()
	if err != nil {
		return admproto.MailView{}, err
	}
	// 1. Check every certificate first, so a bad one changes nothing.
	want := map[string]admproto.MailCert{}
	for _, c := range req.Certs {
		c.Host = strings.ToLower(c.Host)
		d, ok := strings.CutPrefix(c.Host, "mail.")
		if !ok || !mailDomainRe.MatchString(d) {
			return admproto.MailView{}, fmt.Errorf("%q is not a mail server name", c.Host)
		}
		if _, err := checkPair(c.Fullchain, c.Key); err != nil {
			return admproto.MailView{}, fmt.Errorf("%s: %w", c.Host, err)
		}
		want[c.Host] = c
	}
	dir := o.mailTLSDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return admproto.MailView{}, err
	}
	// 2. Remove certificates of names no longer listed.
	changed := false
	have := o.mailEnvLite().Certs
	for h := range have {
		if _, ok := want[h]; !ok {
			os.Remove(filepath.Join(dir, h+".crt"))
			os.Remove(filepath.Join(dir, h+".key"))
			changed = true
		}
	}
	// 3. Write new or changed pairs, key first.
	for h, c := range want {
		crt, _ := os.ReadFile(filepath.Join(dir, h+".crt"))
		key, _ := os.ReadFile(filepath.Join(dir, h+".key"))
		if string(crt) == c.Fullchain && string(key) == c.Key {
			continue
		}
		if err := storeWrite(filepath.Join(dir, h+".key"), []byte(c.Key), 0o600); err != nil {
			return admproto.MailView{}, err
		}
		if err := storeWrite(filepath.Join(dir, h+".crt"), []byte(c.Fullchain), 0o644); err != nil {
			return admproto.MailView{}, err
		}
		changed = true
	}
	// 4. Only a real change rewrites the daemons' configs and reloads them.
	if !changed {
		return o.mailView(ctx, s), nil
	}
	return o.mailCommit(ctx, s, nil)
}

// checkPair makes sure the key belongs to the certificate.
func checkPair(fullchain, key string) (time.Time, error) {
	pair, err := tls.X509KeyPair([]byte(fullchain), []byte(key))
	if err != nil {
		return time.Time{}, fmt.Errorf("certificate and key: %v", err)
	}
	c, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return time.Time{}, err
	}
	return c.NotAfter, nil
}

// MailApply writes everything again (after a package was installed, say).
func (o *Ops) MailApply(ctx context.Context) (admproto.MailView, error) {
	o.mailMu.Lock()
	defer o.mailMu.Unlock()
	s, err := o.readMail()
	if err != nil {
		return admproto.MailView{}, err
	}
	return o.mailCommit(ctx, s, nil)
}

// mailCommit applies s and saves it. The intent is saved even when the
// apply fails, with the error, so the screen shows what is wrong and the
// next save tries again; the files on the machine stay the old ones.
func (o *Ops) mailCommit(ctx context.Context, s *mailStore, drop []string) (admproto.MailView, error) {
	return o.mailCommitKind(ctx, s, drop, false)
}

// mailCommitMaps is mailCommit for box and alias edits: only the maps and
// the users file are rewritten, which is faster and leaves the other daemons
// alone.
func (o *Ops) mailCommitMaps(ctx context.Context, s *mailStore, drop []string) (admproto.MailView, error) {
	return o.mailCommitKind(ctx, s, drop, true)
}

// mailCommitKind is the shared body of mailCommit and mailCommitMaps. A
// maps-only apply is used only when the last apply succeeded; after a
// failure everything is written again so the machine catches up. Paths in
// drop (deleted mail, old DKIM keys) are removed only after a successful
// apply, so a failed save loses no data.
func (o *Ops) mailCommitKind(ctx context.Context, s *mailStore, drop []string, mapsOnly bool) (admproto.MailView, error) {
	if s.DESKey == "" {
		s.DESKey = randText(24)
	}
	var aerr error
	if o.mailInstalled() {
		if mapsOnly && s.Error == "" {
			aerr = o.mailApplyMaps(ctx, s)
		} else {
			aerr = o.mailApply(ctx, s)
		}
	} else if len(s.Domains) > 0 {
		aerr = errors.New("the mail server package is not installed")
	}
	s.Error = ""
	if aerr != nil {
		s.Error = aerr.Error()
	}
	if err := o.writeMail(s); err != nil {
		return admproto.MailView{}, err
	}
	if aerr == nil {
		for _, p := range drop {
			o.host(ctx, "rm", "-rf", "--one-file-system", p)
		}
	}
	return o.mailView(ctx, s), aerr
}

const randAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// randText is n random characters from an alphabet without look-alikes
// (no 0/O, 1/l/I). It makes Roundcube's session key.
func randText(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = randAlphabet[int(b[i])%len(randAlphabet)]
	}
	return string(b)
}

// shq quotes for sh.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// mailEnv looks at the machine: Dovecot's version, the vmail account
// (created here), rspamd, the panel's address.
func (o *Ops) mailEnv(ctx context.Context) (mailEnv, error) {
	e := o.mailEnvLite()
	// Dovecot 2.3 and 2.4 need different config syntax (see renderDovecot).
	ver, err := o.host(ctx, "dovecot", "--version")
	if err != nil {
		return e, fmt.Errorf("dovecot: %v", err)
	}
	e.Dovecot24 = !strings.HasPrefix(strings.TrimSpace(ver), "2.3")
	// Create the vmail account and /var/vmail if missing, then read its ids.
	out, err := o.host(ctx, "sh", "-c", "id -u "+mailUser+" >/dev/null 2>&1 || useradd --system --home-dir "+mailRoot+
		" --no-create-home --shell /usr/sbin/nologin "+mailUser+" && install -d -o "+mailUser+" -g "+mailUser+" -m 0770 "+mailRoot+
		" && id -u "+mailUser+" && id -g "+mailUser)
	if err != nil {
		return e, fmt.Errorf("account %s: %v", mailUser, err)
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		return e, fmt.Errorf("account %s: no uid", mailUser)
	}
	e.UID, _ = strconv.Atoi(f[len(f)-2])
	e.GID, _ = strconv.Atoi(f[len(f)-1])
	if st, _ := o.host(ctx, "systemctl", "is-active", "rspamd"); strings.TrimSpace(st) == "active" {
		e.Spam = true
	}
	if h, err := o.host(ctx, "hostname", "-f"); err == nil {
		e.Hostname = strings.TrimSpace(h)
	}
	e.Listen = "127.0.0.1:8091"
	if site, err := o.currentSite(); err == nil {
		e.Listen = site.Listen
	}
	return e, nil
}

// mailEnvCached is mailEnv reused for up to a minute, because it runs
// several host commands. Certificates and webmail are cheap file checks and
// are always read fresh.
func (o *Ops) mailEnvCached(ctx context.Context) (mailEnv, error) {
	if !o.mEnvAt.IsZero() && time.Since(o.mEnvAt) < time.Minute {
		e := o.mEnv
		lite := o.mailEnvLite()
		e.Certs, e.Webmail, e.PHPSock = lite.Certs, lite.Webmail, lite.PHPSock
		return e, nil
	}
	e, err := o.mailEnv(ctx)
	if err != nil {
		return e, err
	}
	o.mEnv, o.mEnvAt = e, time.Now()
	return e, nil
}

// dkimKey makes the domain's key when it has none and returns the TXT
// value. An existing key is never replaced: mail in transit is signed with
// it, and a new key is a new selector.
func (o *Ops) dkimKey(d admproto.MailDomain) (pemKey []byte, record string, err error) {
	path := o.sys(dkimKeyPath(d))
	b, err := os.ReadFile(path)
	var key *rsa.PrivateKey
	if err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, "", fmt.Errorf("%s: not a PEM key", path)
		}
		k, perr := x509.ParsePKCS1PrivateKey(blk.Bytes)
		if perr != nil {
			k8, p8err := x509.ParsePKCS8PrivateKey(blk.Bytes)
			rk, ok := k8.(*rsa.PrivateKey)
			if p8err != nil || !ok {
				return nil, "", fmt.Errorf("%s: not an RSA key", path)
			}
			k = rk
		}
		key = k
	} else {
		if key, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			return nil, "", err
		}
		b = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, "", err
	}
	return b, "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pub), nil
}

// mailApply writes the files, checks them, reloads the daemons; a failed
// check or reload puts the old files back.
func (o *Ops) mailApply(ctx context.Context, s *mailStore) error {
	// 1. Learn the machine and render every file the panel owns.
	o.mSnap = mailViewSnap{}
	e, err := o.mailEnvCached(ctx)
	if err != nil {
		return err
	}
	files := []hostFile{
		{Path: mailPostfixDir + "/vdomains", Data: renderVDomains(s), Mode: "0644"},
		{Path: mailPostfixDir + "/vmailbox", Data: renderVMailbox(s), Mode: "0644"},
		{Path: mailPostfixDir + "/valias", Data: renderVAlias(s), Mode: "0644"},
		{Path: mailPostfixDir + "/vlogin", Data: renderVLogin(s), Mode: "0644"},
		{Path: mailPostfixDir + "/norecv", Data: renderNoRecv(s), Mode: "0644"},
		{Path: mailPostfixDir + "/sni", Data: renderSNI(s, e), Mode: "0600"},
		{Path: mailDovecotConf, Data: renderDovecot(s, e), Mode: "0644"},
		{Path: mailDovecotUsers, Data: renderUsers(s, e), Mode: "0640", Owner: "root", Group: "dovecot"},
		{Path: dkimConf, Data: renderDKIMConf(), Mode: "0644"},
		{Path: dkimKeyTable, Data: renderKeyTable(s), Mode: "0644"},
		{Path: dkimSignTable, Data: renderSignTable(s), Mode: "0644"},
		{Path: dkimTrusted, Data: renderTrusted(), Mode: "0644"},
	}
	// DKIM keys: created on first use, and the DNS record is kept in the
	// store so the panel can show what to publish.
	for i, d := range s.Domains {
		if !d.DKIM {
			continue
		}
		key, rec, err := o.dkimKey(d)
		if err != nil {
			return fmt.Errorf("DKIM key of %s: %w", d.Name, err)
		}
		s.Domains[i].DKIMRecord = rec
		files = append(files, hostFile{Path: dkimKeyPath(d), Data: string(key), Mode: "0600", Owner: "opendkim", Group: "opendkim"})
	}
	if auth, err := os.ReadFile(o.sys(mailDovecotAuth)); err == nil {
		if txt, changed := disableSystemAuth(string(auth)); changed {
			files = append(files, hostFile{Path: mailDovecotAuth, Data: txt, Mode: "0644"})
		}
	}
	if e.Webmail {
		files = append(files, hostFile{Path: roundcubeConf, Data: renderRoundcube(s), Mode: "0640", Owner: "root", Group: "www-data"})
	}
	// main.cf and master.cf change through postconf; they are saved whole
	// so a failure puts them back as they were.
	// 2. Remember the current contents (or absence) of every file to touch,
	// so a failure can put the machine back exactly as it was.
	backup := append(slices.Clone(files), hostFile{Path: "/etc/postfix/main.cf", Mode: "0644"}, hostFile{Path: "/etc/postfix/master.cf", Mode: "0644"})
	var undo []hostFile
	var gone []string
	for _, f := range backup {
		b, err := os.ReadFile(o.sys(f.Path))
		if errors.Is(err, os.ErrNotExist) {
			gone = append(gone, f.Path)
			continue
		}
		if err != nil {
			return err
		}
		f.Data = string(b)
		undo = append(undo, f)
	}

	// 3. One shell script: create dirs, install the files, rebuild the maps,
	// set the main.cf/master.cf keys, then let Postfix and Dovecot check the
	// result. "set -e" stops at the first failing command.
	var sc strings.Builder
	sc.WriteString("set -e\ninstall -d -m 0755 " + mailPostfixDir + "\ninstall -d -m 0750 -o root -g dovecot " + filepath.Dir(mailDovecotUsers) +
		"\ninstall -d -m 0755 /etc/opendkim\ninstall -d -m 0700 -o opendkim -g opendkim " + dkimKeyDir + "\n")
	put, err := o.stage(files)
	if err != nil {
		return err
	}
	sc.WriteString(put)
	sc.WriteString(postmapAll())
	for _, kv := range mailParams(s, e) {
		sc.WriteString("postconf -e " + shq(kv[0]+"="+kv[1]) + "\n")
	}
	for _, kv := range mailServices() {
		sc.WriteString("postconf -M -e " + shq(kv[0]+"="+kv[1]) + "\n")
	}
	for _, kv := range mailServiceParams() {
		sc.WriteString("postconf -P " + shq(kv[0]+"="+kv[1]) + "\n")
	}
	sc.WriteString("postfix check 2>&1\ndoveconf -n >/dev/null\n")

	// restore puts back the saved files and reloads, then returns why.
	restore := func(why error) error {
		var r strings.Builder
		p, _ := o.stage(undo)
		r.WriteString(p)
		for _, g := range gone {
			r.WriteString("rm -f " + shq(g) + "\n")
		}
		r.WriteString(postmapAll() + "systemctl reload-or-restart postfix dovecot opendkim || true\n")
		o.host(ctx, "sh", "-c", r.String())
		return why
	}
	// 4. Run the script, then reload the daemons; undo on either failure.
	if out, err := o.host(ctx, "sh", "-c", sc.String()); err != nil {
		return restore(fmt.Errorf("the mail configuration did not pass its check: %s", lastLines(out+" "+err.Error(), 6)))
	}
	if _, err := o.host(ctx, "sh", "-c", "systemctl enable --quiet opendkim; systemctl reload-or-restart postfix dovecot && systemctl restart opendkim"); err != nil {
		return restore(fmt.Errorf("mail did not restart: %v", err))
	}
	// 5. The nginx site for certificates and webmail; it has its own undo.
	return o.writeMailSite(ctx, s, e)
}

// mailApplyMaps writes mailbox/alias maps and reloads Postfix. Dovecot
// rereads the users file on each login; OpenDKIM and nginx are untouched.
func (o *Ops) mailApplyMaps(ctx context.Context, s *mailStore) error {
	if !exists(o.sys(mailPostfixDir+"/vmailbox")) || !exists(o.sys(mailDovecotUsers)) {
		return o.mailApply(ctx, s)
	}
	e, err := o.mailEnvCached(ctx)
	if err != nil {
		return err
	}
	files := []hostFile{
		{Path: mailPostfixDir + "/vmailbox", Data: renderVMailbox(s), Mode: "0644"},
		{Path: mailPostfixDir + "/valias", Data: renderVAlias(s), Mode: "0644"},
		{Path: mailPostfixDir + "/vlogin", Data: renderVLogin(s), Mode: "0644"},
		{Path: mailPostfixDir + "/norecv", Data: renderNoRecv(s), Mode: "0644"},
		{Path: mailDovecotUsers, Data: renderUsers(s, e), Mode: "0640", Owner: "root", Group: "dovecot"},
	}
	put, err := o.stage(files)
	if err != nil {
		return err
	}
	var sc strings.Builder
	sc.WriteString("set -e\n")
	sc.WriteString(put)
	for _, m := range []string{"vmailbox", "valias", "vlogin", "norecv"} {
		sc.WriteString("[ -f " + mailPostfixDir + "/" + m + " ] && postmap hash:" + mailPostfixDir + "/" + m + "\n")
	}
	sc.WriteString("systemctl reload-or-restart postfix\n")
	if out, err := o.host(ctx, "sh", "-c", sc.String()); err != nil {
		return fmt.Errorf("the mail maps did not apply: %s", lastLines(out+" "+err.Error(), 6))
	}
	return nil
}

// postmapAll rebuilds the maps; sni is built with -F, which reads the
// certificate files into it.
func postmapAll() string {
	var b strings.Builder
	for _, m := range []string{"vdomains", "vmailbox", "valias", "vlogin", "norecv"} {
		b.WriteString("[ -f " + mailPostfixDir + "/" + m + " ] && postmap hash:" + mailPostfixDir + "/" + m + "\n")
	}
	b.WriteString("[ -f " + mailPostfixDir + "/sni ] && postmap -F hash:" + mailPostfixDir + "/sni\ntrue\n")
	return b.String()
}

// stage writes files to the helper's own directory and returns the install
// commands that put them in place (the helper cannot write /etc itself).
// In tests it writes them under Root and returns nothing.
func (o *Ops) stage(files []hostFile) (string, error) {
	var b strings.Builder
	for i, f := range files {
		if o.Exec != nil {
			p := o.sys(f.Path)
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(f.Data), 0o600); err != nil {
				return "", err
			}
			continue
		}
		dir := filepath.Join(o.stateDir(), "stage")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		tmp := filepath.Join(dir, fmt.Sprintf("%02d.%s", i, filepath.Base(f.Path)))
		if err := os.WriteFile(tmp, []byte(f.Data), 0o600); err != nil {
			return "", err
		}
		b.WriteString("install -D -m " + f.Mode)
		if f.Owner != "" {
			b.WriteString(" -o " + f.Owner + " -g " + f.Group)
		}
		b.WriteString(" " + shq(tmp) + " " + shq(f.Path) + " && rm -f " + shq(tmp) + "\n")
	}
	return b.String(), nil
}

// lastLines joins the last n lines of s into one line, to keep error
// messages from shell scripts short.
func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " ")
}

// writeMailSite is the nginx site of the mail names; a site nginx -t
// refuses is taken back out.
func (o *Ops) writeMailSite(ctx context.Context, s *mailStore, e mailEnv) error {
	site, link := o.sys(webmailSite), o.sys(webmailLink)
	body := renderMailSite(s, e)
	old, oldErr := os.ReadFile(site)
	if oldErr == nil && string(old) == body {
		return nil
	}
	if body == "" {
		os.Remove(link)
		os.Remove(site)
	} else {
		if err := os.MkdirAll(filepath.Dir(site), 0o755); err != nil {
			return err
		}
		if err := storeWrite(site, []byte(body), 0o644); err != nil {
			return err
		}
		os.Remove(link)
		os.MkdirAll(filepath.Dir(link), 0o755)
		if err := os.Symlink(webmailSite, link); err != nil {
			return err
		}
		if err := o.nginxTest(); err != nil {
			if oldErr == nil {
				storeWrite(site, old, 0o644)
			} else {
				os.Remove(link)
				os.Remove(site)
			}
			return fmt.Errorf("webmail site: %v", err)
		}
	}
	if !exists(o.sys("/usr/sbin/nginx")) {
		return nil
	}
	return o.reloadNginx(ctx)
}

// MailStats counts the queue and the last day's letters in the journal.
func (o *Ops) MailStats(ctx context.Context) (admproto.MailStats, error) {
	var st admproto.MailStats
	if !o.mailInstalled() {
		return st, nil
	}
	out, err := o.host(ctx, "sh", "-c", "postqueue -j 2>/dev/null | wc -l; echo @@; journalctl -q --no-pager --since -24h --facility=mail -o short 2>/dev/null | grep -E 'postfix/(lmtp|smtp)\\[[0-9]+\\]: .* status=sent|NOQUEUE: reject:' || true")
	if err != nil {
		return st, err
	}
	q, logs, _ := strings.Cut(out, "@@")
	st.Queue, _ = strconv.Atoi(strings.TrimSpace(q))
	for _, ln := range strings.Split(logs, "\n") {
		switch {
		case strings.Contains(ln, "NOQUEUE: reject:"):
			st.Rejected++
		case strings.Contains(ln, "postfix/lmtp["):
			st.Received++
		case strings.Contains(ln, "postfix/smtp["):
			st.Sent++
		}
	}
	return st, nil
}
