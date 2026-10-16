//go:build darwin

package inbound

// route_darwin.go installs IPv4 split default (0/1 + 128/1) and optional kill-switch.

import (
	"os/exec"
	"strings"
)

// enableKillSwitch adds split default then blackholes 0/0 on undo (no restore of the physical default).
func enableKillSwitch(tun string, exclude []string) (func(), error) {
	undoSplit, err := addSplitDefault(tun, exclude)
	if err != nil {
		return nil, err
	}
	return func() {
		undoSplit()
		// do not restore the physical default: that is the kill-switch
		_ = exec.Command("route", "-n", "add", "-inet", "0.0.0.0/0", "127.0.0.1", "-blackhole").Run()
	}, nil
}

// addSplitDefault pins exclude hosts via the physical gateway, then 0/1 and 128/1 on tun.
func addSplitDefault(tun string, exclude []string) (func(), error) {
	var undo [][]string
	for _, host := range exclude {
		if host == "" {
			continue
		}
		if err := run("route", "-n", "add", "-host", host, gateway()); err == nil {
			undo = append(undo, []string{"delete", "-host", host})
		}
	}
	// IPv4 split default only. ::/0 waits for overlay IPv6.
	if err := run("route", "-n", "add", "-net", "0.0.0.0/1", "-interface", tun); err != nil {
		return nil, err
	}
	undo = append(undo, []string{"delete", "-net", "0.0.0.0/1"})
	if err := run("route", "-n", "add", "-net", "128.0.0.0/1", "-interface", tun); err != nil {
		return nil, err
	}
	undo = append(undo, []string{"delete", "-net", "128.0.0.0/1"})
	return func() {
		for i := len(undo) - 1; i >= 0; i-- {
			_ = exec.Command("route", "-n", undo[i][0], undo[i][1], undo[i][2]).Run()
		}
	}, nil
}

// gateway is the IPv4 default gateway from `route get default`.
func gateway() string {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
		}
	}
	return ""
}
