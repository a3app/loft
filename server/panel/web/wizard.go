package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

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

type checkDomainJSON struct {
	Name      string   `json:"name"`
	IPs       []string `json:"ips"`
	ServerIPs []string `json:"server_ips"`
	Match     bool     `json:"match"`
	Error     string   `json:"error,omitempty"`
}

// domainCheck resolves name and compares with this server's addresses.
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

// issueAndInstall runs the preflight, ACME, and cert.install for target node
// or panel; it records the expiry for renewal.
func (s *Server) issueAndInstall(ctx context.Context, by, name, email, target string) (time.Time, error) {
	if s.ACME == nil {
		return time.Time{}, errors.New("certificates are not configured")
	}
	if err := s.ACME.Preflight(ctx, name); err != nil {
		return time.Time{}, err
	}
	is, err := s.ACME.Issue(ctx, name, email)
	if err != nil {
		return time.Time{}, err
	}
	if err := s.Adm(by).Call(ctx, admproto.OpCertInstall, admproto.CertInstall{Fullchain: is.Fullchain, Key: is.Key, Target: target}, nil); err != nil {
		return time.Time{}, fmt.Errorf("install: %w", admErr(err))
	}
	s.Settings.Update(func(st *store.Settings) {
		if target == "panel" {
			st.PanelCertUntil = is.NotAfter
		} else {
			st.CertUntil, st.Domain = is.NotAfter, name
		}
		if email != "" {
			st.ACMEEmail = email
		}
	})
	s.logf("certificate for %s until %s", name, is.NotAfter.Format(time.DateOnly))
	return is.NotAfter, nil
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
		var k admproto.IssuedKey
		if err := a.Call(ctx, admproto.OpMemberKey, admproto.MemberKey{ID: in.ID}, &k); err != nil {
			return nil, admErr(err)
		}
		s.persistIssued(k)
		nw.Members = append(nw.Members, netconf.Member{ID: in.ID, Name: firstNonEmpty(in.Name, in.ID), KeySHA256: k.KeySHA256, Authorized: true})
		if err := a.Call(ctx, admproto.OpNetworkApply, admproto.NetworkApply{Text: netconf.RenderCommands(*nw), Expect: n.SHA256}, nil); err != nil {
			return nil, admErr(err)
		}
		var info admproto.NodeInfo
		if err := a.Call(ctx, admproto.OpNodeInfo, nil, &info); err != nil {
			return nil, admErr(err)
		}
		link, file := memberLink(info, k.Key, firstNonEmpty(in.Name, in.ID))
		s.logf("member %s created by %s", in.ID, by)
		svg, _ := qrSVG(link)
		return memberJSON{ID: in.ID, Key: k.Key, Link: link, File: file, QR: svg}, nil
	})
}

// memberLink is the loft:// address (GET front, docs/share.md §2) and a .loft
// file with a GET outbound pinned to the controller key.
func memberLink(in admproto.NodeInfo, key, name string) (string, string) {
	host := in.Host
	link := "loft://" + url.PathEscape(key) + "@" + host + in.GetPath + "?type=get&fp=chrome#" + url.PathEscape(name)
	var b strings.Builder
	fmt.Fprintf(&b, "name: %q\nkind: loft\ntoken: %q\nselect: auto\n", name, key)
	if in.RosterPath != "" {
		fmt.Fprintf(&b, "bootstrap: https://%s%s\n", host, in.RosterPath)
	}
	if in.ControllerPub != "" {
		fmt.Fprintf(&b, "controller_pub: %s\n", in.ControllerPub)
	}
	fmt.Fprintf(&b, "outbounds:\n  - name: get\n    type: get\n    url: https://%s\n    path: %s\n    fingerprint: chrome\n", host, in.GetPath)
	return link, b.String()
}

func (s *Server) wizardDone(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Settings.Update(func(st *store.Settings) { st.WizardDone = true }); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, struct{}{})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
