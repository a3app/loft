package main

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// torEnv is where tor and its pluggable transports live, and the bridges
// Tor Browser ships with.
type torEnv struct {
	Tor        string              // tor binary
	BrowserDir string              // Tor Browser folder, or "" when tor came from -tor
	Plugins    []plugin            // transport binaries and the transports each serves
	Builtin    map[string][]string // built-in bridge lines by group name (obfs4, snowflake, meek-azure, ...)
	Recommend  string              // the group Tor Browser suggests first
}

// plugin is one ClientTransportPlugin: the transports it serves, the binary,
// and its arguments. The path is kept apart from the rest because tor splits
// the line on spaces, and Tor Browser on macOS lives in "Tor Browser.app".
type plugin struct {
	Names []string
	Path  string
	Args  []string
}

// Line is the torrc line for p.
func (p plugin) Line() string {
	l := "ClientTransportPlugin " + strings.Join(p.Names, ",") + " exec " + p.Path
	if len(p.Args) > 0 {
		l += " " + strings.Join(p.Args, " ")
	}
	return l
}

// ptConfig is the part of Tor Browser's pt_config.json we use.
type ptConfig struct {
	RecommendedDefault  string              `json:"recommendedDefault"`
	PluggableTransports map[string]string   `json:"pluggableTransports"`
	Bridges             map[string][]string `json:"bridges"`
}

// exe adds .exe on Windows.
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// browserGuesses are the usual install places of Tor Browser.
func browserGuesses() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Applications/Tor Browser.app", filepath.Join(home, "Applications", "Tor Browser.app")}
	case "windows":
		return []string{filepath.Join(home, "Desktop", "Tor Browser"), filepath.Join(home, "OneDrive", "Desktop", "Tor Browser")}
	default:
		return []string{filepath.Join(home, "tor-browser"), filepath.Join(home, "tor-browser_en-US"), filepath.Join(home, ".local", "share", "torbrowser", "tbb")}
	}
}

// findTor locates tor, the transports, and the built-in bridges. A Tor
// Browser folder gives all three; -tor alone gives a tor that can only run
// the direct, file and loft cases (file needs -lyrebird or -snowflake).
func findTor(browserDir, torBin, lyrebird, snowflake string) (*torEnv, error) {
	env := &torEnv{Builtin: map[string][]string{}}
	if browserDir == "" && torBin == "" {
		for _, g := range browserGuesses() {
			if st, err := os.Stat(g); err == nil && st.IsDir() {
				browserDir = g
				break
			}
		}
	}
	if browserDir != "" {
		found, err := scanBrowser(browserDir)
		if err != nil {
			return nil, err
		}
		env.BrowserDir = browserDir
		env.Tor = found["tor"]
		cfg, err := browserPTConfig(found)
		if err != nil {
			return nil, err
		}
		if cfg != nil {
			ptDir := filepath.Dir(found[exe("lyrebird")])
			if found[exe("lyrebird")] == "" {
				ptDir = filepath.Dir(found[exe("tor")])
			}
			env.Plugins = pluginsOf(cfg, ptDir)
			env.Builtin = cfg.Bridges
			env.Recommend = cfg.RecommendedDefault
		}
	}
	if torBin != "" {
		env.Tor = torBin
	}
	if env.Tor == "" {
		if p, err := exec.LookPath("tor"); err == nil {
			env.Tor = p
		}
	}
	if env.Tor == "" {
		return nil, errors.New("no tor found: pass -tor-browser (Tor Browser folder) or -tor (tor binary)")
	}
	if lyrebird != "" {
		env.Plugins = append(env.Plugins, plugin{Names: []string{"meek_lite", "obfs4", "webtunnel"}, Path: lyrebird})
	}
	if snowflake != "" {
		env.Plugins = append(env.Plugins, plugin{Names: []string{"snowflake"}, Path: snowflake})
	}
	return env, nil
}

// scanBrowser walks a Tor Browser folder for tor, the transport binaries,
// pt_config.json, and the omni.ja archives (newer Tor Browsers keep
// pt_config.json inside omni.ja). Layouts differ between macOS, Linux and
// Windows, so it looks for the file names instead of fixed paths. Each
// omni.ja is stored under its own key, "omni.ja#1", "omni.ja#2", ...
func scanBrowser(dir string) (map[string]string, error) {
	want := map[string]bool{exe("tor"): true, "pt_config.json": true, exe("lyrebird"): true, exe("snowflake-client"): true, exe("conjure-client"): true}
	found := map[string]string{}
	omni := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		n := d.Name()
		if want[n] && found[n] == "" {
			found[n] = p
		}
		if n == "omni.ja" {
			omni++
			found[fmt.Sprintf("omni.ja#%d", omni)] = p
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found[exe("tor")] == "" {
		return nil, fmt.Errorf("%s: no %s inside", dir, exe("tor"))
	}
	found["tor"] = found[exe("tor")]
	return found, nil
}

// ptConfigInOmni is where pt_config.json sits inside omni.ja.
const ptConfigInOmni = "chrome/toolkit/content/global/pt_config.json"

// browserPTConfig reads pt_config.json from the folder, or else from an
// omni.ja archive. No config at all is not an error: tor then runs only the
// cases that need no built-in bridges.
func browserPTConfig(found map[string]string) (*ptConfig, error) {
	if p := found["pt_config.json"]; p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		return parsePTConfig(p, b)
	}
	for i := 1; found[fmt.Sprintf("omni.ja#%d", i)] != ""; i++ {
		p := found[fmt.Sprintf("omni.ja#%d", i)]
		b, err := readZipFile(p, ptConfigInOmni)
		if err != nil || b == nil {
			continue
		}
		return parsePTConfig(p+"!"+ptConfigInOmni, b)
	}
	return nil, nil
}

// readZipFile returns one file from a zip archive, or nil when it is absent.
func readZipFile(archive, name string) ([]byte, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 1<<20))
	}
	return nil, nil
}

func parsePTConfig(path string, b []byte) (*ptConfig, error) {
	var cfg ptConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

// pluginsOf reads Tor Browser's ClientTransportPlugin templates and fills
// ${pt_path} and ${pt_suffix}. The template is split before the path is
// filled in, so a folder with spaces stays one path.
func pluginsOf(cfg *ptConfig, ptDir string) []plugin {
	names := make([]string, 0, len(cfg.PluggableTransports))
	for n := range cfg.PluggableTransports {
		names = append(names, n)
	}
	sort.Strings(names)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	var out []plugin
	for _, n := range names {
		f := strings.Fields(cfg.PluggableTransports[n])
		if len(f) < 4 || f[0] != "ClientTransportPlugin" || f[2] != "exec" {
			continue
		}
		path := strings.ReplaceAll(f[3], "${pt_path}", ptDir+string(filepath.Separator))
		path = strings.ReplaceAll(path, "${pt_suffix}", suffix)
		out = append(out, plugin{Names: strings.Split(f[1], ","), Path: path, Args: f[4:]})
	}
	return out
}

// readBridgeFile groups bridge lines by transport. Empty lines and # comments
// are skipped; a leading "Bridge " is allowed.
func readBridgeFile(path string) (map[string][]string, error) {
	out := map[string][]string{}
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		l = strings.TrimSpace(strings.TrimPrefix(l, "Bridge "))
		t := transportOf(l)
		out[t] = append(out[t], l)
	}
	return out, sc.Err()
}

// transportOf is the first word of a bridge line, or "vanilla" for a plain
// address.
func transportOf(line string) string {
	f := strings.Fields(line)
	if len(f) == 0 || strings.ContainsAny(f[0], ".:[") {
		return "vanilla"
	}
	return f[0]
}

// pluginFor is the plugin that serves transport t.
func pluginFor(plugins []plugin, t string) (plugin, bool) {
	for _, p := range plugins {
		for _, name := range p.Names {
			if name == t {
				return p, true
			}
		}
	}
	return plugin{}, false
}

// torCase is one way of starting tor.
type torCase struct {
	Name    string
	Bridges []string // bridge lines; empty = no bridges
	Plugins []plugin // transport plugins the bridges need
	Proxy   string   // Socks5Proxy, for the loft case
	Skip    string   // why the case cannot run here
}

// Describe is one line about the case for -list and the report.
func (c torCase) Describe() string {
	switch {
	case c.Skip != "":
		return "skipped: " + c.Skip
	case c.Proxy != "":
		return "tor through loft SOCKS5 " + c.Proxy
	case len(c.Bridges) > 0:
		return fmt.Sprintf("%d bridge line(s)", len(c.Bridges))
	default:
		return "no bridges"
	}
}

// buildCases lists every case this machine can run, in a stable order:
// direct, Tor Browser's recommended bridges first, the other built-in ones,
// bridges from the file, then loft.
func buildCases(env *torEnv, extra map[string][]string, loftSOCKS string) []torCase {
	cases := []torCase{{Name: "direct"}}
	groups := make([]string, 0, len(env.Builtin))
	for g := range env.Builtin {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if (groups[i] == env.Recommend) != (groups[j] == env.Recommend) {
			return groups[i] == env.Recommend
		}
		return groups[i] < groups[j]
	})
	for _, g := range groups {
		cases = append(cases, bridgeCase("builtin:"+g, env.Builtin[g], env.Plugins))
	}
	ts := make([]string, 0, len(extra))
	for t := range extra {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	for _, t := range ts {
		cases = append(cases, bridgeCase("file:"+t, extra[t], env.Plugins))
	}
	if loftSOCKS != "" {
		c := torCase{Name: "loft", Proxy: loftSOCKS}
		if !socksUp(loftSOCKS) {
			c.Skip = "nothing listens on " + loftSOCKS + " (start the loft client)"
		}
		cases = append(cases, c)
	}
	return cases
}

// bridgeCase builds a case from bridge lines and finds the transport plugins
// they need.
func bridgeCase(name string, lines []string, plugins []plugin) torCase {
	c := torCase{Name: name, Bridges: lines}
	seen := map[string]bool{}
	for _, l := range lines {
		t := transportOf(l)
		if t == "vanilla" || seen[t] {
			continue
		}
		seen[t] = true
		p, ok := pluginFor(plugins, t)
		if !ok {
			c.Skip = "no transport plugin for " + t + " (use Tor Browser or pass -lyrebird / -snowflake)"
			return c
		}
		c.Plugins = append(c.Plugins, p)
	}
	return c
}

// pickCases keeps the cases named in a comma list, all when it is empty.
func pickCases(all []torCase, list string) ([]torCase, error) {
	list = strings.TrimSpace(list)
	if list == "" {
		return all, nil
	}
	byName := map[string]torCase{}
	for _, c := range all {
		byName[c.Name] = c
	}
	var out []torCase
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		c, ok := byName[n]
		if !ok {
			names := make([]string, 0, len(all))
			for _, c := range all {
				names = append(names, c.Name)
			}
			return nil, fmt.Errorf("no case %q here; have: %s", n, strings.Join(names, ", "))
		}
		out = append(out, c)
	}
	return out, nil
}

// torVersion is the first line of tor --version.
func torVersion(tor string) string {
	b, err := exec.Command(tor, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
}

func osLine() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
