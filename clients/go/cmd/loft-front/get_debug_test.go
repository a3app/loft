package main

// Debug loft-front GET: dial() is the same path as -probe / SOCKS.

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/transport"
)

func TestFrontOrderDefaultGET(t *testing.T) {
	got := frontOrder("auto", "https://example.com", true, true)
	if len(got) != 1 || got[0] != "get" {
		t.Fatalf("auto https %v", got)
	}
	if got := frontOrder("get", "https://example.com", true, true); len(got) != 1 || got[0] != "get" {
		t.Fatalf("get %v", got)
	}
	ws := frontOrder("auto", "wss://example.com/api/v1/sync", true, true)
	if len(ws) != 2 || ws[0] != "ws" || ws[1] != "get" {
		t.Fatalf("auto wss %v", ws)
	}
	all := frontOrder("all", "https://example.com", true, false)
	if len(all) != 5 || all[0] != "get" || all[2] != "h2" || all[4] != "h3" {
		t.Fatalf("all %v", all)
	}
	list := frontOrder("quic,h3,quic,nope", "https://example.com", true, false)
	if len(list) != 2 || list[0] != "quic" || list[1] != "h3" {
		t.Fatalf("list %v", list)
	}
	cfg := frontConfig("tok", "https://example.com", "get,ws", "", true, "")
	if len(cfg.Outbounds) != 2 || cfg.Outbounds[0].Type != "get" || cfg.Outbounds[1].Type != "ws" {
		t.Fatalf("cfg %+v", cfg.Outbounds)
	}
	one := frontConfig("tok", "https://cdn.example", "get", "/static/v3/build", true, "yandex")
	one.Fronts = parseKindList("get,ws,h2,quic,h3")
	if len(one.Outbounds) != 1 || one.Outbounds[0].Type != "get" {
		t.Fatalf("hello outbound %+v", one.Outbounds)
	}
	if got := strings.Join(one.EnabledFronts(), ","); got != "get,ws,h2,quic,h3" {
		t.Fatalf("hello fronts %s", got)
	}
}

func TestOutboundForH2MaxAge(t *testing.T) {
	frontH2MaxAgeMS = 60000
	t.Cleanup(func() { frontH2MaxAgeMS = 0 })
	o := outboundFor("https://example.com", "get", "/static/js", "")
	if o.H2MaxAgeMS != 60000 {
		t.Fatalf("h2_max_age_ms %d", o.H2MaxAgeMS)
	}
}

func TestOutboundForPerFrontPaths(t *testing.T) {
	frontWSPath = "/custom/sync"
	frontH2Path = "/custom/h2"
	frontH3Path = "/custom/h3"
	t.Cleanup(func() { frontWSPath = ""; frontH2Path = ""; frontH3Path = "" })
	ws := outboundFor("https://example.com", "ws", "/static/js", "")
	if !strings.HasSuffix(ws.URL, "/custom/sync") {
		t.Fatalf("ws %s", ws.URL)
	}
	h2 := outboundFor("https://example.com", "h2", "/static/js", "")
	if h2.Path != "/custom/h2" {
		t.Fatalf("h2 %s", h2.Path)
	}
	h3 := outboundFor("https://loft.example:444", "h3", "", "")
	if h3.Path != "/custom/h3" {
		t.Fatalf("h3 %s", h3.Path)
	}
	cfg := frontConfig("tok", "https://example.com", "get,ws,h2", "/static/v3/build", true, "")
	if len(cfg.Outbounds) != 3 {
		t.Fatalf("cfg %d", len(cfg.Outbounds))
	}
	if cfg.Outbounds[0].Type != "get" || cfg.Outbounds[0].Path != "/static/v3/build" {
		t.Fatalf("get %+v", cfg.Outbounds[0])
	}
	if cfg.Outbounds[1].Type != "ws" || !strings.HasSuffix(cfg.Outbounds[1].URL, "/custom/sync") {
		t.Fatalf("ws %+v", cfg.Outbounds[1])
	}
	if cfg.Outbounds[2].Type != "h2" || cfg.Outbounds[2].Path != "/custom/h2" {
		t.Fatalf("h2 %+v", cfg.Outbounds[2])
	}
}

func TestOutboundForH2IgnoresGETCoverPath(t *testing.T) {
	for _, kind := range []string{"h2", "h3", "l3"} {
		for _, path := range []string{"", "/static/js", "/static/v3/build", "app.js"} {
			o := outboundFor("https://loft.example:444", kind, path, "")
			if o.Path != "/api/v1/tunnel" {
				t.Fatalf("%s path %q → %q", kind, path, o.Path)
			}
		}
		o := outboundFor("https://loft.example:444", kind, "/custom/connect", "")
		if o.Path != "/custom/connect" {
			t.Fatalf("%s custom %q", kind, o.Path)
		}
	}
	get := outboundFor("https://loft.example:444", "get", "/static/v3/build", "")
	if get.Path != "/static/v3/build" {
		t.Fatalf("get kept %q", get.Path)
	}
	empty := outboundFor("https://loft.example:444", "get", "", "")
	if empty.Path != "/static/v3/build" {
		t.Fatalf("empty get %q", empty.Path)
	}
	legacy := outboundFor("https://loft.example:444", "get", "/static/js", "")
	if legacy.Path != "/static/js" {
		t.Fatalf("explicit legacy get %q", legacy.Path)
	}
}

func TestDebugFrontGETProbeAndEcho(t *testing.T) {
	const token = "test-token-please-change"
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()
	gs := transport.NewGETServer(token, "/static/v3/build")
	var auth, rid, newPath atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth") != "" {
			auth.Add(1)
		}
		if r.Header.Get("X-Request-Id") != "" {
			rid.Add(1)
		}
		if strings.Contains(r.URL.Path, "/new/") {
			newPath.Add(1)
		}
		gs.ServeHTTP(w, r)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	d, front, err := dial(ctx, token, ts.URL, "get", "/static/v3/build", true, "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if front != "get" {
		t.Fatalf("front %q", front)
	}
	if auth.Load() != 0 || rid.Load() != 0 || newPath.Load() != 0 {
		t.Fatalf("headers X-Auth=%d X-Request-Id=%d path-new=%d", auth.Load(), rid.Load(), newPath.Load())
	}
	conn, err := d.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := conn.Write([]byte("front")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "front" {
		t.Fatalf("echo %q", buf)
	}

	ln, err := inbound.ListenSOCKS5("127.0.0.1:0", d)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sc, err := socks5Connect(ln.Addr().String(), echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	_ = sc.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := sc.Write([]byte("socks")); err != nil {
		t.Fatal(err)
	}
	sbuf := make([]byte, 5)
	if _, err := io.ReadFull(sc, sbuf); err != nil {
		t.Fatal(err)
	}
	if string(sbuf) != "socks" {
		t.Fatalf("socks echo %q", sbuf)
	}
	if auth.Load() != 0 || rid.Load() != 0 || newPath.Load() != 0 {
		t.Fatalf("after socks X-Auth=%d X-Request-Id=%d path-new=%d", auth.Load(), rid.Load(), newPath.Load())
	}
}

func socks5Connect(proxy, target string) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		_ = c.Close()
		return nil, err
	}
	var hs [2]byte
	if _, err := io.ReadFull(c, hs[:]); err != nil {
		_ = c.Close()
		return nil, err
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		_ = c.Close()
		return nil, fmt.Errorf("socks target %s", host)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(p))
	req := append([]byte{0x05, 0x01, 0x00, 0x01}, ip...)
	req = append(req, pb[:]...)
	if _, err := c.Write(req); err != nil {
		_ = c.Close()
		return nil, err
	}
	var rep [10]byte
	if _, err := io.ReadFull(c, rep[:]); err != nil {
		_ = c.Close()
		return nil, err
	}
	if rep[1] != 0 {
		_ = c.Close()
		return nil, fmt.Errorf("socks rep %d", rep[1])
	}
	return c, nil
}
