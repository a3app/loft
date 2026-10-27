package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// step is one "Bootstrapped N%" line and when it came.
type step struct {
	At      time.Duration
	Percent int
	Tag     string
	Summary string
}

// result is what one case produced.
type result struct {
	Case     torCase
	Outcome  string        // ok, stuck, failed, skipped, cancelled
	Max      int           // highest bootstrap percent seen
	Took     time.Duration // time to 100%, or how long we waited
	Steps    []step
	Warnings []string // distinct [warn] and [err] lines, redacted
	IsTor    string   // yes, no, or the error of the check
	CheckIn  time.Duration
	Onion    string // status of the -onion fetch, or ""
	OnionIn  time.Duration
	Log      string // redacted tor output
}

// Short is the one-line verdict printed while the program runs.
func (r result) Short() string {
	switch r.Outcome {
	case "ok":
		return fmt.Sprintf("ok in %s, check.torproject.org IsTor=%s", r.Took.Round(time.Second), r.IsTor)
	case "skipped":
		return "skipped: " + r.Case.Skip
	default:
		return fmt.Sprintf("%s at %d%% after %s", r.Outcome, r.Max, r.Took.Round(time.Second))
	}
}

// LogName is the file name of the case's log.
func (r result) LogName() string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(r.Case.Name) + ".log"
}

var bootRe = regexp.MustCompile(`Bootstrapped (\d+)% \(([^)]*)\): (.*)`)

// runCase starts tor for one case and waits for 100%, an exit, or timeout.
func runCase(ctx context.Context, env *torEnv, c torCase, timeout time.Duration, onion string) result {
	r := result{Case: c}
	if c.Skip != "" {
		r.Outcome = "skipped"
		return r
	}
	tmp, err := os.MkdirTemp("", "loft-reach-")
	if err != nil {
		r.Outcome, r.Log = "failed", err.Error()
		return r
	}
	defer os.RemoveAll(tmp)
	port, err := freePort()
	if err != nil {
		r.Outcome, r.Log = "failed", err.Error()
		return r
	}
	c.Plugins = linkPlugins(c.Plugins, filepath.Join(tmp, "pt"))
	torrc := filepath.Join(tmp, "torrc")
	if err := os.WriteFile(torrc, []byte(torrcFor(c, filepath.Join(tmp, "data"), port)), 0o600); err != nil {
		r.Outcome, r.Log = "failed", err.Error()
		return r
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, env.Tor, "-f", torrc)
	cmd.Dir = filepath.Dir(env.Tor)
	cmd.Env = torLibEnv(env.Tor)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Outcome, r.Log = "failed", err.Error()
		return r
	}
	cmd.Stderr = cmd.Stdout
	start := time.Now()
	if err := cmd.Start(); err != nil {
		r.Outcome, r.Log = "failed", "start tor: "+err.Error()
		return r
	}

	var (
		mu    sync.Mutex
		log   strings.Builder
		warns = map[string]bool{}
		done  = make(chan struct{})
		ready = make(chan struct{})
		once  sync.Once
	)
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := strings.ReplaceAll(redact(sc.Text()), tmp, "<tmp>")
			at := time.Since(start)
			mu.Lock()
			fmt.Fprintf(&log, "+%7.1fs %s\n", at.Seconds(), line)
			if m := bootRe.FindStringSubmatch(line); m != nil {
				p, _ := strconv.Atoi(m[1])
				r.Steps = append(r.Steps, step{At: at, Percent: p, Tag: m[2], Summary: m[3]})
				if p > r.Max {
					r.Max = p
				}
				if p == 100 {
					once.Do(func() { close(ready) })
				}
			}
			if k := warnKey(line); (strings.Contains(line, "[warn]") || strings.Contains(line, "[err]")) && !warns[k] {
				warns[k] = true
				r.Warnings = append(r.Warnings, line[strings.Index(line, "["):])
			}
			mu.Unlock()
		}
	}()

	select {
	case <-ready:
		r.Took = time.Since(start)
		r.Outcome = "ok"
		r.IsTor, r.CheckIn = checkTor(runCtx, port)
		if onion != "" {
			r.Onion, r.OnionIn = fetchVia(runCtx, port, onion)
		}
	case <-done:
		r.Took = time.Since(start)
		r.Outcome = "failed"
	case <-runCtx.Done():
		r.Took = time.Since(start)
		r.Outcome = "stuck"
		if ctx.Err() != nil {
			r.Outcome = "cancelled"
		}
	}
	cancel()
	_ = cmd.Wait()
	<-done
	mu.Lock()
	r.Log = log.String()
	mu.Unlock()
	return r
}

// linkPlugins puts a link without spaces in dir for every plugin whose path
// has a space, since tor splits ClientTransportPlugin on spaces. If the link
// cannot be made (Windows without the right), the path stays as it is.
func linkPlugins(ps []plugin, dir string) []plugin {
	out := make([]plugin, len(ps))
	for i, p := range ps {
		out[i] = p
		if !strings.ContainsAny(p.Path, " \t") {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			continue
		}
		link := filepath.Join(dir, fmt.Sprintf("%d-%s", i, filepath.Base(p.Path)))
		if os.Symlink(p.Path, link) == nil {
			out[i].Path = link
		}
	}
	return out
}

var numRe = regexp.MustCompile(`\d+`)

// warnKey drops tor's timestamp and numbers (PIDs, counters) so the same
// warning counts once.
func warnKey(line string) string {
	if i := strings.Index(line, "["); i >= 0 {
		line = line[i:]
	}
	return numRe.ReplaceAllString(line, "N")
}

// torrcFor writes the configuration of one case.
func torrcFor(c torCase, data string, port int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "DataDirectory %s\n", data)
	fmt.Fprintf(&b, "SocksPort 127.0.0.1:%d\n", port)
	b.WriteString("Log notice stdout\nAvoidDiskWrites 1\nClientOnly 1\n")
	if c.Proxy != "" {
		fmt.Fprintf(&b, "Socks5Proxy %s\n", c.Proxy)
	}
	if len(c.Bridges) > 0 {
		b.WriteString("UseBridges 1\n")
		for _, p := range c.Plugins {
			b.WriteString(p.Line() + "\n")
		}
		for _, l := range c.Bridges {
			b.WriteString("Bridge " + l + "\n")
		}
	}
	return b.String()
}

// torLibEnv lets Tor Browser's tor find the libraries next to it on Linux.
func torLibEnv(tor string) []string {
	env := os.Environ()
	if runtime.GOOS == "linux" {
		env = append(env, "LD_LIBRARY_PATH="+filepath.Dir(tor))
	}
	return env
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// viaTor is an HTTP client that goes through tor's SOCKS port and lets tor
// resolve names (so .onion works).
func viaTor(port int) (*http.Client, error) {
	d, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, proxy.Direct)
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("socks dialer has no DialContext")
	}
	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: &http.Transport{DialContext: cd.DialContext, Proxy: nil},
	}, nil
}

// checkTor asks check.torproject.org whether the request came out of Tor.
func checkTor(ctx context.Context, port int) (string, time.Duration) {
	cl, err := viaTor(port)
	if err != nil {
		return redact(err.Error()), 0
	}
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://check.torproject.org/api/ip", nil)
	resp, err := cl.Do(req)
	if err != nil {
		return redact(err.Error()), time.Since(start)
	}
	defer resp.Body.Close()
	var v struct {
		IsTor bool `json:"IsTor"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&v); err != nil {
		return "bad answer: " + redact(err.Error()), time.Since(start)
	}
	if v.IsTor {
		return "yes", time.Since(start)
	}
	return "no", time.Since(start)
}

// fetchVia GETs url through tor and returns the HTTP status or the error.
func fetchVia(ctx context.Context, port int, url string) (string, time.Duration) {
	cl, err := viaTor(port)
	if err != nil {
		return redact(err.Error()), 0
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err.Error(), 0
	}
	resp, err := cl.Do(req)
	if err != nil {
		return redact(err.Error()), time.Since(start)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	return resp.Status, time.Since(start)
}
