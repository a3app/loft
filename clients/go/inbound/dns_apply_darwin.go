//go:build darwin

package inbound

// dns_apply_darwin.go sets per-service DNS via networksetup and restores it on undo.

import (
	"bufio"
	"os/exec"
	"strings"
)

// applyDNS sets overlay DNS on every networksetup service and restores previous servers.
func applyDNS(_ string, servers []string) func() {
	if len(servers) == 0 {
		return func() {}
	}
	svcs := dnsServices()
	prev := make([][]string, len(svcs))
	for i, s := range svcs {
		prev[i] = dnsGet(s)
		dnsSet(s, servers)
	}
	return func() {
		for i, s := range svcs {
			dnsSet(s, prev[i])
		}
	}
}

// dnsServices lists enabled networksetup services, skipping the header and disabled (*).
func dnsServices() []string {
	out, err := exec.Command("/usr/sbin/networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil
	}
	var svcs []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	if sc.Scan() {
		// header line
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		svcs = append(svcs, line)
	}
	return svcs
}

// dnsGet returns current DNS servers for svc, or nil if none are set.
func dnsGet(svc string) []string {
	out, err := exec.Command("/usr/sbin/networksetup", "-getdnsservers", svc).Output()
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(out))
	if s == "" || strings.Contains(s, "aren't any DNS") {
		return nil
	}
	var ips []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			ips = append(ips, line)
		}
	}
	return ips
}

// dnsSet writes DNS servers for svc; empty ips clears them ("Empty").
func dnsSet(svc string, ips []string) {
	args := []string{"-setdnsservers", svc}
	if len(ips) == 0 {
		args = append(args, "Empty")
	} else {
		args = append(args, ips...)
	}
	_ = exec.Command("/usr/sbin/networksetup", args...).Run()
}
