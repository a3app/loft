package web

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestPreflightRejectsBadName(t *testing.T) {
	a := &ACME{Dir: t.TempDir()}
	ctx := context.Background()
	for _, name := range []string{
		"",
		"localhost",
		"127.0.0.1",
		"169.254.169.254",
		"::1",
		"http://example.com",
		"example.com:80",
		"example.com/x",
		"user@example.com",
		"example.com.",
		".example.com",
	} {
		// trailing dot is stripped, example.com. becomes example.com and is a DNS name
		if name == "example.com." {
			continue
		}
		if err := a.Preflight(ctx, name); err == nil {
			t.Fatalf("%q accepted", name)
		}
	}
}

func TestPreflightRejectsInternal(t *testing.T) {
	a := &ACME{Dir: t.TempDir()}
	ctx := context.Background()
	cases := []net.IP{
		net.IPv4(127, 0, 0, 1),
		net.IPv4(10, 0, 0, 1),
		net.IPv4(169, 254, 169, 254),
		net.IPv4(192, 168, 1, 1),
		net.ParseIP("::1"),
		net.ParseIP("fc00::1"),
	}
	for _, ip := range cases {
		a.lookup = func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: ip}}, nil
		}
		a.httpGet = func(context.Context, string) (string, error) {
			t.Fatal("GET to internal address")
			return "", nil
		}
		if err := a.Preflight(ctx, "vpn.example.net"); err == nil {
			t.Fatalf("%s accepted", ip)
		}
	}
}

func TestPreflightPinsPublic(t *testing.T) {
	here := net.IPv4(203, 0, 113, 10)
	var got string
	a := &ACME{
		Dir: t.TempDir(),
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: here}, {IP: net.IPv4(10, 0, 0, 1)}}, nil
		},
		localIPs: func() []net.IP { return []net.IP{here} },
		httpGet: func(_ context.Context, raw string) (string, error) {
			got = raw
			i := strings.LastIndex(raw, "/")
			return raw[i+1:], nil
		},
	}
	if err := a.Preflight(context.Background(), "Vpn.Example.NET"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "http://vpn.example.net/.well-known/acme-challenge/loft-probe-") {
		t.Fatalf("url %q", got)
	}
}

func TestPreflightMismatch(t *testing.T) {
	a := &ACME{
		Dir: t.TempDir(),
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.IPv4(203, 0, 113, 10)}}, nil
		},
		localIPs: func() []net.IP { return []net.IP{net.IPv4(198, 51, 100, 1)} },
		httpGet: func(context.Context, string) (string, error) {
			t.Fatal("GET on mismatch")
			return "", nil
		},
	}
	if err := a.Preflight(context.Background(), "vpn.example.net"); err == nil {
		t.Fatal("mismatch accepted")
	}
}
