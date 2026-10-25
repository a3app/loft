// One-shot CDN measure. Reads the cdn-get profile. Prints rates only.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/transport"
	"golang.org/x/net/proxy"
	"gopkg.in/yaml.v3"
)

const (
	nbyte    = 4 << 20
	profPath = "/Users/macbookpro16/Library/Application Support/loft/profiles/cdn-get.yaml"
)

type prof struct {
	Token     string `yaml:"token"`
	Outbounds []struct {
		URL  string `yaml:"url"`
		Path string `yaml:"path"`
		CDN  string `yaml:"cdn"`
	} `yaml:"outbounds"`
}

func main() {
	raw, err := os.ReadFile(profPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "profile: %v\n", err)
		os.Exit(1)
	}
	var p prof
	if err := yaml.Unmarshal(raw, &p); err != nil || p.Token == "" || len(p.Outbounds) == 0 {
		fmt.Fprintf(os.Stderr, "profile parse\n")
		os.Exit(1)
	}
	o := p.Outbounds[0]
	for i := 1; i <= 2; i++ {
		measure(p.Token, o.URL, o.Path, o.CDN, i)
		time.Sleep(2 * time.Second)
	}
}

func measure(token, rawURL, path, cdnName string, n int) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	hs, hcancel := context.WithTimeout(ctx, 20*time.Second)
	d, err := transport.Start(hs, token, config.Outbound{
		Name: "sweep", Type: "get", URL: rawURL, Path: path, CDN: cdnName,
		Fingerprint: "chrome", HeaderBudget: 16 << 10, Inflight: 16,
	})
	hcancel()
	if err != nil {
		fmt.Printf("run%d handshake err=%v\n", n, err)
		return
	}
	defer d.Close()
	time.Sleep(400 * time.Millisecond)
	tcp := h2TCP()
	ln, err := inbound.ListenSOCKS5("127.0.0.1:0", d)
	if err != nil {
		fmt.Printf("run%d socks err=%v\n", n, err)
		return
	}
	defer ln.Close()
	up, upErr := one(ctx, ln.Addr().String(), true)
	down, downErr := one(ctx, ln.Addr().String(), false)
	trips := ""
	if g, ok := d.(*transport.GET); ok {
		trips = g.TripSummary()
	}
	fmt.Printf("run%d h2_tcp=%d up=%.2f down=%.2f up_err=%v down_err=%v %s\n", n, tcp, up, down, upErr, downErr, trips)
}

func h2TCP() int {
	out, err := exec.Command("lsof", "-nP", "-a", "-p", strconv.Itoa(os.Getpid()), "-iTCP", "-sTCP:ESTABLISHED").Output()
	if err != nil {
		return -1
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "->") && strings.Contains(line, ":443") && !strings.Contains(line, "127.0.0.1") {
			n++
		}
	}
	return n
}

func one(ctx context.Context, socks string, upload bool) (float64, error) {
	d, err := proxy.SOCKS5("tcp", socks, nil, proxy.Direct)
	if err != nil {
		return 0, err
	}
	tr := &http.Transport{DialContext: func(c context.Context, network, addr string) (net.Conn, error) {
		return d.Dial(network, addr)
	}}
	cl := &http.Client{Transport: tr, Timeout: 35 * time.Second}
	cctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	var req *http.Request
	if upload {
		req, err = http.NewRequestWithContext(cctx, http.MethodPost, "https://speed.cloudflare.com/__up", bytes.NewReader(make([]byte, nbyte)))
		if err == nil {
			req.ContentLength = nbyte
		}
	} else {
		req, err = http.NewRequestWithContext(cctx, http.MethodGet, "https://speed.cloudflare.com/__down?bytes=4194304", nil)
	}
	if err != nil {
		return 0, err
	}
	t0 := time.Now()
	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 80))
		return 0, fmt.Errorf("http %s %s", resp.Status, slurp)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, err
	}
	dt := time.Since(t0)
	if dt <= 0 {
		return 0, fmt.Errorf("zero time")
	}
	return float64(nbyte) / dt.Seconds() * 8 / 1e6, nil
}
