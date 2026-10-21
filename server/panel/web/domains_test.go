package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a3app/loft/server/panel/store"
)

// fakeDNS is a provider in memory.
type fakeDNS struct {
	mu   sync.Mutex
	recs map[string][]Record
	n    int
}

func (f *fakeDNS) Check(context.Context) error             { return nil }
func (f *fakeDNS) Zones(context.Context) ([]string, error) { return []string{"example.net"}, nil }
func (f *fakeDNS) Records(_ context.Context, z string) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if z != "example.net" {
		return nil, io.EOF
	}
	return append([]Record(nil), f.recs[z]...), nil
}
func (f *fakeDNS) Create(_ context.Context, z string, r Record) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	r.ID = strconv.Itoa(f.n)
	f.recs[z] = append(f.recs[z], r)
	return r.ID, nil
}
func (f *fakeDNS) Update(_ context.Context, z string, r Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.recs[z] {
		if f.recs[z][i].ID == r.ID {
			f.recs[z][i] = r
		}
	}
	return nil
}
func (f *fakeDNS) Delete(_ context.Context, z, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keep []Record
	for _, r := range f.recs[z] {
		if r.ID != id {
			keep = append(keep, r)
		}
	}
	f.recs[z] = keep
	return nil
}

func TestDomainsZonesRecords(t *testing.T) {
	e := newEnv(t)
	e.srv.SetDomainsFile(t.TempDir())
	fd := &fakeDNS{recs: map[string][]Record{}}
	e.srv.dom.provider = func(dnsProv) DNSProvider { return fd }
	c, csrf := e.owner()
	e.srv.Settings.Update(func(st *store.Settings) { st.Domain = "vpn.example.net" })
	p := e.do("POST", "/api/domains/provider", map[string]string{"kind": "cloudflare", "token": "tok-1234567890"}, c, csrf)
	if p.code != 200 {
		t.Fatalf("provider %d %v", p.code, p.body)
	}
	pid := p.body["id"].(string)
	if r := e.do("POST", "/api/domains/zone", map[string]any{"name": "nowhere.org", "provider": pid}, c, csrf); r.code != 400 {
		t.Fatalf("unknown zone at provider: %d", r.code)
	}
	if r := e.do("POST", "/api/domains/zone", map[string]any{"name": "example.net", "provider": pid, "create": []string{"addr", "caa"}}, c, csrf); r.code != 200 {
		t.Fatalf("zone %d %v", r.code, r.body)
	}
	rr := e.do("GET", "/api/domains/records?zone=example.net", nil, c, "")
	recs := rr.body["records"].([]any)
	var caa map[string]any
	for _, x := range recs {
		if m := x.(map[string]any); m["type"] == "CAA" {
			caa = m
		}
	}
	if caa == nil || caa["ours"] != true {
		t.Fatalf("records %v", recs)
	}
	if r := e.do("POST", "/api/domains/record", map[string]any{"zone": "example.net", "record": map[string]any{"type": "A", "name": "www", "value": "not-ip"}}, c, csrf); r.code != 400 {
		t.Fatalf("bad A accepted: %d", r.code)
	}
	if r := e.do("POST", "/api/domains/record", map[string]any{"zone": "example.net", "record": map[string]any{"type": "MX", "name": "@", "value": "mail.example.net", "priority": 10}}, c, csrf); r.code != 200 {
		t.Fatalf("MX %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/domains/record", map[string]any{"zone": "example.net", "record": map[string]any{"id": caa["id"]}, "delete": true}, c, csrf); r.code != 200 {
		t.Fatalf("delete %d", r.code)
	}
	if n := len(fd.recs["example.net"]); n < 1 {
		t.Fatal("no records")
	}
	if r := e.do("POST", "/api/domains/provider", map[string]any{"id": pid, "delete": true}, c, csrf); r.code != 409 {
		t.Fatalf("provider in use deleted: %d", r.code)
	}
	// A manual zone keeps its list and checks it in DNS.
	if r := e.do("POST", "/api/domains/zone", map[string]any{"name": "example.org", "provider": "manual"}, c, csrf); r.code != 200 {
		t.Fatalf("manual %d", r.code)
	}
	e.do("POST", "/api/domains/record", map[string]any{"zone": "example.org", "record": map[string]any{"type": "TXT", "name": "@", "value": "v=spf1 -all"}}, c, csrf)
	if r := e.do("GET", "/api/domains/records?zone=example.org", nil, c, ""); len(r.body["records"].([]any)) != 1 {
		t.Fatalf("manual records %v", r.body)
	}
	v := e.do("GET", "/api/domains", nil, c, "")
	if len(v.body["zones"].([]any)) != 2 || !strings.Contains(v.raw(), "tok-…7890") || strings.Contains(v.raw(), "tok-1234567890") {
		t.Fatalf("view %s", v.raw())
	}
	// A node certificate must cover the node's name.
	if err := e.srv.useCert(context.Background(), "admin", []string{"other.example.net"}, "upload", "node", "", "x", "y", time.Now()); err == nil {
		t.Fatal("node cert without the node name accepted")
	}
}

func TestWizardDNSCreatesZoneBeforeNames(t *testing.T) {
	e := newEnv(t)
	e.srv.SetDomainsFile(t.TempDir())
	fd := &fakeDNS{recs: map[string][]Record{"example.net": {}}}
	e.srv.dom.provider = func(dnsProv) DNSProvider { return fd }
	c, csrf := e.owner()
	if r := e.do("POST", "/api/domains/provider", map[string]any{"kind": "cloudflare", "token": "tok-1234567890", "check_only": true}, c, csrf); r.code != 200 {
		t.Fatalf("check_only %d %v", r.code, r.body)
	}
	if r := e.do("GET", "/api/domains", nil, c, ""); len(r.body["providers"].([]any)) != 0 {
		t.Fatalf("check_only stored a provider: %v", r.body)
	}
	p := e.do("POST", "/api/domains/provider", map[string]string{"kind": "cloudflare", "token": "tok-1234567890"}, c, csrf)
	if p.code != 200 {
		t.Fatalf("provider %d %v", p.code, p.body)
	}
	pid := p.body["id"].(string)
	again := e.do("POST", "/api/domains/provider", map[string]string{"kind": "cloudflare", "token": "tok-1234567890"}, c, csrf)
	if again.code != 200 || again.body["id"] != pid {
		t.Fatalf("same token must reuse the provider: %d %v", again.code, again.body)
	}
	// Names are not in settings yet (wizard step 2 creates records first).
	if r := e.do("POST", "/api/domains/zone", map[string]any{
		"name": "example.net", "provider": pid, "create": []string{"addr", "caa"},
		"hosts": []string{"vpn.example.net", "panel.example.net"},
	}, c, csrf); r.code != 200 {
		t.Fatalf("zone %d %v", r.code, r.body)
	}
	rr := e.do("GET", "/api/domains/records?zone=example.net", nil, c, "")
	recs := rr.body["records"].([]any)
	var caa, vpn bool
	for _, x := range recs {
		m := x.(map[string]any)
		if m["type"] == "CAA" {
			caa = true
		}
		if m["type"] == "A" && m["name"] == "vpn" {
			vpn = true
		}
	}
	if !caa {
		t.Fatalf("CAA missing: %v", recs)
	}
	if n := len(fd.recs["example.net"]); n < 1 {
		t.Fatalf("provider got no records: %d", n)
	}
	_ = vpn // A records only exist when ServerIPs() sees a public address
}

func TestCloudflareAndDigitalOcean(t *testing.T) {
	var cfSeen []string
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfSeen = append(cfSeen, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(403)
			return
		}
		switch {
		case r.URL.Path == "/zones":
			io.WriteString(w, `{"result":[{"id":"z1","name":"example.net"}]}`)
		case r.URL.Path == "/zones/z1/dns_records" && r.Method == "GET":
			io.WriteString(w, `{"result":[{"id":"r1","type":"CAA","name":"example.net","ttl":1,"data":{"flags":0,"tag":"issue","value":"letsencrypt.org"}},{"id":"r2","type":"A","name":"vpn.example.net","content":"203.0.113.10","ttl":300}]}`)
		case r.URL.Path == "/zones/z1/dns_records" && r.Method == "POST":
			var x map[string]any
			json.NewDecoder(r.Body).Decode(&x)
			if x["name"] != "www.example.net" {
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, `{"result":{"id":"r3"}}`)
		default:
			io.WriteString(w, `{}`)
		}
	}))
	defer cf.Close()
	c := &Cloudflare{Token: "t", Base: cf.URL}
	ctx := context.Background()
	recs, err := c.Records(ctx, "example.net")
	if err != nil || len(recs) != 2 || recs[0].Value != `0 issue "letsencrypt.org"` || recs[0].Name != "@" || recs[1].Name != "vpn" || recs[0].TTL != 0 {
		t.Fatalf("%+v %v", recs, err)
	}
	if id, err := c.Create(ctx, "example.net", Record{Type: "A", Name: "www", Value: "203.0.113.11"}); err != nil || id != "r3" {
		t.Fatalf("%s %v", id, err)
	}
	if err := (&Cloudflare{Token: "bad", Base: cf.URL}).Check(ctx); err == nil {
		t.Fatal("bad token passed")
	}
	do := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/records") && r.Method == "GET":
			io.WriteString(w, `{"domain_records":[{"id":7,"type":"MX","name":"@","data":"mail.example.net","priority":10,"ttl":1800}]}`)
		case strings.HasSuffix(r.URL.Path, "/records"):
			io.WriteString(w, `{"domain_record":{"id":8}}`)
		}
	}))
	defer do.Close()
	d := &DigitalOcean{Token: "t", Base: do.URL}
	if recs, err := d.Records(ctx, "example.net"); err != nil || recs[0].Priority != 10 || recs[0].ID != "7" {
		t.Fatalf("%+v %v", recs, err)
	}
	if id, _ := d.Create(ctx, "example.net", Record{Type: "TXT", Name: "@", Value: "x"}); id != "8" {
		t.Fatalf("do id %s", id)
	}
}
