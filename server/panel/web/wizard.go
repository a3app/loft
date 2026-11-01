package web

// wizard.go is the first-run wizard the owner goes through after setup:
// give the server a DNS name, get Let's Encrypt certificates for the node
// and the panel, and create the first member (a person with a key to the
// overlay network) with a ready-to-import link and .loft file. Renew, which
// keeps those certificates fresh, lives here too. The privileged steps go
// through panel-adm, the root helper; certificates come from acme.go and
// domains.go.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/server/panel/adm"
	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/store"
)

// wizardJSON is what the wizard needs to resume on any step.
type wizardJSON struct {
	Done      bool               `json:"done"`
	Domain    string             `json:"domain"`
	Node      *admproto.NodeInfo `json:"node,omitempty"`
	ServerIPs []string           `json:"server_ips"`
	CertUntil *time.Time         `json:"cert_until,omitempty"`
	Panel     string             `json:"panel_domain,omitempty"`
	PanelCert *time.Time         `json:"panel_cert_until,omitempty"`
	Email     string             `json:"email,omitempty"`
	Members   int                `json:"members"`
}

// wizard returns the state the wizard resumes from: saved settings, this
// server's addresses, the node's info, and how many members exist. If no
// domain is saved yet, the node's own host name is offered when it is a
// valid DNS name.
func (s *Server) wizard(w http.ResponseWriter, r *http.Request) {
	st, err := s.Settings.Get()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := wizardJSON{Done: st.WizardDone, Domain: st.Domain, Email: st.ACMEEmail, ServerIPs: []string{}}
	for _, ip := range ServerIPs() {
		out.ServerIPs = append(out.ServerIPs, ip.String())
	}
	if !st.CertUntil.IsZero() {
		out.CertUntil = &st.CertUntil
	}
	out.Panel = st.PanelDomain
	if !st.PanelCertUntil.IsZero() {
		out.PanelCert = &st.PanelCertUntil
	}
	a := s.Adm(userOf(r).Login)
	var in admproto.NodeInfo
	if err := a.Call(r.Context(), admproto.OpNodeInfo, nil, &in); err == nil {
		out.Node = &in
		if out.Domain == "" && adm.ValidDomain(in.Host) {
			out.Domain = in.Host
		}
	}
	var n admproto.Network
	if err := a.Call(r.Context(), admproto.OpNetworkGet, nil, &n); err == nil && n.Text != "" {
		if nw, err := netconf.ParseCommands(n.Text); err == nil {
			out.Members = len(nw.Members)
		}
	}
	ok(w, out)
}

// checkDomainJSON is the result of a domain check: what the name resolves
// to, this server's addresses, and whether they match.
type checkDomainJSON struct {
	Name      string   `json:"name"`
	IPs       []string `json:"ips"`
	ServerIPs []string `json:"server_ips"`
	Match     bool     `json:"match"`
	Error     string   `json:"error,omitempty"`
}

// domainCheck resolves name and compares with this server's addresses.
// Problems are reported in the reply's Error field, not as HTTP errors, so
// the UI can show them next to the input.
func (s *Server) domainCheck(w http.ResponseWriter, r *http.Request) {
	auditSkip(r)
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Name), "."))
	out := checkDomainJSON{Name: name, IPs: []string{}, ServerIPs: []string{}}
	mine := ServerIPs()
	for _, ip := range mine {
		out.ServerIPs = append(out.ServerIPs, ip.String())
	}
	if !adm.ValidDomain(name) {
		out.Error = "not a DNS name like vpn.example.net"
		ok(w, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		out.Error = "does not resolve"
		ok(w, out)
		return
	}
	for _, ip := range ips {
		out.IPs = append(out.IPs, ip.IP.String())
		for _, m := range mine {
			if ip.IP.Equal(m) {
				out.Match = true
			}
		}
	}
	if len(mine) == 0 { // behind NAT: cannot compare, trust the operator
		out.Match = true
	}
	ok(w, out)
}

// domainSet renames the server through the daemon and lets the panel answer
// to the new name at once (the env file carries it over restarts).
// Certificates saved for an old name are forgotten, since they do not cover
// the new one.
func (s *Server) domainSet(w http.ResponseWriter, r *http.Request) {
	var in admproto.DomainSet
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		var info admproto.NodeInfo
		if err := s.Adm(by).Call(ctx, admproto.OpDomainSet, in, &info); err != nil {
			return nil, admErr(err)
		}
		panel := ""
		if p := strings.TrimSpace(info.Panel); p != "" && p != info.Host {
			panel = p
		}
		s.addHost(info.Host)
		s.addHost(panel)
		if _, err := s.Settings.Update(func(st *store.Settings) {
			if st.Domain != info.Host {
				st.CertUntil = time.Time{} // the old certificate does not cover the new name
			}
			if st.PanelDomain != panel {
				st.PanelCertUntil = time.Time{}
			}
			st.Domain, st.PanelDomain = info.Host, panel
		}); err != nil {
			return nil, err
		}
		s.logf("domain set to %s by %s", info.Host, by)
		return info, nil
	})
}

// certIssue gets a Let's Encrypt certificate for the server name and installs it.
// If the panel has its own name, it gets a certificate as well. Runs as a
// job because ACME can take a while.
func (s *Server) certIssue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	in.Email = strings.TrimSpace(in.Email)
	if in.Email != "" && (!strings.Contains(in.Email, "@") || strings.ContainsAny(in.Email, " \t\r\n<>")) {
		fail(w, http.StatusBadRequest, "email: not an address")
		return
	}
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		name, err := s.serverName(ctx, by)
		if err != nil {
			return nil, err
		}
		until, err := s.issueAndInstall(ctx, by, name, in.Email, "node")
		if err != nil {
			return nil, err
		}
		out := map[string]any{"name": name, "until": until}
		if st, _ := s.Settings.Get(); st.PanelDomain != "" {
			pu, err := s.issueAndInstall(ctx, by, st.PanelDomain, in.Email, "panel")
			if err != nil {
				return nil, fmt.Errorf("the node's certificate is in place; the panel's (%s): %w", st.PanelDomain, err)
			}
			out["panel"], out["panel_until"] = st.PanelDomain, pu
		}
		return out, nil
	})
}

// serverName is the server's DNS name: the saved domain, or else the node's
// advertised host if that is a real DNS name.
func (s *Server) serverName(ctx context.Context, by string) (string, error) {
	st, err := s.Settings.Get()
	if err != nil {
		return "", err
	}
	if st.Domain != "" {
		return st.Domain, nil
	}
	var info admproto.NodeInfo
	if err := s.Adm(by).Call(ctx, admproto.OpNodeInfo, nil, &info); err != nil {
		return "", admErr(err)
	}
	if !adm.ValidDomain(info.Host) {
		return "", fmt.Errorf("the server has no DNS name yet (advertise %q): set one first", info.Advertise)
	}
	return info.Host, nil
}

// issueAndInstall runs ACME and cert.install for target node or panel.
// DNS-01 is used when the name is in a zone with a DNS provider; else HTTP-01.
// (HTTP-01 proves the name by serving a file on port 80; DNS-01 by creating
// a TXT record through the provider.) The e-mail is saved for renewals.
func (s *Server) issueAndInstall(ctx context.Context, by, name, email, target string) (time.Time, error) {
	method := "http-01"
	if _, err := s.dns01For([]string{name}); err == nil {
		method = "dns-01"
	}
	until, err := s.issueCert(ctx, by, []string{name}, method, target, email, "")
	if err != nil {
		return time.Time{}, err
	}
	s.Settings.Update(func(st *store.Settings) {
		if email != "" {
			st.ACMEEmail = email
		}
		if target == "node" && st.Domain == "" {
			st.Domain = name
		}
	})
	return until, nil
}

// Renew keeps the certificate fresh: every 12 h, 30 days before expiry.
func (s *Server) Renew(ctx context.Context) {
	t := time.NewTicker(12 * time.Hour)
	defer t.Stop()
	for {
		st, err := s.Settings.Get()
		due := func(t time.Time) bool { return !t.IsZero() && time.Until(t) < 30*24*time.Hour }
		// The names and check the certificate was last issued with (Domains
		// may have added names or switched it to DNS-01).
		renew := func(names []string, method, target string) {
			cctx, cancel := context.WithTimeout(ctx, jobTimeout)
			defer cancel()
			if _, err := s.issueCert(cctx, "renewal", names, firstOf(method, "http-01"), target, st.ACMEEmail, ""); err != nil {
				s.logf("certificate renewal for %s failed: %v", strings.Join(names, ", "), err)
				s.sysEvent("cert-failed", strings.Join(names, ", "))
			}
		}
		if err == nil && st.Domain != "" && due(st.CertUntil) {
			renew(firstNames(st.NodeCertNames, st.Domain), st.NodeCertMethod, "node")
		}
		if err == nil && st.PanelDomain != "" && st.PanelDomain != st.Domain && due(st.PanelCertUntil) {
			renew(firstNames(st.PanelCertNames, st.PanelDomain), st.PanelCertMethod, "panel")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// memberJSON is the new member's key and the ways to hand it over.
type memberJSON struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Link string `json:"link"`
	File string `json:"file"`
	QR   string `json:"qr,omitempty"` // SVG of Link, drawn here
}

// memberCreate adds an enabled member with a fresh key to network.conf,
// applies it, and returns the key with a loft:// link and a .loft file.
func (s *Server) memberCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	in.ID, in.Name = strings.TrimSpace(in.ID), strings.Join(strings.Fields(in.Name), " ")
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		// 1. Read network.conf and refuse a duplicate member ID.
		a := s.Adm(by)
		var n admproto.Network
		if err := a.Call(ctx, admproto.OpNetworkGet, nil, &n); err != nil {
			return nil, admErr(err)
		}
		nw, err := netconf.ParseCommands(n.Text)
		if err != nil {
			return nil, fmt.Errorf("network.conf: %w", err)
		}
		for _, m := range nw.Members {
			if m.ID == in.ID {
				return nil, fmt.Errorf("member %s already exists", in.ID)
			}
		}
		// 2. Issue the key, add the member, and save network.conf. Expect
		// carries the file's hash as read, so a concurrent edit is not lost.
		var k admproto.IssuedKey
		if err := a.Call(ctx, admproto.OpMemberKey, admproto.MemberKey{ID: in.ID}, &k); err != nil {
			return nil, admErr(err)
		}
		nw.Members = append(nw.Members, netconf.Member{ID: in.ID, Name: firstNonEmpty(in.Name, in.ID), KeySHA256: k.KeySHA256, Authorized: true})
		if err := a.Call(ctx, admproto.OpNetworkApply, admproto.NetworkApply{Text: netconf.RenderCommands(*nw), Expect: n.SHA256}, nil); err != nil {
			return nil, admErr(err)
		}
		// 3. Build the link, file and QR code from the node's public info.
		var info admproto.NodeInfo
		if err := a.Call(ctx, admproto.OpNodeInfo, nil, &info); err != nil {
			return nil, admErr(err)
		}
		link, file := s.memberLink(info, k.Key, firstNonEmpty(in.Name, in.ID), in.ID)
		s.logf("member %s created by %s", in.ID, by)
		svg, _ := qrSVG(link)
		return memberJSON{ID: in.ID, Key: k.Key, Link: link, File: file, QR: svg}, nil
	})
}

// memberLink is the loft:// address (the share-link format in docs/share.md)
// and a .loft file with a GET outbound. Query get=/ws=/h2= lists node fronts
// (the ways a client can connect); the YAML keeps one GET outbound and a
// fronts: list, so the client's Hello offers to attach the other fronts
// later. The node's Status reply stays the source of truth for paths. conn
// is the connection name; memberID is the member part of the outbound name.
func (s *Server) memberLink(in admproto.NodeInfo, key, conn, memberID string) (string, string) {
	host := in.Host
	link := "loft://" + url.PathEscape(key) + "@" + host + "/?" + loftFrontQuery(in) + "#" + url.PathEscape(conn)
	var st store.Settings
	if s.Settings != nil {
		if got, err := s.Settings.Get(); err == nil {
			st = got
		}
	}
	ob := st.OutboundName(host, memberID, "get")
	var b strings.Builder
	fmt.Fprintf(&b, "name: %q\nkind: loft\ntoken: %q\nselect: auto\n", conn, key)
	if fronts := loftMuxFronts(in); len(fronts) > 1 {
		fmt.Fprintf(&b, "fronts: [%s]\n", strings.Join(fronts, ", "))
	}
	if in.RosterPath != "" {
		fmt.Fprintf(&b, "bootstrap: https://%s%s\n", host, in.RosterPath)
	}
	if in.ControllerPub != "" {
		fmt.Fprintf(&b, "controller_pub: %s\n", in.ControllerPub)
	}
	getP := in.GetPath
	if getP == "" {
		getP = config.DefaultGETPath
	}
	fmt.Fprintf(&b, "outbounds:\n  - name: %q\n    type: get\n    url: https://%s\n    path: %s\n    fingerprint: chrome\n", ob, host, getP)
	if in.MaskPub != "" {
		fmt.Fprintf(&b, "    mask_pub: %s\n", in.MaskPub)
	}
	return link, b.String()
}

// loftFrontQuery is get=/ws=/h2=/h3=/quic&fp=chrome. Empty Fronts is GET only.
func loftFrontQuery(in admproto.NodeInfo) string {
	enabled := map[string]bool{}
	for _, f := range in.Fronts {
		enabled[strings.ToLower(strings.TrimSpace(f))] = true
	}
	want := func(name string) bool {
		if len(enabled) == 0 {
			return name == "get"
		}
		return enabled[name]
	}
	getP := in.GetPath
	if getP == "" {
		getP = config.DefaultGETPath
	}
	wsP := in.WSPath
	if wsP == "" {
		wsP = config.DefaultWSPath
	}
	h2P := in.H2Path
	if h2P == "" {
		h2P = config.DefaultH2Path
	}
	h3P := in.H3Path
	if h3P == "" {
		h3P = h2P
	}
	var q []string
	add := func(k, v string) {
		if v == "" {
			q = append(q, k)
			return
		}
		q = append(q, k+"="+loftQueryVal(v))
	}
	if want("get") {
		add("get", getP)
	}
	if want("ws") {
		add("ws", wsP)
	}
	if want("h2") {
		add("h2", h2P)
	}
	if want("h3") {
		add("h3", h3P)
	}
	if want("quic") {
		add("quic", "")
	}
	add("fp", "chrome")
	if in.MaskPub != "" {
		add("pbk", in.MaskPub)
	}
	return strings.Join(q, "&")
}

// loftMuxFronts is get/ws/h2/quic/h3 from NodeInfo in FrontOrder. l3/connect stay off Hello.
func loftMuxFronts(in admproto.NodeInfo) []string {
	enabled := map[string]bool{}
	for _, f := range in.Fronts {
		enabled[strings.ToLower(strings.TrimSpace(f))] = true
	}
	if len(enabled) == 0 {
		return []string{"get"}
	}
	var out []string
	for _, f := range []string{"get", "ws", "h2", "quic", "h3"} {
		if enabled[f] {
			out = append(out, f)
		}
	}
	return out
}

// loftQueryVal escapes a query value but keeps slashes readable, since the
// values are URL paths.
func loftQueryVal(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "%2F", "/")
}

// wizardDone marks the wizard finished, so the UI stops opening it.
func (s *Server) wizardDone(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Settings.Update(func(st *store.Settings) { st.WizardDone = true }); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, struct{}{})
}

// firstNonEmpty returns a, or b when a is empty (same as firstOf).
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// dnsNow answers the wizard's records table: what public DNS holds now for
// each record the panel would create, and when the zone's registration ends.
func (s *Server) dnsNow(w http.ResponseWriter, r *http.Request) {
	auditSkip(r)
	var in struct {
		Zone    string   `json:"zone"`
		Records []Record `json:"records"`
	}
	if !readJSON(w, r, 8<<10, &in) {
		return
	}
	zone := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Zone), "."))
	if !zoneRe.MatchString(zone) || len(in.Records) > 20 {
		fail(w, http.StatusBadRequest, "\""+zone+"\" is not a domain name")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	type now struct {
		Now   []string `json:"now"`
		Check string   `json:"check"`
	}
	out := make([]now, len(in.Records))
	var wg sync.WaitGroup
	for i, rec := range in.Records {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _ := lookupRecord(ctx, zone, rec)
			out[i] = now{Now: append([]string{}, got...), Check: checkRecord(ctx, zone, rec)}
		}()
	}
	until := s.rdapUntil(ctx, zone)
	wg.Wait()
	res := map[string]any{"records": out}
	if !until.IsZero() {
		res["reg_until"] = until
	}
	ok(w, res)
}
