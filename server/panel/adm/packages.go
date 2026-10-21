package adm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

func runExec(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func (o *Ops) exec(ctx context.Context, name string, args ...string) (string, error) {
	if o.Exec != nil {
		return o.Exec(ctx, name, args...)
	}
	return runExec(ctx, name, args...)
}

type dpkgRow struct {
	ok      bool // ii: installed and configured
	version string
	kb      int
}

// dpkgState asks dpkg about names in one call; missing names are absent.
func (o *Ops) dpkgState(ctx context.Context, names []string) map[string]dpkgRow {
	m := map[string]dpkgRow{}
	if len(names) == 0 {
		return m
	}
	// dpkg-query exits 1 when one name is unknown, but still prints the rest.
	out, _ := o.exec(ctx, "dpkg-query", append([]string{"-W", "-f=${Package}\t${db:Status-Abbrev}\t${Version}\t${Installed-Size}\n"}, names...)...)
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Split(ln, "\t")
		if len(f) != 4 {
			continue
		}
		kb, _ := strconv.Atoi(f[3])
		m[f[0]] = dpkgRow{ok: strings.HasPrefix(f[1], "ii"), version: f[2], kb: kb}
	}
	return m
}

// candidates is apt-cache policy's Candidate for each installed name.
func (o *Ops) candidates(ctx context.Context, names []string) map[string]string {
	m := map[string]string{}
	if len(names) == 0 {
		return m
	}
	out, _ := o.exec(ctx, "apt-cache", append([]string{"policy"}, names...)...)
	cur := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		ln := sc.Text()
		if !strings.HasPrefix(ln, " ") && strings.HasSuffix(ln, ":") {
			cur = strings.TrimSuffix(ln, ":")
			continue
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "Candidate:"); ok && cur != "" {
			m[cur] = strings.TrimSpace(v)
		}
	}
	return m
}

// unitStates reads ActiveState and UnitFileState of units in one call;
// systemctl show answers for unknown units too (inactive, no file state).
func (o *Ops) unitStates(ctx context.Context, units []string) (active map[string]string, enabled map[string]bool) {
	active, enabled = map[string]string{}, map[string]bool{}
	if len(units) == 0 {
		return
	}
	out, _ := o.Systemctl(ctx, append([]string{"show", "--property=Id,ActiveState,UnitFileState"}, units...)...)
	for _, blk := range strings.Split(out, "\n\n") {
		var id, act, file string
		for _, ln := range strings.Split(blk, "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(ln), "=")
			switch k {
			case "Id":
				id = v
			case "ActiveState":
				act = v
			case "UnitFileState":
				file = v
			}
		}
		if id != "" {
			active[id], enabled[id] = act, file == "enabled"
		}
	}
	return
}

// Packages is every catalog entry as it stands, marking those a running job
// is changing.
func (o *Ops) Packages(ctx context.Context) ([]admproto.Package, error) {
	all := catalog.All()
	var names, units []string
	for _, p := range all {
		names = append(names, p.Apt...)
		if p.Unit != "" {
			units = append(units, p.Unit)
		}
	}
	dp := o.dpkgState(ctx, names)
	var have []string
	for n, r := range dp {
		if r.ok {
			have = append(have, n)
		}
	}
	cand := o.candidates(ctx, have)
	active, enabled := o.unitStates(ctx, units)
	node := o.NodeStatus(ctx)
	busy := map[string]string{}
	if j, err := o.JobGet(ctx, false); err == nil && j.State == "running" && j.Plan != nil {
		for _, k := range j.Plan.Install {
			busy[k] = "install"
		}
		for _, k := range j.Plan.Remove {
			busy[k] = "remove"
		}
		for _, k := range j.Plan.Upgrade {
			busy[k] = "upgrade"
		}
	}

	out := make([]admproto.Package, 0, len(all))
	for _, p := range all {
		s := admproto.Package{Key: p.Key, Group: p.Group, Base: p.Base(), Service: p.Unit != "", Ports: p.Ports, Busy: busy[p.Key]}
		if p.Unit != "" {
			s.Active, s.Enabled = active[p.Unit], enabled[p.Unit]
		}
		switch {
		case p.Key == "node" || p.Key == "sstp" || p.Key == "proxy": // SSTP and proxies are part of loft-node
			s.Installed, s.Version = true, nodeVersion(node.Version)
		case p.Method == catalog.MethodRelease:
			bin := filepath.Join(p.Release.Dir, p.Release.Bin)
			if _, err := os.Stat(filepath.Join(o.Root, bin)); err == nil {
				s.Installed = true
				if out, err := o.exec(ctx, bin, "version"); err == nil {
					if f := strings.Fields(out); len(f) > 1 {
						s.Version = f[1]
					}
				}
			} else {
				s.DiskMB = p.SizeMB
			}
		case p.Method == catalog.MethodBin && p.Bin != nil:
			s.DiskMB = p.SizeMB
			if _, err := os.Stat(o.sys(p.Bin.Path)); err == nil {
				s.Installed = true
				s.Version = o.binVersion(p)
				if pin := strings.TrimPrefix(p.Bin.Tag, "v"); pin != "" && pin != s.Version {
					s.Candidate = pin
				}
			}
		case len(p.Apt) > 0:
			s.Installed = true
			for _, n := range p.Apt {
				r := dp[n]
				s.Installed = s.Installed && r.ok
				s.DiskMB += r.kb
			}
			s.DiskMB = (s.DiskMB + 1023) / 1024
			if s.Installed {
				s.Version = dp[p.Version()].version
				if c := cand[p.Version()]; c != "" && c != "(none)" && c != s.Version && o.versionGT(ctx, c, s.Version) {
					s.Candidate = c
				}
			} else {
				s.DiskMB = p.SizeMB
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// versionGT is dpkg's own comparison (epochs, ~, Debian revisions).
func (o *Ops) versionGT(ctx context.Context, a, b string) bool {
	_, err := o.exec(ctx, "dpkg", "--compare-versions", a, "gt", b)
	return err == nil
}

// PackageUnit turns an installed package's service on or off. The base
// services are not switchable here: the panel is reached through them.
func (o *Ops) PackageUnit(ctx context.Context, req admproto.PackageUnit) (admproto.Package, error) {
	p, ok := catalog.Find(req.Key)
	if !ok {
		return admproto.Package{}, fmt.Errorf("unknown package %q", req.Key)
	}
	if p.Base() || p.Unit == "" {
		return admproto.Package{}, fmt.Errorf("package %q has no service to switch", req.Key)
	}
	if j, err := o.JobGet(ctx, false); err == nil && j.State == "running" {
		return admproto.Package{}, errors.New("a package job is running: wait for it to finish")
	}
	if !o.packageInstalled(ctx, p) {
		return admproto.Package{}, fmt.Errorf("package %q is not installed", req.Key)
	}
	verb := "disable"
	if req.On {
		verb = "enable"
	}
	// Outside the sandbox: for a unit with a SysV script (fail2ban) systemctl
	// also rewrites /etc/rc*.d, which is read-only in here.
	if out, err := o.exec(ctx, "systemd-run", "--wait", "--pipe", "--collect", "--quiet", "systemctl", verb, "--now", p.Unit); err != nil {
		return admproto.Package{}, fmt.Errorf("systemctl %s %s: %v %s", verb, p.Unit, err, strings.TrimSpace(out))
	}
	all, err := o.Packages(ctx)
	if err != nil {
		return admproto.Package{}, err
	}
	for _, s := range all {
		if s.Key == req.Key {
			return s, nil
		}
	}
	return admproto.Package{}, errors.New("package vanished")
}

func (o *Ops) packageInstalled(ctx context.Context, p catalog.Package) bool {
	if p.Method == catalog.MethodBin && p.Bin != nil {
		_, err := os.Stat(o.sys(p.Bin.Path))
		return err == nil
	}
	if p.Method == catalog.MethodRelease && p.Release != nil {
		_, err := os.Stat(filepath.Join(o.Root, p.Release.Dir, p.Release.Bin))
		return err == nil
	}
	if len(p.Apt) == 0 {
		return false
	}
	return o.dpkgState(ctx, p.Apt)[p.Apt[0]].ok
}

func (o *Ops) binVersion(p catalog.Package) string {
	if len(p.DataDirs) == 0 {
		return ""
	}
	b, err := os.ReadFile(o.sys(filepath.Join(p.DataDirs[0], "version")))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// nodeVersion is 1.0.22 from "loft-node 1.0.22 (LOFT/1.0)".
func nodeVersion(v string) string {
	if f := strings.Fields(v); len(f) > 1 && f[0] == "loft-node" {
		return f[1]
	}
	return v
}

// installedKeys is which apt packages of the catalog are fully installed.
func (o *Ops) installedKeys(ctx context.Context) map[string]bool {
	var names []string
	for _, p := range catalog.All() {
		if p.Method == catalog.MethodApt {
			names = append(names, p.Apt...)
		}
	}
	dp := o.dpkgState(ctx, names)
	out := map[string]bool{}
	for _, p := range catalog.All() {
		if p.Method == catalog.MethodRelease {
			_, err := os.Stat(filepath.Join(o.Root, p.Release.Dir, p.Release.Bin))
			out[p.Key] = err == nil
			continue
		}
		if p.Method == catalog.MethodBin && p.Bin != nil {
			_, err := os.Stat(o.sys(p.Bin.Path))
			out[p.Key] = err == nil
			continue
		}
		if p.Method != catalog.MethodApt {
			continue
		}
		ok := true
		for _, n := range p.Apt {
			ok = ok && dp[n].ok
		}
		out[p.Key] = ok
	}
	return out
}
