// Package lanui is the LAN operator page for loft on MikroTik (:8090).
// Keenetic uses clients/Keenetic (many OpkgTun sessions, web-admin cookie).
//
// When the loft client runs as a container on a MikroTik router, there is no
// GUI. This package serves a small web page inside the container (listen.http
// in client.yaml, usually :8090) where the operator sees the status, the log
// tail and can change the key, server URL, front type and uplink interface.
// A "front" is the way loft packs its traffic into HTTPS (get, ws, h2, ...).
// The page also hands out uplink.rsc, a RouterOS script that sends the
// container's traffic out through the chosen WAN. The command that owns the
// session (cmd/loft) supplies the Live and Apply callbacks.
package lanui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/a3app/loft/internal/config"
)

// page holds index.html, the single-page UI, built into the binary.
//
//go:embed index.html
var page embed.FS

// cookieName is the login cookie. srcNet is the container's network on the
// router (veth/bridge, see docs/mikrotik.md); uplink.rsc routes it.
const (
	cookieName = "loft-lan"
	srcNet     = "172.31.20.0/24"
)

// Status is GET /api/status.
type Status struct {
	Version  string `json:"version"`
	SOCKS    string `json:"socks"`
	HTTP     string `json:"http"`
	Bind     string `json:"bind"`
	Uplink   string `json:"uplink"`
	Overlay  string `json:"overlay"`
	TUN      bool   `json:"tun"`
	Front    string `json:"front"`
	Outbound string `json:"outbound"`
	URL      string `json:"url"`
	Path     string `json:"path"`
	TokenSet bool   `json:"token_set"`
	NeedPass bool   `json:"need_pass"`
}

// Patch is POST /api/save.
type Patch struct {
	Token  string `json:"token"`
	URL    string `json:"url"`
	Type   string `json:"type"`
	Path   string `json:"path"`
	Uplink string `json:"uplink"`
}

// Live returns the current session snapshot.
type Live func() Status

// Apply writes yaml, uplink.rsc, and restarts the outbound.
type Apply func(Patch) error

// Ring keeps the last log lines for the page.
type Ring struct {
	mu    sync.Mutex
	lines []string
	max   int
}

// NewRing keeps up to max lines.
func NewRing(max int) *Ring {
	if max <= 0 {
		max = 80
	}
	return &Ring{max: max}
}

// Write appends a log line. log.Logger adds a trailing newline.
func (r *Ring) Write(p []byte) (int, error) {
	s := strings.TrimRight(string(p), "\n")
	if s == "" {
		return len(p), nil
	}
	r.mu.Lock()
	r.lines = append(r.lines, s)
	if len(r.lines) > r.max {
		r.lines = r.lines[len(r.lines)-r.max:]
	}
	r.mu.Unlock()
	return len(p), nil
}

// Snapshot is a copy of the lines, oldest first.
func (r *Ring) Snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

// Server is the LAN page.
type Server struct {
	Addr  string
	Pass  string
	Log   *Ring
	Live  Live
	Apply Apply

	once   sync.Once
	secret string
}

// token returns the random value stored in the login cookie. It is made once
// per process, so restarting loft logs everyone out. If the system random
// source fails, a fixed value is used instead.
func (s *Server) token() string {
	s.once.Do(func() {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			s.secret = "loft-lan"
			return
		}
		s.secret = hex.EncodeToString(b[:])
	})
	return s.secret
}

// ok reports whether the request may see and change settings: always when no
// password is set, otherwise only with the right login cookie (compared in
// constant time so the value cannot be guessed by timing).
func (s *Server) ok(r *http.Request) bool {
	if s.Pass == "" {
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.token())) == 1
}

// mux routes the page and its JSON API.
func (s *Server) mux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", s.page)
	m.HandleFunc("/login", s.login)
	m.HandleFunc("/api/status", s.status)
	m.HandleFunc("/api/log", s.lines)
	m.HandleFunc("/api/save", s.save)
	m.HandleFunc("/api/uplink.rsc", s.rsc)
	return m
}

// Listen serves Addr until the listener dies.
func (s *Server) Listen() error {
	if s.Addr == "" {
		return fmt.Errorf("empty http listen")
	}
	return http.ListenAndServe(s.Addr, s.mux())
}

// page serves index.html at "/" and 404 elsewhere. no-store keeps a browser
// from showing an old copy after an upgrade.
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := page.ReadFile("index.html")
	if err != nil {
		http.Error(w, "page", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// cookieSecure marks the cookie Secure only when the page came over TLS;
// on plain http a Secure cookie would never be sent back.
func cookieSecure(r *http.Request) bool {
	return r != nil && r.TLS != nil
}

// login (POST /login) checks the form password and sets the login cookie.
// With no password configured, login is refused: there is nothing to log in to.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	if s.Pass == "" || subtle.ConstantTimeCompare([]byte(r.Form.Get("password")), []byte(s.Pass)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.token(),
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// status (GET /api/status) returns the live Status. Without a valid login it
// answers 401 with need_pass set, so the page knows to show the password box.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if !s.ok(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(Status{NeedPass: s.Pass != ""})
		return
	}
	st := Status{}
	if s.Live != nil {
		st = s.Live()
	}
	st.NeedPass = false
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
}

// lines (GET /api/log) returns the recent log lines kept in the Ring.
func (s *Server) lines(w http.ResponseWriter, r *http.Request) {
	if !s.ok(r) {
		http.Error(w, "forbidden", http.StatusUnauthorized)
		return
	}
	var out []string
	if s.Log != nil {
		out = s.Log.Snapshot()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// save (POST /api/save) reads a Patch, checks the uplink interface name and
// passes the patch to Apply, which rewrites the config and reconnects.
func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	if !s.ok(r) {
		http.Error(w, "forbidden", http.StatusUnauthorized)
		return
	}
	var p Patch
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil {
		http.Error(w, "json", http.StatusBadRequest)
		return
	}
	p.Uplink = strings.TrimSpace(p.Uplink)
	if err := config.ValidIFName(p.Uplink); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.Apply == nil {
		http.Error(w, "no apply", 500)
		return
	}
	if err := s.Apply(p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rsc (GET /api/uplink.rsc) returns the RouterOS script for the uplink the
// session currently uses, for the operator to /import on the router.
func (s *Server) rsc(w http.ResponseWriter, r *http.Request) {
	if !s.ok(r) {
		http.Error(w, "forbidden", http.StatusUnauthorized)
		return
	}
	wan := ""
	if s.Live != nil {
		wan = s.Live().Uplink
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, UplinkScript(wan, srcNet))
}

// UplinkScript is RouterOS 7: pin 172.31.20.0/24 out through wan.
// Empty wan removes the rule (main table).
func UplinkScript(wan, src string) string {
	if src == "" {
		src = srcNet
	}
	var b strings.Builder
	b.WriteString("# loft — pin container egress. /import <disk>/loft/data/uplink.rsc\n")
	fmt.Fprintf(&b, ":local wan %q\n", wan)
	fmt.Fprintf(&b, ":local src %q\n\n", src)
	b.WriteString(":if ([:len [/routing/table/find where name=\"loft-wan\"]] = 0) do={\n")
	b.WriteString("  /routing/table/add fib name=loft-wan\n}\n")
	b.WriteString(":do { /ip/route/remove [find where comment=\"loft-wan\"] } on-error={}\n")
	b.WriteString(":do { /routing/rule/remove [find where comment=\"loft-wan\"] } on-error={}\n")
	b.WriteString(":if ($wan != \"\") do={\n")
	b.WriteString("  /ip/route/add dst-address=0.0.0.0/0 gateway=$wan routing-table=loft-wan comment=loft-wan\n")
	b.WriteString("  /routing/rule/add src-address=$src action=lookup-only-in-table table=loft-wan comment=loft-wan\n")
	b.WriteString("}\n")
	return b.String()
}

// WriteUplinkRSC stores the script next to client.yaml.
func WriteUplinkRSC(path, wan, src string) error {
	return os.WriteFile(path, []byte(UplinkScript(wan, src)), 0o644)
}
