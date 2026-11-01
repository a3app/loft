package main

// Runtime SOCKS5 bind. The GUI writes "socks host:port" or "socks off"
// on stdin; the mux session stays up.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/transport"
)

// serveSOCKS binds addr (unless off) and rebinds on stdin socks commands.
func serveSOCKS(ctx context.Context, addr string, d transport.Dialer) *inbound.SocksGate {
	g := inbound.NewSocksGate(d)
	if !inbound.SocksOff(addr) {
		status("connecting", "socks", "", false)
		bound, err := g.Set(addr)
		if err != nil {
			status("error", "socks "+err.Error(), "", false)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "socks5 %s\n", bound)
	}
	go watchSOCKS(ctx, g)
	return g
}

// onSwitch drops the live front so Supervise dials the next one.
// SOCKS and TUN stay up. The GUI does not send this line.
var onSwitch func()

// watchSOCKS reads "socks ADDR" / "socks off" / "switch" until ctx is done.
func watchSOCKS(ctx context.Context, g *inbound.SocksGate) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		cmd, arg, _ := strings.Cut(line, " ")
		if strings.EqualFold(cmd, "switch") {
			if onSwitch != nil {
				onSwitch()
			}
			continue
		}
		if !strings.EqualFold(cmd, "socks") {
			continue
		}
		bound, err := g.Set(strings.TrimSpace(arg))
		if err != nil {
			fmt.Fprintf(os.Stderr, "socks %v\n", err)
			continue
		}
		if bound == "" {
			fmt.Fprintf(os.Stderr, "socks5 off\n")
		} else {
			fmt.Fprintf(os.Stderr, "socks5 %s\n", bound)
		}
	}
}
