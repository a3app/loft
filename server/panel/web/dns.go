package web

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DNS providers the Domains screen manages zones through. Names in records
// are relative to the zone ("@" is the zone itself); TTL 0 is the
// provider's automatic value.

// Record is one DNS record.
type Record struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
	TTL      int    `json:"ttl,omitempty"`
}

// DNSProvider is what the panel needs from a provider.
type DNSProvider interface {
	Check(ctx context.Context) error
	Zones(ctx context.Context) ([]string, error)
	Records(ctx context.Context, zone string) ([]Record, error)
	Create(ctx context.Context, zone string, r Record) (string, error)
	Update(ctx context.Context, zone string, r Record) error
	Delete(ctx context.Context, zone string, id string) error
}

// RecordTypes the panel edits.
var RecordTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "CAA", "SRV", "NS"}

func fqdn(name, zone string) string {
	if name == "" || name == "@" {
		return zone
	}
	return name + "." + zone
}

func relName(full, zone string) string {
	full = strings.TrimSuffix(strings.ToLower(full), ".")
	if full == zone {
		return "@"
	}
	return strings.TrimSuffix(full, "."+zone)
}

type httpAPI struct {
	c    *http.Client
	base string
	auth func(*http.Request)
}

func (a httpAPI) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	a.auth(req)
	c := a.c
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("the provider refused the token")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the provider answered HTTP %d: %s", resp.StatusCode, apiMessage(b))
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

func apiMessage(b []byte) string {
	var m struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &m) == nil {
		if len(m.Errors) > 0 {
			return m.Errors[0].Message
		}
		if m.Message != "" {
			return m.Message
		}
	}
	if len(b) > 200 {
		b = b[:200]
	}
	return strings.TrimSpace(string(b))
}

// Cloudflare is the Cloudflare API v4 with an API token (Zone.DNS — Edit).
type Cloudflare struct {
	Token string
	Base  string // tests
	HTTP  *http.Client
	ids   map[string]string // zone → zone id
}

func (c *Cloudflare) api() httpAPI {
	return httpAPI{c: c.HTTP, base: firstNonEmpty(c.Base, "https://api.cloudflare.com/client/v4"), auth: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }}
}

func (c *Cloudflare) Check(ctx context.Context) error {
	var r struct {
		Success bool `json:"success"`
	}
	if err := c.api().do(ctx, "GET", "/user/tokens/verify", nil, &r); err != nil {
		return err
	}
	if !r.Success {
		return errors.New("the provider refused the token")
	}
	return nil
}

func (c *Cloudflare) Zones(ctx context.Context) ([]string, error) {
	var r struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := c.api().do(ctx, "GET", "/zones?per_page=50", nil, &r); err != nil {
		return nil, err
	}
	if c.ids == nil {
		c.ids = map[string]string{}
	}
	var out []string
	for _, z := range r.Result {
		c.ids[z.Name] = z.ID
		out = append(out, z.Name)
	}
	sort.Strings(out)
	return out, nil
}

func (c *Cloudflare) zoneID(ctx context.Context, zone string) (string, error) {
	if id := c.ids[zone]; id != "" {
		return id, nil
	}
	var r struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := c.api().do(ctx, "GET", "/zones?name="+url.QueryEscape(zone), nil, &r); err != nil {
		return "", err
	}
	if len(r.Result) == 0 {
		return "", fmt.Errorf("zone %s is not in this Cloudflare account", zone)
	}
	if c.ids == nil {
		c.ids = map[string]string{}
	}
	c.ids[zone] = r.Result[0].ID
	return r.Result[0].ID, nil
}

type cfRecord struct {
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	Content  string         `json:"content,omitempty"`
	TTL      int            `json:"ttl"`
	Priority *int           `json:"priority,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

func toCF(zone string, r Record) cfRecord {
	x := cfRecord{Type: r.Type, Name: fqdn(r.Name, zone), Content: r.Value, TTL: r.TTL}
	if x.TTL == 0 {
		x.TTL = 1 // automatic
	}
	switch r.Type {
	case "MX":
		p := r.Priority
		x.Priority = &p
	case "CAA": // 0 issue "letsencrypt.org"
		f := strings.SplitN(r.Value, " ", 3)
		if len(f) == 3 {
			flags, _ := strconv.Atoi(f[0])
			x.Content, x.Data = "", map[string]any{"flags": flags, "tag": f[1], "value": strings.Trim(f[2], `"`)}
		}
	case "SRV": // weight port target, priority apart
		f := strings.Fields(r.Value)
		if len(f) == 3 {
			w, _ := strconv.Atoi(f[0])
			port, _ := strconv.Atoi(f[1])
			x.Content, x.Data = "", map[string]any{"priority": r.Priority, "weight": w, "port": port, "target": f[2]}
		}
	}
	return x
}

func (c *Cloudflare) Records(ctx context.Context, zone string) ([]Record, error) {
	id, err := c.zoneID(ctx, zone)
	if err != nil {
		return nil, err
	}
	var r struct {
		Result []struct {
			ID       string         `json:"id"`
			Type     string         `json:"type"`
			Name     string         `json:"name"`
			Content  string         `json:"content"`
			TTL      int            `json:"ttl"`
			Priority int            `json:"priority"`
			Data     map[string]any `json:"data"`
		} `json:"result"`
	}
	if err := c.api().do(ctx, "GET", "/zones/"+id+"/dns_records?per_page=500", nil, &r); err != nil {
		return nil, err
	}
	var out []Record
	for _, x := range r.Result {
		rec := Record{ID: x.ID, Type: x.Type, Name: relName(x.Name, zone), Value: x.Content, Priority: x.Priority, TTL: x.TTL}
		if rec.TTL == 1 {
			rec.TTL = 0
		}
		if x.Type == "CAA" && x.Data != nil {
			rec.Value = fmt.Sprintf("%v %v \"%v\"", x.Data["flags"], x.Data["tag"], x.Data["value"])
		}
		if x.Type == "SRV" && x.Data != nil {
			rec.Value = fmt.Sprintf("%v %v %v", x.Data["weight"], x.Data["port"], x.Data["target"])
		}
		if x.Type == "TXT" {
			rec.Value = strings.Trim(rec.Value, `"`)
		}
		out = append(out, rec)
	}
	return out, nil
}

func (c *Cloudflare) Create(ctx context.Context, zone string, r Record) (string, error) {
	id, err := c.zoneID(ctx, zone)
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := c.api().do(ctx, "POST", "/zones/"+id+"/dns_records", toCF(zone, r), &out); err != nil {
		return "", err
	}
	return out.Result.ID, nil
}

func (c *Cloudflare) Update(ctx context.Context, zone string, r Record) error {
	id, err := c.zoneID(ctx, zone)
	if err != nil {
		return err
	}
	return c.api().do(ctx, "PUT", "/zones/"+id+"/dns_records/"+url.PathEscape(r.ID), toCF(zone, r), nil)
}

func (c *Cloudflare) Delete(ctx context.Context, zone, rid string) error {
	id, err := c.zoneID(ctx, zone)
	if err != nil {
		return err
	}
	return c.api().do(ctx, "DELETE", "/zones/"+id+"/dns_records/"+url.PathEscape(rid), nil, nil)
}

// DigitalOcean is the DigitalOcean API v2 with a token that may write.
type DigitalOcean struct {
	Token string
	Base  string
	HTTP  *http.Client
}

func (d *DigitalOcean) api() httpAPI {
	return httpAPI{c: d.HTTP, base: firstNonEmpty(d.Base, "https://api.digitalocean.com/v2"), auth: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+d.Token) }}
}

func (d *DigitalOcean) Check(ctx context.Context) error {
	return d.api().do(ctx, "GET", "/account", nil, nil)
}

func (d *DigitalOcean) Zones(ctx context.Context) ([]string, error) {
	var r struct {
		Domains []struct {
			Name string `json:"name"`
		} `json:"domains"`
	}
	if err := d.api().do(ctx, "GET", "/domains?per_page=200", nil, &r); err != nil {
		return nil, err
	}
	var out []string
	for _, z := range r.Domains {
		out = append(out, z.Name)
	}
	sort.Strings(out)
	return out, nil
}

type doRecord struct {
	ID       int    `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Data     string `json:"data"`
	Priority *int   `json:"priority,omitempty"`
	Port     *int   `json:"port,omitempty"`
	Weight   *int   `json:"weight,omitempty"`
	TTL      int    `json:"ttl,omitempty"`
	Flags    *int   `json:"flags,omitempty"`
	Tag      string `json:"tag,omitempty"`
}

func toDO(r Record) doRecord {
	x := doRecord{Type: r.Type, Name: firstNonEmpty(r.Name, "@"), Data: r.Value, TTL: r.TTL}
	if x.TTL == 0 {
		x.TTL = 1800
	}
	switch r.Type {
	case "MX":
		p := r.Priority
		x.Priority = &p
	case "CAA":
		f := strings.SplitN(r.Value, " ", 3)
		if len(f) == 3 {
			fl, _ := strconv.Atoi(f[0])
			x.Flags, x.Tag, x.Data = &fl, f[1], strings.Trim(f[2], `"`)
		}
	case "SRV":
		f := strings.Fields(r.Value)
		if len(f) == 3 {
			w, _ := strconv.Atoi(f[0])
			port, _ := strconv.Atoi(f[1])
			p := r.Priority
			x.Weight, x.Port, x.Priority, x.Data = &w, &port, &p, f[2]
		}
	}
	return x
}

func (d *DigitalOcean) Records(ctx context.Context, zone string) ([]Record, error) {
	var r struct {
		Records []doRecord `json:"domain_records"`
	}
	if err := d.api().do(ctx, "GET", "/domains/"+url.PathEscape(zone)+"/records?per_page=200", nil, &r); err != nil {
		return nil, err
	}
	var out []Record
	for _, x := range r.Records {
		rec := Record{ID: strconv.Itoa(x.ID), Type: x.Type, Name: x.Name, Value: x.Data, TTL: x.TTL}
		if x.Priority != nil {
			rec.Priority = *x.Priority
		}
		if x.Type == "CAA" && x.Flags != nil {
			rec.Value = fmt.Sprintf("%d %s \"%s\"", *x.Flags, x.Tag, x.Data)
		}
		if x.Type == "SRV" && x.Port != nil && x.Weight != nil {
			rec.Value = fmt.Sprintf("%d %d %s", *x.Weight, *x.Port, x.Data)
		}
		out = append(out, rec)
	}
	return out, nil
}

func (d *DigitalOcean) Create(ctx context.Context, zone string, r Record) (string, error) {
	var out struct {
		Record doRecord `json:"domain_record"`
	}
	if err := d.api().do(ctx, "POST", "/domains/"+url.PathEscape(zone)+"/records", toDO(r), &out); err != nil {
		return "", err
	}
	return strconv.Itoa(out.Record.ID), nil
}

func (d *DigitalOcean) Update(ctx context.Context, zone string, r Record) error {
	return d.api().do(ctx, "PUT", "/domains/"+url.PathEscape(zone)+"/records/"+url.PathEscape(r.ID), toDO(r), nil)
}

func (d *DigitalOcean) Delete(ctx context.Context, zone, id string) error {
	return d.api().do(ctx, "DELETE", "/domains/"+url.PathEscape(zone)+"/records/"+url.PathEscape(id), nil, nil)
}

// Namecheap is the Namecheap XML API: the user's API key, the API user, and
// the server's public IP on the account's allowlist. It replaces a zone's
// records as a whole (setHosts), so record IDs are positions in getHosts.
type Namecheap struct {
	User, Key, ClientIP string
	Base                string
	HTTP                *http.Client
}

type ncResponse struct {
	Status string `xml:"Status,attr"`
	Errors []struct {
		Number string `xml:"Number,attr"`
		Msg    string `xml:",chardata"`
	} `xml:"Errors>Error"`
	Hosts []struct {
		Name    string `xml:"Name,attr"`
		Type    string `xml:"Type,attr"`
		Address string `xml:"Address,attr"`
		MXPref  int    `xml:"MXPref,attr"`
		TTL     int    `xml:"TTL,attr"`
	} `xml:"CommandResponse>DomainDNSGetHostsResult>host"`
	Domains []struct {
		Name string `xml:"Name,attr"`
	} `xml:"CommandResponse>DomainGetListResult>Domain"`
}

func (n *Namecheap) call(ctx context.Context, cmd string, extra url.Values) (ncResponse, error) {
	v := url.Values{"ApiUser": {n.User}, "ApiKey": {n.Key}, "UserName": {n.User}, "ClientIp": {n.ClientIP}, "Command": {cmd}}
	for k, x := range extra {
		v[k] = x
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, firstNonEmpty(n.Base, "https://api.namecheap.com/xml.response"), strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c := n.HTTP
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	var out ncResponse
	resp, err := c.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return out, fmt.Errorf("Namecheap: %v", err)
	}
	if out.Status != "OK" {
		msg := "error"
		if len(out.Errors) > 0 {
			msg = strings.TrimSpace(out.Errors[0].Msg)
			// the same answer for a wrong user, a wrong key and API access off;
			// a wrong ClientIp is 1011150, apart
			if out.Errors[0].Number == "1011102" {
				msg = "the API user and key do not match, or API access is off (Profile → Tools → API Access); the user is the account name, not the e-mail"
			}
		}
		return out, fmt.Errorf("Namecheap: %s", msg)
	}
	return out, nil
}

func splitZone(zone string) (sld, tld string) {
	i := strings.IndexByte(zone, '.')
	if i < 0 {
		return zone, ""
	}
	return zone[:i], zone[i+1:]
}

func (n *Namecheap) Check(ctx context.Context) error {
	_, err := n.call(ctx, "namecheap.domains.getList", nil)
	return err
}

func (n *Namecheap) Zones(ctx context.Context) ([]string, error) {
	r, err := n.call(ctx, "namecheap.domains.getList", url.Values{"PageSize": {"100"}})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range r.Domains {
		out = append(out, strings.ToLower(d.Name))
	}
	return out, nil
}

func (n *Namecheap) Records(ctx context.Context, zone string) ([]Record, error) {
	sld, tld := splitZone(zone)
	r, err := n.call(ctx, "namecheap.domains.dns.getHosts", url.Values{"SLD": {sld}, "TLD": {tld}})
	if err != nil {
		return nil, err
	}
	var out []Record
	for i, h := range r.Hosts {
		ttl := h.TTL
		if ttl == 1799 {
			ttl = 0 // Namecheap's automatic
		}
		out = append(out, Record{ID: strconv.Itoa(i), Type: h.Type, Name: firstNonEmpty(h.Name, "@"), Value: h.Address, Priority: h.MXPref, TTL: ttl})
	}
	return out, nil
}

func (n *Namecheap) set(ctx context.Context, zone string, recs []Record) error {
	sld, tld := splitZone(zone)
	v := url.Values{"SLD": {sld}, "TLD": {tld}}
	for i, r := range recs {
		k := strconv.Itoa(i + 1)
		v.Set("HostName"+k, firstNonEmpty(r.Name, "@"))
		v.Set("RecordType"+k, r.Type)
		v.Set("Address"+k, r.Value)
		v.Set("TTL"+k, strconv.Itoa(max(r.TTL, 60)))
		if r.TTL == 0 {
			v.Set("TTL"+k, "1799")
		}
		if r.Type == "MX" {
			v.Set("MXPref"+k, strconv.Itoa(r.Priority))
			v.Set("EmailType", "MX")
		}
	}
	_, err := n.call(ctx, "namecheap.domains.dns.setHosts", v)
	return err
}

func (n *Namecheap) Create(ctx context.Context, zone string, r Record) (string, error) {
	recs, err := n.Records(ctx, zone)
	if err != nil {
		return "", err
	}
	recs = append(recs, r)
	return strconv.Itoa(len(recs) - 1), n.set(ctx, zone, recs)
}

func (n *Namecheap) Update(ctx context.Context, zone string, r Record) error {
	recs, err := n.Records(ctx, zone)
	if err != nil {
		return err
	}
	i, err := strconv.Atoi(r.ID)
	if err != nil || i < 0 || i >= len(recs) {
		return errors.New("the record changed at Namecheap: reload")
	}
	recs[i] = r
	return n.set(ctx, zone, recs)
}

func (n *Namecheap) Delete(ctx context.Context, zone, id string) error {
	recs, err := n.Records(ctx, zone)
	if err != nil {
		return err
	}
	i, err := strconv.Atoi(id)
	if err != nil || i < 0 || i >= len(recs) {
		return errors.New("the record changed at Namecheap: reload")
	}
	return n.set(ctx, zone, append(recs[:i], recs[i+1:]...))
}
