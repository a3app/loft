package mobile

import (
	"strings"
	"testing"
)

func TestParseShareRejectsUnknownKind(t *testing.T) {
	raw := "kind: other\ntoken: lt_x\noutbounds:\n  - name: a\n    type: get\n    url: https://edge.example.com\n"
	if _, err := ParseShare(raw); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestParseShareLoft(t *testing.T) {
	y, err := ParseShare("loft://lt_abc@edge.example.com:443/static/js?type=get&cdn=cloudflare&fp=chrome#Work")
	if err != nil {
		t.Fatal(err)
	}
	cfg, extra, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "lt_abc" {
		t.Fatalf("token %q", cfg.Token)
	}
	if len(cfg.Outbounds) != 1 {
		t.Fatalf("outbounds %d", len(cfg.Outbounds))
	}
	o := cfg.Outbounds[0]
	if o.Type != "get" || o.CDN != "cloudflare" || o.Fingerprint != "chrome" || o.Name != "Work" {
		t.Fatalf("%+v extra=%+v", o, extra)
	}
	if o.URL != "https://edge.example.com" {
		t.Fatalf("url %q", o.URL)
	}
	if o.Path != "/static/js" {
		t.Fatalf("path %q", o.Path)
	}
	if primaryType(cfg) != "get" {
		t.Fatalf("kind %q", primaryType(cfg))
	}
	if !strings.Contains(y, "kind: loft") {
		t.Fatalf("want kind: loft in\n%s", y)
	}
}

func TestParseShareLoftPbk(t *testing.T) {
	y, err := ParseShare("loft://lt_abc@edge.example.com/static/js?pbk=q3ucJuIkRdJYS8_N0Si4U7FA3Qo7jqcblDghyDUXIc8#Home")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Outbounds[0].MaskPub == "" {
		t.Fatalf("mask_pub missing in\n%s", y)
	}
}

func TestParseShareLoftFrontQuery(t *testing.T) {
	y, err := ParseShare("loft://lt_abc@huy.com:443/?get=/static/v3/build&ws=/api/v1/sync&h2=/api/v1/tunnel&quic&fp=chrome#bob")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outbounds) != 4 {
		t.Fatalf("outbounds %d\n%s", len(cfg.Outbounds), y)
	}
	if cfg.Outbounds[0].Type != "get" || cfg.Outbounds[0].Path != "/static/v3/build" || cfg.Outbounds[0].URL != "https://huy.com" {
		t.Fatalf("get %+v", cfg.Outbounds[0])
	}
	if cfg.Outbounds[1].Type != "ws" || cfg.Outbounds[1].URL != "wss://huy.com/api/v1/sync" {
		t.Fatalf("ws %+v", cfg.Outbounds[1])
	}
	if cfg.Outbounds[2].Type != "h2" || cfg.Outbounds[2].Path != "/api/v1/tunnel" {
		t.Fatalf("h2 %+v", cfg.Outbounds[2])
	}
	if cfg.Outbounds[3].Type != "quic" {
		t.Fatalf("quic %+v", cfg.Outbounds[3])
	}
	empty, err := ParseShare("loft://lt_abc@huy.com/?get=&ws=&h2=#Home")
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := decodeFile([]byte(empty))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Outbounds) != 3 || got.Outbounds[0].Path != "/static/v3/build" {
		t.Fatalf("empty get= %+v", got.Outbounds)
	}
	if got.Outbounds[1].Type != "ws" || got.Outbounds[2].Type != "h2" {
		t.Fatalf("empty fronts %+v", got.Outbounds)
	}
}

func TestParseShareEdgeGETThenNode(t *testing.T) {
	raw := "loft://lt_abc@loft.example.com:444/?get=/static/v3/build&ws=/api/v1/sync&h2=/api/v1/tunnel&quic&edge=https://cdn.example.com/static/v3/build#Home"
	y, err := ParseShare(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outbounds) != 5 {
		t.Fatalf("outbounds %d\n%s", len(cfg.Outbounds), y)
	}
	if cfg.Outbounds[0].Type != "get" || cfg.Outbounds[0].URL != "https://cdn.example.com" || cfg.Outbounds[0].Path != "/static/v3/build" {
		t.Fatalf("edge %+v", cfg.Outbounds[0])
	}
	if cfg.Outbounds[1].Type != "get" || cfg.Outbounds[1].URL != "https://loft.example.com:444" || cfg.Outbounds[1].Path != "/static/v3/build" {
		t.Fatalf("node %+v", cfg.Outbounds[1])
	}
	if cfg.Outbounds[2].Type != "ws" || cfg.Outbounds[2].URL != "wss://loft.example.com:444/api/v1/sync" {
		t.Fatalf("ws %+v", cfg.Outbounds[2])
	}
	if cfg.Outbounds[3].URL != "https://loft.example.com:444" || cfg.Outbounds[4].URL != "https://loft.example.com:444" {
		t.Fatalf("h2/quic %+v %+v", cfg.Outbounds[3], cfg.Outbounds[4])
	}
	if cfg.EdgeURL != "https://cdn.example.com/static/v3/build" {
		t.Fatalf("edge field %q", cfg.EdgeURL)
	}
}

func TestParseShareLoftRejectsSSTPType(t *testing.T) {
	_, err := ParseShare("loft://lt_abc@edge.example.com?type=sstp")
	if err == nil {
		t.Fatal("want error")
	}
}

func TestParseShareLoftL3(t *testing.T) {
	y, err := ParseShare("loft://lt_abc@edge.example.com:443?type=l3#Lab")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outbounds) != 1 || cfg.Outbounds[0].Type != "l3" {
		t.Fatalf("%+v", cfg.Outbounds)
	}
	if primaryType(cfg) != "l3" {
		t.Fatalf("kind %q", primaryType(cfg))
	}
}

func TestParseShareVLESS(t *testing.T) {
	raw := "vless://550e8400-e29b-41d4-a716-446655440000@de1.example.com:443?security=tls&sni=de1.example.com#n"
	y, err := ParseShare(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("token %q", cfg.Token)
	}
	if primaryType(cfg) != "vless" {
		t.Fatalf("kind %q", primaryType(cfg))
	}
	if !strings.Contains(cfg.Outbounds[0].URL, "vless://") {
		t.Fatalf("url %q", cfg.Outbounds[0].URL)
	}
}

func TestParseShareTrojan(t *testing.T) {
	raw := "trojan://secret@de1.example.com:443?security=reality&sni=www.microsoft.com&fp=chrome&pbk=q3ucJuIkRdJYS8_N0Si4U7FA3Qo7jqcblDghyDUXIc8&sid=abcd&type=tcp#n"
	y, err := ParseShare(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "secret" {
		t.Fatalf("token %q", cfg.Token)
	}
	if primaryType(cfg) != "trojan" {
		t.Fatalf("kind %q", primaryType(cfg))
	}
	if !strings.Contains(cfg.Outbounds[0].URL, "trojan://") {
		t.Fatalf("url %q", cfg.Outbounds[0].URL)
	}
	if !strings.Contains(y, "kind: trojan") {
		t.Fatalf("want kind: trojan in\n%s", y)
	}
}

func TestParseShareTrojanYAMLNoToken(t *testing.T) {
	raw := "kind: trojan\noutbounds:\n  - name: t\n    type: trojan\n    url: trojan://pw@de1.example.com:443?security=tls\n"
	y, err := ParseShare(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "pw" || primaryType(cfg) != "trojan" {
		t.Fatalf("token=%q kind=%q", cfg.Token, primaryType(cfg))
	}
}

func TestParseShareSSTP(t *testing.T) {
	y, err := ParseShare("sstp://alice:secret@vpn.example.com:8443/path?sni=vpn.example.com&pap=1#Office")
	if err != nil {
		t.Fatal(err)
	}
	cfg, extra, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if extra.username != "alice" || extra.password != "secret" || !extra.pap {
		t.Fatalf("extra %+v", extra)
	}
	if primaryType(cfg) != "sstp" {
		t.Fatalf("kind %q", primaryType(cfg))
	}
	if cfg.Outbounds[0].URL != "https://vpn.example.com:8443/path" {
		t.Fatalf("url %q", cfg.Outbounds[0].URL)
	}
	if cfg.Outbounds[0].SNI != "vpn.example.com" {
		t.Fatalf("sni %q", cfg.Outbounds[0].SNI)
	}
}

func TestParseShareBundle(t *testing.T) {
	raw := `connections:
  - name: Home
    kind: loft
    token: lt_home
    outbounds:
      - name: cdn
        type: get
        url: https://cdn.example.com
        path: /static/v3/build
  - name: Office
    kind: sstp
    username: alice
    password: secret
    outbounds:
      - name: Office
        type: sstp
        url: https://vpn.example.com
`
	y, err := ParseShare(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "lt_home" || primaryType(cfg) != "get" {
		t.Fatalf("token=%q kind=%q", cfg.Token, primaryType(cfg))
	}
}

func TestParseShareAllBundle(t *testing.T) {
	raw := `connections:
  - name: Home
    kind: loft
    token: lt_home
    outbounds:
      - name: cdn
        type: get
        url: https://cdn.example.com
        path: /static/v3/build
  - name: Office
    kind: sstp
    username: alice
    password: secret
    outbounds:
      - name: Office
        type: sstp
        url: https://vpn.example.com
  - name: Lab
    kind: loft
    token: lt_lab
    outbounds:
      - name: l3
        type: l3
        url: https://edge.example.com
`
	all, err := ParseShareAll(raw)
	if err != nil {
		t.Fatal(err)
	}
	docs := strings.Split(all, "\n---\n")
	if len(docs) != 3 {
		t.Fatalf("docs %d\n%s", len(docs), all)
	}
	cfg, _, err := decodeFile([]byte(docs[2]))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "lt_lab" || primaryType(cfg) != "l3" {
		t.Fatalf("token=%q kind=%q", cfg.Token, primaryType(cfg))
	}
}

func TestParseShareYAML(t *testing.T) {
	raw := "token: lt_x\nselect: auto\noutbounds:\n  - name: a\n    type: ws\n    url: wss://edge.example.com/ws\n"
	y, err := ParseShare(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "lt_x" || primaryType(cfg) != "ws" {
		t.Fatalf("token=%q kind=%q", cfg.Token, primaryType(cfg))
	}
}

func TestParseShareVPNJSON(t *testing.T) {
	y, err := ParseShare(`{"host":"vpn.example.com","username":"bob","password":"p","name":"Home"}`)
	if err != nil {
		t.Fatal(err)
	}
	cfg, extra, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if extra.username != "bob" || extra.password != "p" {
		t.Fatalf("extra %+v", extra)
	}
	if primaryType(cfg) != "sstp" {
		t.Fatalf("kind %q", primaryType(cfg))
	}
}

func TestParseShareBase64(t *testing.T) {
	// loft://lt_abc@edge.example.com
	y, err := ParseShare("bG9mdDovL2x0X2FiY0BlZGdlLmV4YW1wbGUuY29t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(y, "lt_abc") {
		t.Fatalf("%s", y)
	}
}

func TestParseShareGarbage(t *testing.T) {
	if _, err := ParseShare(""); err == nil {
		t.Fatal("empty")
	}
	if _, err := ParseShare("hello world"); err == nil {
		t.Fatal("garbage")
	}
	if _, err := ParseShare("https://example.com"); err == nil {
		t.Fatal("https")
	}
}
