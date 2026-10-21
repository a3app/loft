package config

// Switch-style node.conf commands (role, listen, decoy, canary, …).

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"github.com/a3app/loft/internal/startup"
)

// ParseCommands reads node config as switch-style commands.
func ParseCommands(body string) (*ServerConfig, error) {
	c := &ServerConfig{}
	for _, ln := range startup.Lines(body) {
		if len(ln.Tok) == 0 {
			continue
		}
		if ln.Indent > 0 {
			return nil, fmt.Errorf("line %d: unexpected indent", ln.N)
		}
		if err := applyNode(c, ln.Tok); err != nil {
			return nil, fmt.Errorf("line %d: %w", ln.N, err)
		}
	}
	return finishServer(c)
}

// applyNode sets one top-level command. hostname and keep_keys are accepted and ignored.
func applyNode(c *ServerConfig, tok []string) error {
	switch startup.Word(tok[0]) {
	case "hostname":
		// overlay id lives in network.conf; accepted and ignored.
	case "role":
		if len(tok) < 2 {
			return fmt.Errorf("role wants controller|relay")
		}
		c.Role = startup.Word(tok[1])
	case "listen":
		if len(tok) < 2 {
			return fmt.Errorf("listen wants an address")
		}
		c.Listen = tok[1]
	case "advertise":
		if len(tok) < 2 {
			return fmt.Errorf("advertise wants a URL")
		}
		c.Advertise = tok[1]
	case "enrol":
		if len(tok) < 2 {
			return fmt.Errorf("enrol wants list|open")
		}
		c.Enrol = startup.Word(tok[1])
	case "auth":
		// auth shared enable|disable — LOFT_TOKEN as client secret when the ring is empty.
		if len(tok) < 3 || startup.Word(tok[1]) != "shared" {
			return fmt.Errorf("auth wants shared enable|disable")
		}
		on, ok := startup.OnOff(tok[2])
		if !ok {
			return fmt.Errorf("auth shared: want enable|disable")
		}
		c.AuthShared = on
	case "keep_keys":
		// operator-side flag; the node does not use it.
	case "mask":
		if len(tok) < 2 {
			return fmt.Errorf("mask wants enable|disable")
		}
		on, ok := startup.OnOff(tok[1])
		if !ok {
			return fmt.Errorf("mask: want enable|disable")
		}
		c.Mask = on
	case "fronts":
		c.Fronts = append([]string(nil), tok[1:]...)
	case "decoy":
		if len(tok) < 2 {
			return fmt.Errorf("decoy wants host:port")
		}
		c.Decoy = append(c.Decoy, tok[1])
	case "decoy_proxy":
		if len(tok) < 2 {
			return fmt.Errorf("decoy_proxy wants enable|disable")
		}
		on, ok := startup.OnOff(tok[1])
		if !ok {
			return fmt.Errorf("decoy_proxy: want enable|disable")
		}
		c.DecoyProxy = on
	case "cert":
		if len(tok) < 2 {
			return fmt.Errorf("cert wants a path")
		}
		c.CertFile = tok[1]
	case "key":
		if len(tok) < 2 {
			return fmt.Errorf("key wants a path")
		}
		c.KeyFile = tok[1]
	case "cover_dir":
		if len(tok) >= 2 {
			c.CoverDir = tok[1]
		}
	case "get_path":
		if len(tok) >= 2 {
			c.GetPath = tok[1]
		}
	case "get_h2":
		if len(tok) < 2 {
			return fmt.Errorf("get_h2 wants 1-4")
		}
		n := 0
		fmt.Sscanf(tok[1], "%d", &n)
		if n < 1 || n > 4 {
			return fmt.Errorf("get_h2 wants 1-4")
		}
		c.GetH2 = n
	case "ws_path":
		if len(tok) >= 2 {
			c.WSPath = tok[1]
		}
	case "h2_path":
		if len(tok) >= 2 {
			c.H2Path = tok[1]
		}
	case "h3_path":
		if len(tok) >= 2 {
			c.H3Path = tok[1]
		}
	case "roster_path":
		if len(tok) >= 2 {
			c.RosterPath = tok[1]
		}
	case "lists_path":
		if len(tok) >= 2 {
			c.ListsPath = tok[1]
		}
	case "controller":
		if len(tok) >= 2 {
			c.Controller = tok[1]
		}
	case "id":
		if len(tok) >= 2 {
			c.ID = tok[1]
		}
	case "canary":
		return applyCanary(&c.Canary, tok)
	case "trace":
		if len(tok) < 2 {
			return fmt.Errorf("trace wants a path")
		}
		c.Trace = tok[1]
	case "relay":
		return applyRelay(c, tok)
	case "cdn_peer":
		if len(tok) < 2 {
			return fmt.Errorf("cdn_peer wants a CIDR")
		}
		if _, _, err := net.ParseCIDR(tok[1]); err != nil {
			return fmt.Errorf("cdn_peer %q: not a CIDR", tok[1])
		}
		c.CDNPeers = append(c.CDNPeers, tok[1])
	default:
		return fmt.Errorf("unknown command %q", tok[0])
	}
	return nil
}

// applyRelay is "relay ID url URL [decoy HOST:PORT] [fronts a,b] [weight N]
// [path P] [mask_pub HEX] [drain]": one row of the enrol: list allowlist.
func applyRelay(c *ServerConfig, tok []string) error {
	if len(tok) < 2 {
		return fmt.Errorf("relay wants ID [url URL] …")
	}
	e := RelayEntry{ID: tok[1]}
	for i := 2; i < len(tok); i++ {
		k := startup.Word(tok[i])
		if k == "drain" {
			e.Drain = true
			continue
		}
		if i+1 >= len(tok) {
			return fmt.Errorf("relay %s: %s wants a value", e.ID, k)
		}
		v := tok[i+1]
		i++
		switch k {
		case "url":
			e.URL = v
		case "decoy":
			e.Decoy = v
		case "fronts":
			e.Fronts = strings.Split(v, ",")
		case "weight":
			n := 0
			fmt.Sscanf(v, "%d", &n)
			if n < 0 || n > 255 {
				return fmt.Errorf("relay %s: weight 0-255", e.ID)
			}
			e.Weight = byte(n)
		case "path":
			e.Path = v
		case "mask_pub":
			e.MaskPub = v
		default:
			return fmt.Errorf("relay %s: unknown %q", e.ID, k)
		}
	}
	c.Relays = append(c.Relays, e)
	return nil
}

// applyCanary is "canary KEY VALUE" for fat_hello, split_hello, pad.
func applyCanary(d *CanaryConfig, tok []string) error {
	if len(tok) < 3 {
		return fmt.Errorf("canary wants KEY VALUE")
	}
	switch startup.Word(tok[1]) {
	case "fat_hello":
		on, ok := startup.OnOff(tok[2])
		if !ok {
			return fmt.Errorf("canary fat_hello: want enable|disable")
		}
		d.FatHello = &on
	case "split_hello":
		on, ok := startup.OnOff(tok[2])
		if !ok {
			return fmt.Errorf("canary split_hello: want enable|disable")
		}
		d.SplitHello = on
	case "pad":
		n := 0
		fmt.Sscanf(tok[2], "%d", &n)
		d.Pad = n
	default:
		return fmt.Errorf("canary: unknown %q", tok[1])
	}
	return nil
}

// RenderCommands writes node config as switch-style commands.
func RenderCommands(c ServerConfig, keepKeys bool) string {
	if c.Listen == "" {
		c.Listen = ":443"
	}
	if c.Role == "" {
		c.Role = "controller"
	}
	if c.Enrol == "" {
		c.Enrol = "list"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "role %s\n", c.Role)
	fmt.Fprintf(&b, "listen %s\n", c.Listen)
	if c.CertFile != "" {
		fmt.Fprintf(&b, "cert %s\n", c.CertFile)
	}
	if c.KeyFile != "" {
		fmt.Fprintf(&b, "key %s\n", c.KeyFile)
	}
	if c.CoverDir != "" {
		fmt.Fprintf(&b, "cover_dir %s\n", c.CoverDir)
	}
	if c.GetPath != "" {
		fmt.Fprintf(&b, "get_path %s\n", c.GetPath)
	}
	if c.GetH2 > 1 {
		fmt.Fprintf(&b, "get_h2 %d\n", c.GetH2)
	}
	if c.WSPath != "" {
		fmt.Fprintf(&b, "ws_path %s\n", c.WSPath)
	}
	if c.H2Path != "" {
		fmt.Fprintf(&b, "h2_path %s\n", c.H2Path)
	}
	if c.H3Path != "" {
		fmt.Fprintf(&b, "h3_path %s\n", c.H3Path)
	}
	if c.Advertise != "" {
		fmt.Fprintf(&b, "advertise %s\n", c.Advertise)
	}
	if c.Role == "relay" {
		if c.ID != "" {
			fmt.Fprintf(&b, "id %s\n", c.ID)
		}
		if c.Controller != "" {
			fmt.Fprintf(&b, "controller %s\n", c.Controller)
		}
	} else {
		if c.RosterPath != "" {
			fmt.Fprintf(&b, "roster_path %s\n", c.RosterPath)
		}
		if c.ListsPath != "" {
			fmt.Fprintf(&b, "lists_path %s\n", c.ListsPath)
		}
		fmt.Fprintf(&b, "enrol %s\n", c.Enrol)
	}
	fmt.Fprintf(&b, "auth shared %s\n", onOff(c.AuthShared))
	for _, d := range c.Decoy {
		fmt.Fprintf(&b, "decoy %s\n", d)
	}
	if c.DecoyProxy {
		b.WriteString("decoy_proxy enable\n")
	}
	fmt.Fprintf(&b, "mask %s\n", onOff(c.Mask))
	if len(c.Fronts) > 0 {
		fmt.Fprintf(&b, "fronts %s\n", strings.Join(c.Fronts, " "))
	}
	fmt.Fprintf(&b, "keep_keys %s\n", onOff(keepKeys))
	fmt.Fprintf(&b, "canary fat_hello %s\n", onOff(c.Canary.Fat()))
	fmt.Fprintf(&b, "canary split_hello %s\n", onOff(c.Canary.SplitHello))
	fmt.Fprintf(&b, "canary pad %d\n", c.Canary.Pad)
	if c.Trace != "" {
		fmt.Fprintf(&b, "trace %s\n", c.Trace)
	}
	for _, p := range c.CDNPeers {
		fmt.Fprintf(&b, "cdn_peer %s\n", p)
	}
	for _, e := range c.Relays {
		fmt.Fprintf(&b, "relay %s", e.ID)
		if e.URL != "" {
			fmt.Fprintf(&b, " url %s", e.URL)
		}
		if e.Decoy != "" {
			fmt.Fprintf(&b, " decoy %s", e.Decoy)
		}
		if len(e.Fronts) > 0 {
			fmt.Fprintf(&b, " fronts %s", strings.Join(e.Fronts, ","))
		}
		if e.Weight > 0 {
			fmt.Fprintf(&b, " weight %d", e.Weight)
		}
		if e.Path != "" {
			fmt.Fprintf(&b, " path %s", e.Path)
		}
		if e.MaskPub != "" {
			fmt.Fprintf(&b, " mask_pub %s", e.MaskPub)
		}
		if e.Drain {
			b.WriteString(" drain")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// LoadLists reads lists.conf: "NAME ENTRY" per line (domain, .suffix or
// CIDR), # comments. A missing file is no lists.
func LoadLists(path string) (map[string][]string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string][]string{}
	for i, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		f := strings.Fields(ln)
		if len(f) != 2 {
			return nil, fmt.Errorf("%s line %d: NAME ENTRY", path, i+1)
		}
		m[f[0]] = append(m[f[0]], f[1])
	}
	return m, nil
}

// RenderLists writes lists in the form LoadLists reads, names sorted.
func RenderLists(m map[string][]string) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Address lists the node signs and serves: NAME ENTRY (domain, .suffix, CIDR).\n")
	for _, n := range names {
		for _, e := range m[n] {
			fmt.Fprintf(&b, "%s %s\n", n, e)
		}
	}
	return b.String()
}

// onOff formats a bool as enable/disable.
func onOff(v bool) string {
	if v {
		return "enable"
	}
	return "disable"
}
