package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
)

// TestMemberKeyFromNode: the link, QR and .loft are shown again from the key
// the node holds; the panel stores no copy and has no setting for one.
func TestMemberKeyFromNode(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	if r := e.do("GET", "/api/settings", nil, c, ""); r.code != 200 || r.body["keep_member_keys"] != nil {
		t.Fatalf("settings still has keep_member_keys: %v", r.body)
	}
	issued := e.do("POST", "/api/members/key", map[string]string{"id": "alice"}, c, csrf)
	if issued.code != 200 || !strings.HasPrefix(issued.body["key"].(string), "lt_") {
		t.Fatalf("issue %d %v", issued.code, issued.body)
	}
	if file, _ := issued.body["file"].(string); !strings.Contains(file, `name: "alice-get"`) {
		t.Fatalf("default outbound name in .loft: %q", file)
	}
	key := issued.body["key"].(string)
	if strings.Contains(e.do("GET", "/api/settings", nil, c, "").raw(), key) {
		t.Fatal("plaintext key in GET /api/settings")
	}
	view := e.do("GET", "/api/members/key?id=alice", nil, c, "")
	if view.code != 200 || view.body["key"] != key || view.body["link"] == nil {
		t.Fatalf("view %d %v", view.code, view.body)
	}
	if r := e.do("GET", "/api/members/key?id=nobody", nil, c, ""); r.code != 404 || r.body["error"] != "no stored key for this member" {
		t.Fatalf("missing %d %v", r.code, r.body)
	}
	if r := e.do("GET", "/api/members/key", nil, c, ""); r.code != 400 {
		t.Fatalf("no id %d", r.code)
	}
	e.srv.Adm = func(string) Adm { return noKeysAdm{} }
	if r := e.do("GET", "/api/members/key?id=alice", nil, c, ""); r.code != 404 {
		t.Fatalf("empty ring must not fall back to a panel copy: %d %v", r.code, r.body)
	}
}

func TestLoftOutboundInFile(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	e.do("POST", "/api/settings/panel", map[string]any{"session_idle": "30m", "session_max": "7d", "lang": "ru",
		"loft_outbound": map[string]any{"host": true, "front": true}}, c, csrf)
	issued := e.do("POST", "/api/members/key", map[string]string{"id": "bob"}, c, csrf)
	file, _ := issued.body["file"].(string)
	if !strings.Contains(file, `name: "panel.example.net-get"`) {
		t.Fatalf("host+front .loft: %q", file)
	}
	e.do("POST", "/api/settings/panel", map[string]any{"session_idle": "30m", "session_max": "7d", "lang": "ru",
		"loft_outbound": map[string]any{"host": true, "member": true, "front": true}}, c, csrf)
	issued = e.do("POST", "/api/members/key", map[string]string{"id": "bob"}, c, csrf)
	file, _ = issued.body["file"].(string)
	if !strings.Contains(file, `name: "panel.example.net-bob-get"`) {
		t.Fatalf("host+member+front .loft: %q", file)
	}
}

func TestMemberLinkQueryFronts(t *testing.T) {
	s := &Server{}
	link, file := s.memberLink(admproto.NodeInfo{
		Host: "example.com", GetPath: "/static/v3/build", WSPath: "/api/v1/sync", H2Path: "/api/v1/tunnel",
		Fronts: []string{"get", "ws", "h2", "quic"},
	}, "lt_abc", "bob", "bob")
	if !strings.Contains(link, "get=/static/v3/build") || !strings.Contains(link, "ws=/api/v1/sync") || !strings.Contains(link, "h2=/api/v1/tunnel") || !strings.Contains(link, "quic") {
		t.Fatalf("link %s", link)
	}
	if strings.Contains(link, "type=get") {
		t.Fatalf("old type= in %s", link)
	}
	if !strings.HasPrefix(link, "loft://lt_abc@example.com/?") || !strings.HasSuffix(link, "#bob") {
		t.Fatalf("shape %s", link)
	}
	if !strings.Contains(file, "type: get") || strings.Contains(file, "type: ws") {
		t.Fatalf("file %s", file)
	}
	if !strings.Contains(file, "fronts: [get, ws, h2, quic]") {
		t.Fatalf("hello fronts %s", file)
	}
	getOnly, getFile := s.memberLink(admproto.NodeInfo{Host: "example.com", GetPath: "/static/js"}, "lt_abc", "bob", "bob")
	if getOnly != "loft://lt_abc@example.com/?get=/static/js&fp=chrome#bob" {
		t.Fatalf("get-only %s", getOnly)
	}
	if strings.Contains(getFile, "fronts:") || strings.Contains(getFile, "mask_pub:") {
		t.Fatalf("get-only file %s", getFile)
	}
	masked, maskedFile := s.memberLink(admproto.NodeInfo{
		Host: "example.com", GetPath: "/static/js", Mask: true, MaskPub: "cccb563311f0de9f0c3dd28b1450d978bdabfc63ca6d73cf561f2ff3ecc09443",
	}, "lt_abc", "bob", "bob")
	if !strings.Contains(masked, "pbk=cccb563311f0de9f0c3dd28b1450d978bdabfc63ca6d73cf561f2ff3ecc09443") {
		t.Fatalf("pbk %s", masked)
	}
	if !strings.Contains(maskedFile, "mask_pub: cccb563311f0de9f0c3dd28b1450d978bdabfc63ca6d73cf561f2ff3ecc09443") {
		t.Fatalf("yaml pbk %s", maskedFile)
	}
}

func TestWizardMemberKeepsKey(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	j := e.job(e.do("POST", "/api/wizard/member", map[string]string{"id": "carol", "name": "Carol"}, c, csrf), c)
	res, _ := j["result"].(map[string]any)
	if j["state"] != "done" || res["key"] == nil {
		t.Fatalf("wizard %v", j)
	}
	key := res["key"].(string)
	view := e.do("GET", "/api/members/key?id=carol", nil, c, "")
	if view.code != 200 || view.body["key"] != key {
		t.Fatalf("wizard stored %d %v", view.code, view.body)
	}
}

type noKeysAdm struct{}

func (noKeysAdm) Call(_ context.Context, op string, _, out any) error {
	if op == admproto.OpMemberGet {
		return errors.New("no stored key for this member")
	}
	if op == admproto.OpNodeInfo {
		b, _ := json.Marshal(admproto.NodeInfo{Host: "panel.example.net", GetPath: "/static/js"})
		return json.Unmarshal(b, out)
	}
	return nil
}
