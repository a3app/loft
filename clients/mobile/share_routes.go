package mobile

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseRoutes turns a loft://routes/… link, a YAML kind: routes document, or
// a SplitJSON object into the operator/link SplitStore JSON. It is not a
// connection: ParseShare refuses loft://routes.
func ParseRoutes(raw string) (string, error) {
	s := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if s == "" {
		return "", fmt.Errorf("empty")
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "loft://routes") {
		return routesFromURL(s)
	}
	if strings.HasPrefix(s, "{") {
		if looksLikeSplit([]byte(s)) {
			return s, nil
		}
		return "", fmt.Errorf("routes")
	}
	var y struct {
		Kind    string `yaml:"kind"`
		Profile string `yaml:"profile"`
	}
	if err := yaml.Unmarshal([]byte(s), &y); err != nil || !strings.EqualFold(y.Kind, "routes") {
		return "", fmt.Errorf("routes")
	}
	if y.Profile != "" {
		return decodeRoutesPayload(y.Profile)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		return "", err
	}
	delete(m, "kind")
	delete(m, "profile")
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	if !looksLikeSplit(b) {
		return "", fmt.Errorf("routes")
	}
	return string(b), nil
}

// EncodeRoutes is loft://routes/<base64url(SplitJSON)>.
func EncodeRoutes(splitJSON string) string {
	return "loft://routes/" + base64.RawURLEncoding.EncodeToString([]byte(splitJSON))
}

func routesFromURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "loft") {
		return "", fmt.Errorf("loft://routes")
	}
	host := strings.ToLower(u.Hostname())
	if host != "routes" {
		return "", fmt.Errorf("loft://routes")
	}
	p := strings.TrimPrefix(u.Path, "/")
	if p == "" && u.Opaque != "" {
		p = strings.TrimPrefix(u.Opaque, "routes/")
	}
	if p == "" {
		return "", fmt.Errorf("loft://routes")
	}
	return decodeRoutesPayload(p)
}

func decodeRoutesPayload(p string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(p)
		if err != nil {
			return "", fmt.Errorf("loft://routes")
		}
	}
	if !looksLikeSplit(raw) {
		return "", fmt.Errorf("loft://routes")
	}
	return string(raw), nil
}

func looksLikeSplit(b []byte) bool {
	var d struct {
		Zones    json.RawMessage `json:"zones"`
		Services json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return false
	}
	return len(d.Zones) > 0 && len(d.Services) > 0
}
