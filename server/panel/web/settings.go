package web

// settings.go is the Settings screen and the panel's background watcher.
// The screen covers sign-in session limits, the default language, how
// connection names in issued .loft files are built, the server's time zone
// (changed through panel-adm, the root helper), and event letters sent by
// e-mail through an SMTP server. Watch runs once a minute for the life of
// the panel: it checks domains and certificates, gives the mail system its
// certificates, runs backup checks, records presence, and sends those
// letters.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/store"
)

// idleChoices and maxChoices are the allowed session limits: how long an
// unused session lives, and how long any session lives at most.
var (
	idleChoices = map[string]time.Duration{"30m": 30 * time.Minute, "8h": 8 * time.Hour, "off": 0}
	maxChoices  = map[string]time.Duration{"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}
)

// Events a letter can be about.
const (
	EventCert   = "cert"   // a certificate ends in 14 days
	EventUpdate = "update" // an update was installed or rolled back
	EventNode   = "node"   // loft-node is not running
	EventBackup = "backup" // a scheduled backup failed
)

// events lists every event kind, in the order the UI shows them.
var events = []string{EventCert, EventUpdate, EventNode, EventBackup}

// SessionLimits turns the stored choice into durations (defaults 30m, 7d).
// "off" idles until Max.
func SessionLimits(st store.Settings) (idle, max time.Duration) {
	max, ok := maxChoices[st.SessionMax]
	if !ok {
		max = maxChoices["7d"]
	}
	idle, ok = idleChoices[st.SessionIdle]
	if !ok {
		idle = idleChoices["30m"]
	}
	if idle == 0 {
		idle = max
	}
	return idle, max
}

// notifyJSON is the e-mail settings as the UI sees them. The stored SMTP
// password is never sent out, only whether there is one.
type notifyJSON struct {
	To          string   `json:"to"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Security    string   `json:"security"`
	User        string   `json:"user"`
	Password    string   `json:"password,omitempty"` // in: "" keeps the stored one
	HasPassword bool     `json:"has_password"`
	From        string   `json:"from"`
	Events      []string `json:"events"`
}

// settingsJSON is the Settings screen's data. Node and Panel are the domain
// names of the node and the panel (read-only here; the wizard sets them).
type settingsJSON struct {
	Node         string             `json:"node"`
	Panel        string             `json:"panel"`
	SessionIdle  string             `json:"session_idle"`
	SessionMax   string             `json:"session_max"`
	Lang         string             `json:"lang"`
	LoftOutbound store.LoftOutbound `json:"loft_outbound"`
	PortActivity bool               `json:"port_activity"`
	Notify       notifyJSON         `json:"notify"`
}

// settingsView turns stored settings into settingsJSON, filling in
// defaults. The store keeps the events that are off, the UI wants those
// that are on.
func settingsView(st store.Settings) settingsJSON {
	n := st.Notify
	on := []string{}
	for _, e := range events {
		if !slices.Contains(n.Off, e) {
			on = append(on, e)
		}
	}
	return settingsJSON{Node: st.Domain, Panel: firstOf(st.PanelDomain, st.Domain),
		SessionIdle: firstOf(st.SessionIdle, "30m"), SessionMax: firstOf(st.SessionMax, "7d"), Lang: firstOf(st.Lang, "ru"),
		LoftOutbound: st.LoftOutboundView(), PortActivity: st.PortActivity,
		Notify: notifyJSON{To: n.To, Host: n.Host, Port: n.Port, Security: firstOf(n.Security, "starttls"), User: n.User,
			HasPassword: n.Password != "", From: n.From, Events: on}}
}

// settingsGet returns the Settings screen's data.
func (s *Server) settingsGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.Settings.Get()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, settingsView(st))
}

// settingsPanel saves the session limits, language, .loft naming and the
// port LED style. New session limits are handed to the session store at
// once.
func (s *Server) settingsPanel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionIdle  string              `json:"session_idle"`
		SessionMax   string              `json:"session_max"`
		Lang         string              `json:"lang"`
		LoftOutbound *store.LoftOutbound `json:"loft_outbound"`
		PortActivity *bool               `json:"port_activity"`
	}
	if !readJSON(w, r, 4<<10, &req) {
		return
	}
	if _, ok := idleChoices[req.SessionIdle]; !ok {
		fail(w, http.StatusBadRequest, "bad settings")
		return
	}
	if _, ok := maxChoices[req.SessionMax]; !ok || !oneOf(req.Lang, "ru", "en", "zh") {
		fail(w, http.StatusBadRequest, "bad settings")
		return
	}
	st, err := s.Settings.Update(func(st *store.Settings) {
		st.SessionIdle, st.SessionMax, st.Lang = req.SessionIdle, req.SessionMax, req.Lang
		if req.LoftOutbound != nil {
			o := *req.LoftOutbound
			if !o.Host && !o.Member && !o.Front {
				o.Front = true
			}
			st.LoftOutbound = &o
		}
		if req.PortActivity != nil {
			st.PortActivity = *req.PortActivity
		}
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Sessions.SetLimits(SessionLimits(st))
	ok(w, settingsView(st))
}

// timezoneGet returns the server's time zone from the helper.
func (s *Server) timezoneGet(w http.ResponseWriter, r *http.Request) {
	var tz admproto.Timezone
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpTimezone, nil, &tz); err != nil {
		admFail(w, err)
		return
	}
	ok(w, tz)
}

// timezoneSet changes the server's time zone through the helper.
func (s *Server) timezoneSet(w http.ResponseWriter, r *http.Request) {
	var req admproto.Timezone
	if !readJSON(w, r, 4<<10, &req) {
		return
	}
	var tz admproto.Timezone
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpTimezoneSet, admproto.Timezone{Zone: req.Zone}, &tz); err != nil {
		admFail(w, err)
		return
	}
	s.logf("time zone %s by %s", tz.Zone, userOf(r).Login)
	ok(w, tz)
}

// check validates e-mail settings. Empty recipient and server mean letters
// are switched off, which is always valid.
func (n notifyJSON) check() error {
	if n.To == "" && n.Host == "" {
		return nil // switched off
	}
	if _, err := mail.ParseAddress(n.To); err != nil {
		return errors.New("notify: recipient is not an e-mail address")
	}
	if _, err := mail.ParseAddress(n.From); err != nil {
		return errors.New("notify: sender is not an e-mail address")
	}
	if n.Host == "" || strings.ContainsAny(n.Host, " /:") {
		return errors.New("notify: SMTP server is not a host name")
	}
	if n.Port < 1 || n.Port > 65535 {
		return errors.New("notify: SMTP port is out of range")
	}
	if !oneOf(n.Security, "starttls", "tls", "none") {
		return errors.New("notify: bad SMTP security")
	}
	for _, e := range n.Events {
		if !slices.Contains(events, e) {
			return fmt.Errorf("notify: unknown event %q", e)
		}
	}
	return nil
}

// settingsNotify saves the e-mail settings. An empty password keeps the
// stored one; an empty SMTP user removes it.
func (s *Server) settingsNotify(w http.ResponseWriter, r *http.Request) {
	var req notifyJSON
	if !readJSON(w, r, 8<<10, &req) {
		return
	}
	req.To, req.From, req.Host, req.User = strings.TrimSpace(req.To), strings.TrimSpace(req.From), strings.TrimSpace(req.Host), strings.TrimSpace(req.User)
	if err := req.check(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	st, err := s.Settings.Update(func(st *store.Settings) {
		n := &st.Notify
		n.To, n.Host, n.Port, n.Security, n.User, n.From = req.To, req.Host, req.Port, req.Security, req.User, req.From
		if req.Password != "" {
			n.Password = req.Password
		}
		if req.User == "" {
			n.Password = ""
		}
		n.Off = nil
		for _, e := range events {
			if !slices.Contains(req.Events, e) {
				n.Off = append(n.Off, e)
			}
		}
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, settingsView(st))
}

// settingsNotifyTest sends a test letter with the saved settings, so the
// operator can see that letters arrive.
func (s *Server) settingsNotifyTest(w http.ResponseWriter, r *http.Request) {
	st, err := s.Settings.Get()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.Notify.To == "" || st.Notify.Host == "" {
		fail(w, http.StatusBadRequest, "notify: save the recipient and the SMTP server first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.sendMail(ctx, st, "Test letter", "This is a test letter from loft-panel on "+firstOf(st.PanelDomain, st.Domain)+".\n\nEvent letters will come from here."); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	ok(w, struct{}{})
}

// sendMail sends one plain-text letter through the configured server.
// Security "tls" connects with TLS from the start, "starttls" upgrades a
// plain connection, "none" stays plain.
func (s *Server) sendMail(ctx context.Context, st store.Settings, subject, body string) error {
	n := st.Notify
	if s.SendMail != nil {
		return s.SendMail(n, subject, body)
	}
	addr := net.JoinHostPort(n.Host, strconv.Itoa(n.Port))
	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if n.Security == "tls" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: n.Host})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("SMTP %s: %v", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, n.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP %s: %v", addr, err)
	}
	defer c.Close()
	if err := c.Hello("loft-panel"); err != nil {
		return fmt.Errorf("SMTP: %v", err)
	}
	if n.Security == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: n.Host}); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %v", err)
		}
	}
	if n.User != "" {
		// smtp.PlainAuth refuses to send the password without TLS, except to localhost.
		if err := c.Auth(smtp.PlainAuth("", n.User, n.Password, n.Host)); err != nil {
			return fmt.Errorf("SMTP login: %v", err)
		}
	}
	if err := c.Mail(n.From); err != nil {
		return fmt.Errorf("SMTP sender: %v", err)
	}
	if err := c.Rcpt(n.To); err != nil {
		return fmt.Errorf("SMTP recipient: %v", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP: %v", err)
	}
	host := firstOf(st.PanelDomain, st.Domain)
	msg := "From: loft-panel <" + n.From + ">\r\nTo: <" + n.To + ">\r\nSubject: [loft " + host + "] " + subject +
		"\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"
	if _, err := wc.Write([]byte(msg)); err != nil {
		return fmt.Errorf("SMTP: %v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("SMTP: %v", err)
	}
	return c.Quit()
}

// eventOn reports whether letters are set up and event e is not switched
// off.
func eventOn(st store.Settings, e string) bool {
	return st.Notify.To != "" && st.Notify.Host != "" && !slices.Contains(st.Notify.Off, e)
}

// Watch sends event letters: a certificate ending in 14 days, an update
// installed or rolled back, loft-node down for two checks in a row.
func (s *Server) Watch(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	down := 0
	for {
		st, err := s.Settings.Get()
		if err == nil {
			s.watchCerts(ctx, st)
			s.watchDomains(ctx)
			s.syncMailCerts(ctx, "renewal", nil)
			s.watchBackups(ctx)
			if ns, err := s.readPresence(ctx); err == nil {
				s.track(ns) // last seen, even while nobody looks at the panel
			}
			s.watchUpdate(ctx, st)
			down = s.watchNode(ctx, st, down)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// notify sends one event letter and, only if it was sent, calls mark to
// record that in the settings, so the same event is not mailed again.
func (s *Server) notify(ctx context.Context, st store.Settings, subject, body string, mark func(*store.Notify)) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.sendMail(cctx, st, subject, body); err != nil {
		s.logf("notify %q: %v", subject, err)
		return
	}
	s.Settings.Update(func(x *store.Settings) { mark(&x.Notify) })
}

// watchCerts writes when the node's or the panel's certificate ends within
// 14 days (renewal starts at 30, so it has been failing), once per
// certificate.
func (s *Server) watchCerts(ctx context.Context, st store.Settings) {
	if !eventOn(st, EventCert) {
		return
	}
	for name, until := range map[string]time.Time{st.Domain: st.CertUntil, st.PanelDomain: st.PanelCertUntil} {
		if name == "" || until.IsZero() || time.Until(until) > 14*24*time.Hour || st.Notify.SentCert[name].Equal(until) {
			continue
		}
		name, until := name, until
		s.notify(ctx, st, "Certificate for "+name+" ends "+until.Format("2006-01-02"),
			fmt.Sprintf("The Let's Encrypt certificate for %s ends on %s.\nThe panel renews it 30 days ahead; renewal has not succeeded yet. Check that %s points at this server and port 80 is open.", name, until.Format("2006-01-02 15:04 MST"), name),
			func(n *store.Notify) {
				if n.SentCert == nil {
					n.SentCert = map[string]time.Time{}
				}
				n.SentCert[name] = until
			})
	}
}

// watchUpdate writes once when an update job has finished or failed. A job
// that ended more than a day ago is only marked as seen, so a fresh panel
// does not mail about old updates.
func (s *Server) watchUpdate(ctx context.Context, st store.Settings) {
	if !eventOn(st, EventUpdate) || s.Adm == nil {
		return
	}
	var j admproto.Job
	if err := s.Adm("notify").Call(ctx, admproto.OpJobGet, nil, &j); err != nil || j.Kind != "update" || j.State == "running" || j.ID == st.Notify.SentJob {
		return
	}
	if time.Since(j.Ended) > 24*time.Hour { // an old job seen for the first time
		s.Settings.Update(func(x *store.Settings) { x.Notify.SentJob = j.ID })
		return
	}
	subject, body := "Updated to "+strings.TrimPrefix(j.Tag, "v"), "The node and the panel run "+strings.TrimPrefix(j.Tag, "v")+" now."
	if j.State != "done" {
		subject, body = "Update to "+strings.TrimPrefix(j.Tag, "v")+" failed", "The update did not finish: "+j.Error+"\n\nThe job log is on the Packages screen."
	}
	s.notify(ctx, st, subject, body, func(n *store.Notify) { n.SentJob = j.ID })
}

// watchNode counts consecutive checks in which loft-node is not active and
// writes on the second one (so a quick restart does not cause a letter). It
// writes only once per outage: NodeDownAt is cleared when the node runs
// again. It returns the new count.
func (s *Server) watchNode(ctx context.Context, st store.Settings, down int) int {
	if !eventOn(st, EventNode) || s.Adm == nil {
		return 0
	}
	var ns admproto.NodeStatus
	if err := s.Adm("notify").Call(ctx, admproto.OpNodeStatus, nil, &ns); err != nil {
		return down
	}
	if ns.Active == "active" {
		if !st.Notify.NodeDownAt.IsZero() {
			s.Settings.Update(func(x *store.Settings) { x.Notify.NodeDownAt = time.Time{} })
		}
		return 0
	}
	down++
	if down == 2 && st.Notify.NodeDownAt.IsZero() {
		s.notify(ctx, st, "loft-node is "+ns.Active, "loft-node on this server is "+ns.Active+" for more than a minute: members cannot connect.\nThe panel will not write again until the node has been running.",
			func(n *store.Notify) { n.NodeDownAt = time.Now().UTC() })
	}
	return down
}
