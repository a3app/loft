//go:build linux

package inbound

// route_linux.go installs IPv4 split default (0/1 + 128/1) and optional kill-switch.

// enableKillSwitch adds split default then blackholes 0/0 on undo.
func enableKillSwitch(tun string, exclude []string) (func(), error) {
	undo, err := addSplitDefault(tun, exclude)
	if err != nil {
		return nil, err
	}
	return func() {
		undo()
		_ = run("ip", "route", "replace", "blackhole", "default")
	}, nil
}

// addSplitDefault pins exclude hosts, then 0/1 and 128/1 on tun. ::/0 waits for overlay IPv6.
func addSplitDefault(tun string, exclude []string) (func(), error) {
	var cmds [][]string
	for _, host := range exclude {
		if host == "" {
			continue
		}
		if err := run("ip", "route", "add", host, "via", gateway()); err == nil {
			cmds = append(cmds, []string{"ip", "route", "del", host})
		}
	}
	// IPv4 split default only. ::/0 waits for overlay IPv6.
	if err := run("ip", "route", "add", "0.0.0.0/1", "dev", tun); err != nil {
		return nil, err
	}
	cmds = append(cmds, []string{"ip", "route", "del", "0.0.0.0/1"})
	if err := run("ip", "route", "add", "128.0.0.0/1", "dev", tun); err != nil {
		return nil, err
	}
	cmds = append(cmds, []string{"ip", "route", "del", "128.0.0.0/1"})
	return func() {
		for i := len(cmds) - 1; i >= 0; i-- {
			_ = run(cmds[i][0], cmds[i][1:]...)
		}
	}, nil
}

// gateway is unused on Linux (exclude hosts are added without via).
func gateway() string { return "" }
