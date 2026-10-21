package lanui

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUplinkScriptPinsWAN(t *testing.T) {
	s := UplinkScript("ether1", "172.31.20.0/24")
	for _, want := range []string{
		`:local wan "ether1"`,
		`gateway=$wan`,
		`src-address=$src`,
		`table=loft-wan`,
		`lookup-only-in-table`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
}

func TestUplinkScriptRejectsEmptyStillValid(t *testing.T) {
	s := UplinkScript("", "172.31.20.0/24")
	if !strings.Contains(s, `:local wan ""`) {
		t.Fatal(s)
	}
	if strings.Contains(s, `gateway=""`) {
		t.Fatal("empty gateway")
	}
}

func TestWriteUplinkRSC(t *testing.T) {
	p := filepath.Join(t.TempDir(), "uplink.rsc")
	if err := WriteUplinkRSC(p, "lte1", ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `:local wan "lte1"`) {
		t.Fatal(string(b))
	}
}

func TestStatusAndSave(t *testing.T) {
	var got Patch
	s := &Server{
		Live: func() Status {
			return Status{SOCKS: "0.0.0.0:1080", Uplink: "ether1", Overlay: "10.8.0.14", Version: "loft 1.0.22 (LOFT/1.0)"}
		},
		Apply: func(p Patch) error { got = p; return nil },
		Log:   NewRing(8),
	}
	_, _ = s.Log.Write([]byte("socks5 0.0.0.0:1080\n"))
	h := s.mux()

	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var st Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Uplink != "ether1" || st.Overlay != "10.8.0.14" {
		t.Fatalf("%+v", st)
	}

	body := `{"token":"lt_ab","url":"https://vpn.example.net","type":"get","path":"/static/v3/build","uplink":"lte1"}`
	r = httptest.NewRequest(http.MethodPost, "/api/save", strings.NewReader(body))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got.Uplink != "lte1" || got.URL != "https://vpn.example.net" {
		t.Fatalf("%+v", got)
	}

	r = httptest.NewRequest(http.MethodPost, "/api/save", strings.NewReader(`{"uplink":"ether1; reboot"}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("want 400 got %d %s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	page := w.Body.String()
	if w.Code != 200 || !strings.Contains(page, "Исходящий интерфейс") {
		t.Fatalf("page %d", w.Code)
	}
	for _, want := range []string{
		`viewBox="0 0 96 96"`,
		`class="mark"`,
		`--stone: #B7A99A`,
		`--line: rgba(243,237,228,.12)`,
		`http://172.31.20.2:8090/`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q", want)
		}
	}
}

func TestHTTPPass(t *testing.T) {
	s := &Server{Pass: "secret", Live: func() Status { return Status{SOCKS: "x"} }}
	h := s.mux()
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("password=secret"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	ck := w.Result().Cookies()
	if len(ck) == 0 {
		t.Fatal("cookie")
	}
	if ck[0].Secure || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteLaxMode || ck[0].Path != "/" {
		t.Fatalf("http cookie flags %+v", ck[0])
	}
	r = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	r.AddCookie(ck[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestLoginCookieSecureOnTLS(t *testing.T) {
	s := &Server{Pass: "secret"}
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("password=secret"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	s.mux().ServeHTTP(w, r)
	ck := w.Result().Cookies()
	if w.Code != 204 || len(ck) == 0 || !ck[0].Secure || !ck[0].HttpOnly {
		t.Fatalf("tls cookie %d %+v", w.Code, ck)
	}
}
