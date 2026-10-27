package keenetic

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/a3app/loft/internal/version"
)

//go:embed web/index.html
var page embed.FS

// Allow decides whether the browser holds a Keenetic admin session.
// ok is false with a login URL when the operator must sign in.
// A non-nil error means the web admin did not answer.
type Allow func(r *http.Request) (ok bool, login string, err error)

// Panel is the LAN page.
type Panel struct {
	Store   *Store
	Sup     *Sup
	Allow   Allow
	Check   func(context.Context) (Update, error)
	Install func(context.Context, Update) error
}

// Handler is the page and the JSON API. Mutations need header X-Loft: 1
// so a foreign page cannot post with the Keenetic cookie.
func (p *Panel) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", p.home)
	m.HandleFunc("/api/status", p.status)
	m.HandleFunc("/api/conn", p.conn)
	m.HandleFunc("/api/conn/toggle", p.toggle)
	m.HandleFunc("/api/import", p.imp)
	m.HandleFunc("/api/log", p.log)
	m.HandleFunc("/api/package/check", p.check)
	m.HandleFunc("/api/package/auto", p.auto)
	m.HandleFunc("/api/package/update", p.install)
	return p.gate(m)
}

func (p *Panel) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Header.Get("X-Loft") != "1" {
			http.Error(w, "origin", http.StatusForbidden)
			return
		}
		ok, login, err := p.allow(r)
		if err != nil {
			http.Error(w, "Keenetic session check failed", http.StatusServiceUnavailable)
			return
		}
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"login": login})
				return
			}
			http.Redirect(w, r, login, http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Panel) allow(r *http.Request) (bool, string, error) {
	if p.Allow != nil {
		return p.Allow(r)
	}
	f, err := p.Store.Load()
	if err != nil {
		return false, "", err
	}
	ok, err := Check(r.Context(), f.Keenetic.Auth, r.Header.Get("Cookie"), r.URL.Query().Get("ndw4"))
	if err != nil {
		return false, "", err
	}
	if ok {
		return true, "", nil
	}
	return false, LoginURL(r), nil
}

func (p *Panel) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := page.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "page", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

type listItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Enabled  bool   `json:"enabled"`
	Opkg     int    `json:"opkg"`
	OpkgName string `json:"opkg_name"`
	State    string `json:"state"`
	Front    string `json:"front"`
	Meta     string `json:"meta"`
}

func (p *Panel) status(w http.ResponseWriter, r *http.Request) {
	f, err := p.Store.Load()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	up := Update{Arch: AssetArch()}
	if p.Sup != nil {
		up = p.Sup.LastUpdate()
		if up.Arch == "" {
			up.Arch = AssetArch()
		}
		up.Current = strings.TrimPrefix(version.Version, "v")
		if up.Current == "" {
			up.Current = "dev"
		}
	}
	var list []listItem
	for _, c := range f.Connections {
		ndm, _, _ := Names(c.Opkg)
		snap := Snap{}
		if p.Sup != nil {
			snap = p.Sup.Snap(c.ID)
		}
		state := snap.State
		if !c.Enabled {
			state = "off"
		}
		if state == "" {
			state = "off"
		}
		front := snap.Front
		if front == "" {
			front = c.Connect
		}
		if front == "" && len(c.Fronts) > 0 {
			front = c.Fronts[0]
		}
		if front == "" {
			front = c.Kind
		}
		list = append(list, listItem{
			ID: c.ID, Name: c.Name, Kind: c.Kind, Enabled: c.Enabled,
			Opkg: c.Opkg, OpkgName: ndm, State: state, Front: front,
			Meta: c.Kind + " · " + front + " · " + ndm,
		})
	}
	if list == nil {
		list = []listItem{}
	}
	writeJSON(w, map[string]any{
		"version":       version.Line("loft"),
		"arch":          AssetArch(),
		"check_updates": f.Keenetic.CheckUpdates,
		"update":        up,
		"connections":   list,
	})
}

func (p *Panel) conn(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		id := r.URL.Query().Get("id")
		f, err := p.Store.Load()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		for _, c := range f.Connections {
			if c.ID == id {
				var snap Snap
				if p.Sup != nil {
					snap = p.Sup.Snap(id)
				}
				writeJSON(w, map[string]any{"conn": c, "snap": snap})
				return
			}
		}
		http.NotFound(w, r)
	case http.MethodPost:
		var c Conn
		if err := decodeJSON(r, &c, 1<<20); err != nil {
			http.Error(w, "json", http.StatusBadRequest)
			return
		}
		saved, err := p.Store.Put(c, true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p.changed()
		writeJSON(w, saved)
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if _, err := p.Store.Delete(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p.changed()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (p *Panel) toggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeJSON(r, &body, 1<<16); err != nil {
		http.Error(w, "json", http.StatusBadRequest)
		return
	}
	err := p.Store.Update(func(f *File) error {
		for i := range f.Connections {
			if f.Connections[i].ID == body.ID {
				f.Connections[i].Enabled = body.Enabled
				return nil
			}
		}
		return fmt.Errorf("not found")
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.changed()
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) imp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	text, err := importBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := ImportText(text)
	if err != nil && len(res.Added) == 0 {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var added []Conn
	for _, c := range res.Added {
		c.ID = ""
		c.Opkg = -1
		c.Enabled = true
		saved, err := p.Store.Put(c, false)
		if err != nil {
			res.Skipped = append(res.Skipped, c.Name+": "+err.Error())
			continue
		}
		added = append(added, saved)
	}
	res.Added = added
	p.changed()
	writeJSON(w, res)
}

func importBody(r *http.Request) (string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			return "", err
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			return "", err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 8<<20))
		if err != nil {
			return "", err
		}
		if looksImage(hdr.Filename, hdr.Header.Get("Content-Type"), b) {
			return TextFromImage(b)
		}
		return string(b), nil
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(r, &body, 4<<20); err != nil {
		return "", err
	}
	return body.Text, nil
}

func (p *Panel) log(w http.ResponseWriter, r *http.Request) {
	var lines []string
	if p.Sup != nil && p.Sup.Ring != nil {
		lines = p.Sup.Ring.Lines()
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, lines)
}

func (p *Panel) check(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	fn := p.Check
	if fn == nil {
		fn = func(ctx context.Context) (Update, error) {
			return LatestRelease(ctx, AssetArch())
		}
	}
	up, err := fn(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	up.Current = strings.TrimPrefix(version.Version, "v")
	if p.Sup != nil {
		p.Sup.SetUpdate(up)
	}
	writeJSON(w, up)
}

func (p *Panel) auto(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := decodeJSON(r, &body, 1<<12); err != nil {
		http.Error(w, "json", http.StatusBadRequest)
		return
	}
	if err := p.Store.SetAuto(body.On); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) install(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	var up Update
	if p.Sup != nil {
		up = p.Sup.LastUpdate()
	}
	if up.IPK == "" || !up.Ready {
		fn := p.Check
		if fn == nil {
			fn = func(ctx context.Context) (Update, error) {
				return LatestRelease(ctx, AssetArch())
			}
		}
		var err error
		up, err = fn(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	if !up.Ready {
		http.Error(w, "already current", http.StatusConflict)
		return
	}
	fn := p.Install
	if fn == nil {
		fn = func(ctx context.Context, u Update) error {
			dst := "/opt/tmp/loft-keenetic.ipk"
			if err := DownloadIPK(ctx, u, dst); err != nil {
				return err
			}
			return OpkgInstall(ctx, dst)
		}
	}
	if err := fn(r.Context(), up); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) changed() {
	if p.Sup != nil {
		p.Sup.Reconcile()
	}
}

// Serve listens on the LAN addresses and loopback until ctx is done.
func (p *Panel) Serve(ctx context.Context) error {
	f, err := p.Store.Load()
	if err != nil {
		return err
	}
	addrs := ListenAddrs(f.Listen.HTTP, PrivateIPv4())
	var servers []*http.Server
	h := p.Handler()
	for _, a := range addrs {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			logErr("listen %s: %v", a, err)
			continue
		}
		s := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
		servers = append(servers, s)
		go func() { _ = s.Serve(ln) }()
		logErr("http %s", a)
	}
	if len(servers) == 0 {
		return fmt.Errorf("no listen address")
	}
	<-ctx.Done()
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(c)
	}
	return nil
}

func logErr(format string, args ...any) {
	// panel avoids importing log in the signature noise of tests; the
	// supervisor logs the same facts. Use the standard logger.
	fmt.Printf("loft: "+format+"\n", args...)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v any, n int64) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, n)).Decode(v)
}
