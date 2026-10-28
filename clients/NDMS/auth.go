package ndms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Check asks the Keenetic web admin whether cookie is a signed-in
// administrator. authURL must be loopback (/auth on :80). A 200 with no
// role is accepted: the account name is not the role. Guest, viewer and
// readonly are refused. KeeneticOS 5.2's one-shot token, when the browser
// was bounced back with ?ndw4=, is sent as x-ndw4-authenticate.
func Check(ctx context.Context, authURL, cookie, ndw4 string) (bool, error) {
	if err := Loopback(authURL); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return false, err
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if ndw4 != "" {
		req.Header.Set("x-ndw4-authenticate", ndw4)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return false, nil
	}
	return !guestRole(body), nil
}

// Loopback refuses any auth or RCI URL that is not the router itself.
func Loopback(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme")
	}
	h := u.Hostname()
	if h != "127.0.0.1" && h != "localhost" && h != "::1" {
		return fmt.Errorf("url host %q", h)
	}
	return nil
}

// guestWords are roles that may open the stock admin and must not open loft.
var guestWords = map[string]bool{
	"guest": true, "viewer": true, "readonly": true, "read-only": true,
	"user": true, "none": true, "monitor": true,
}

// staffWords are roles that may change the router.
var staffWords = map[string]bool{
	"admin": true, "administrator": true, "configurator": true,
	"root": true, "full": true,
}

// guestRole is true when the /auth JSON names a guest role, or the only
// identity is the built-in guest account. A body with no role is not a guest.
func guestRole(body []byte) bool {
	if len(strings.TrimSpace(string(body))) == 0 {
		return false
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return false
	}
	var role, user string
	var sawRole bool
	walkAuth(v, func(k, val string) {
		lk, lv := strings.ToLower(k), strings.ToLower(strings.TrimSpace(val))
		switch lk {
		case "level", "role", "policy", "tag", "group", "access", "rights":
			sawRole = true
			role = lv
		case "user", "username", "identity", "login":
			if user == "" {
				user = lv
			}
		}
	})
	if sawRole {
		if guestWords[role] {
			return true
		}
		if staffWords[role] {
			return false
		}
	}
	return user == "guest"
}

// walkAuth visits every string leaf of an /auth JSON value. Firmware
// builds put role and user at different depths, so the walk is by key
// name rather than by a fixed path.
func walkAuth(v any, fn func(k, val string)) {
	switch t := v.(type) {
	case map[string]any:
		for k, c := range t {
			if s, ok := c.(string); ok {
				fn(k, s)
				continue
			}
			walkAuth(c, fn)
		}
	case []any:
		for _, c := range t {
			walkAuth(c, fn)
		}
	}
}

// LoginURL is the stock web admin with a return to this page.
func LoginURL(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = "192.168.1.1:8090"
	}
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	ret := "http://" + host + "/"
	return "http://" + h + "/?return=" + url.QueryEscape(ret)
}

// NDW4 reports whether an /auth response asked for the one-shot token.
func NDW4(h http.Header) bool {
	return h.Get("x-ndw4-authenticate") != "" || h.Get("X-NDW4-Authenticate") != ""
}
