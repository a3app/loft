package adm

// This file is the worker side of background jobs. Long work (installing
// packages, updating loft, making or restoring a backup) does not run inside
// the helper's request, which has a short deadline. Instead jobs.go records
// the job in job.json and starts a transient systemd unit that runs this
// binary as "run-job"; RunJob below then does the work and writes its
// progress to job.log, which the panel shows live.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

// RunJob is the body of the transient unit loft-panel-job: it does the job
// recorded in job.json, appends to job.log, and records how it ended. It runs
// as root outside the helper's sandbox (apt writes /usr, downloads use the
// network), and only ever does what the catalog or a release tag says.
// The job runs by kind; a panic becomes an error, so the job always ends
// with a recorded state ("done" or "error") instead of staying "running".
func (o *Ops) RunJob(ctx context.Context) error {
	j, err := o.readJob()
	if err != nil {
		return err
	}
	if j.State != "running" {
		return fmt.Errorf("job %s is %s, not pending", j.ID, j.State)
	}
	f, err := os.OpenFile(o.jobLog(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// The recovery phrase is read once and leaves the job file at once.
	phrase := ""
	if j.Backup != nil && j.Backup.Phrase != "" {
		phrase, j.Backup.Phrase = j.Backup.Phrase, ""
		o.saveJob(j)
	}
	r := &runner{o: o, j: &j, w: f}
	jerr := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("internal error: %v", p)
			}
		}()
		switch j.Kind {
		case "packages":
			if j.Plan == nil {
				return errors.New("job has no plan")
			}
			return r.packages(ctx, *j.Plan)
		case "update":
			return r.update(ctx, j.Tag)
		case "backup", "restore":
			if j.Backup == nil {
				return errors.New("job has no backup")
			}
			if j.Kind == "backup" {
				return r.backup(ctx, j.Backup)
			}
			return r.restore(ctx, j.Backup, phrase)
		}
		return fmt.Errorf("unknown job kind %q", j.Kind)
	}()
	j.Now, j.Ended = "", time.Now().UTC()
	if jerr != nil {
		j.State, j.Error = "error", jerr.Error()
		r.say("✗ %s", jerr)
	} else {
		j.State = "done"
		r.say("✓ done")
	}
	return o.saveJob(j)
}

// runner carries one job while it runs: the Ops it works on, the job record
// (saved as it advances) and the job log that every step writes to.
type runner struct {
	o *Ops
	j *admproto.Job
	w io.Writer
}

// say writes one progress line to the job log.
func (r *runner) say(f string, a ...any) { fmt.Fprintf(r.w, "▸ "+f+"\n", a...) }

// step records what the job is doing now (for example "install:mail") in
// job.json, so the panel can show the current step.
func (r *runner) step(now string) {
	r.j.Now = now
	r.o.saveJob(*r.j)
}

// path maps a system path under Root (tests).
func (r *runner) path(p string) string { return r.o.sys(p) }

// aptEnv keeps apt and dpkg from asking anything: old config files stay,
// needrestart does not restart services behind the panel's back.
var aptEnv = []string{"DEBIAN_FRONTEND=noninteractive", "UCF_FORCE_CONFFOLD=1", "NEEDRESTART_SUSPEND=1", "LC_ALL=C.UTF-8"}

// cmd runs a command with its output going to the job log, after echoing
// the command line itself. The apt environment is set for every command, so
// nothing it starts can stop to ask a question. Tests replace it via Stream.
func (r *runner) cmd(ctx context.Context, name string, args ...string) error {
	fmt.Fprintf(r.w, "$ %s %s\n", name, strings.Join(args, " "))
	if r.o.Stream != nil {
		return r.o.Stream(ctx, r.w, name, args...)
	}
	c := exec.CommandContext(ctx, name, args...)
	c.Env = append(os.Environ(), aptEnv...)
	c.Stdout, c.Stderr = r.w, r.w
	return c.Run()
}

// apt runs apt-get non-interactively: it waits up to 5 minutes for the dpkg
// lock (another apt may be running) and keeps existing config files.
func (r *runner) apt(ctx context.Context, args ...string) error {
	base := []string{"-y", "-q", "-o", "DPkg::Lock::Timeout=300",
		"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}
	if err := r.cmd(ctx, "apt-get", append(base, args...)...); err != nil {
		return fmt.Errorf("apt-get %s failed: %v", args[0], err)
	}
	return nil
}

// packages carries out a package plan from the panel: removals first, then
// installs, then upgrades. Each package is handled by its catalog method:
// apt packages through apt-get, "release" and "bin" packages through their
// own downloaders. Apt repositories and package lists are refreshed only
// when some package in the plan comes from apt. On upgrade, release and bin
// packages are simply installed again, apt packages are upgraded in place
// without pulling anything new. The first failure stops the job.
func (r *runner) packages(ctx context.Context, plan catalog.Plan) error {
	if err := plan.Check(); err != nil {
		return err
	}
	find := func(k string) catalog.Package { p, _ := catalog.Find(k); return p }
	// Dependents first: they come after what they require in the catalog.
	for i := len(plan.Remove) - 1; i >= 0; i-- {
		k := plan.Remove[i]
		r.step("remove:" + k)
		r.say("remove %s", k)
		rm := r.remove
		if find(k).Method == catalog.MethodRelease {
			rm = func(ctx context.Context, p catalog.Package, _ bool) error { return r.removeRelease(ctx, p) }
		}
		if err := rm(ctx, find(k), plan.Purge); err != nil {
			return fmt.Errorf("remove %s: %w", k, err)
		}
	}
	aptWork := false
	for _, k := range append(append([]string(nil), plan.Install...), plan.Upgrade...) {
		aptWork = aptWork || find(k).Method == catalog.MethodApt
	}
	if aptWork {
		for _, k := range plan.Install {
			if p := find(k); p.Repo != nil {
				r.step("repo:" + k)
				if err := r.addRepo(p); err != nil {
					return fmt.Errorf("install %s: %w", k, err)
				}
			}
		}
		r.step("update")
		r.say("refresh package lists")
		if err := r.apt(ctx, "update"); err != nil {
			return err
		}
	}
	for _, k := range plan.Install {
		r.step("install:" + k)
		r.say("install %s", k)
		in := r.install
		if find(k).Method == catalog.MethodRelease {
			in = r.installRelease
		}
		if err := in(ctx, find(k)); err != nil {
			return fmt.Errorf("install %s: %w", k, err)
		}
	}
	for _, k := range plan.Upgrade {
		r.step("upgrade:" + k)
		r.say("upgrade %s", k)
		p := find(k)
		if p.Method == catalog.MethodRelease {
			if err := r.installRelease(ctx, p); err != nil {
				return fmt.Errorf("upgrade %s: %w", k, err)
			}
			continue
		}
		if p.Method == catalog.MethodBin {
			if err := r.install(ctx, p); err != nil {
				return fmt.Errorf("upgrade %s: %w", k, err)
			}
			continue
		}
		if err := r.apt(ctx, append([]string{"install", "--only-upgrade"}, p.Apt...)...); err != nil {
			return fmt.Errorf("upgrade %s: %w", k, err)
		}
	}
	return nil
}

// install installs one apt or bin package: get the software (apt-get with
// any debconf answers the catalog gives, or the bin downloader), write the
// package's files from the catalog, then enable and start its service and
// check it stays up for Settle (a bad config usually crashes it at once). A
// service that fails to start fails the job with the tail of its journal in
// the log.
func (r *runner) install(ctx context.Context, p catalog.Package) error {
	if p.Method == catalog.MethodBin {
		if err := r.installBin(ctx, p); err != nil {
			return err
		}
	} else {
		if len(p.Preseed) > 0 {
			f := filepath.Join(r.o.stateDir(), "preseed")
			if err := os.WriteFile(f, []byte(strings.Join(p.Preseed, "\n")+"\n"), 0o600); err != nil {
				return err
			}
			err := r.cmd(ctx, "debconf-set-selections", f)
			os.Remove(f)
			if err != nil {
				return fmt.Errorf("debconf answers: %v", err)
			}
		}
		// Extras already on the server are left out of the command: naming them
		// would mark them installed by hand, and a remove would never take them.
		before := r.o.dpkgState(ctx, p.Extra)
		var auto []string
		for _, n := range p.Extra {
			if !before[n].ok {
				auto = append(auto, n)
			}
		}
		if err := r.apt(ctx, append(append([]string{"install", "--no-install-recommends"}, p.Apt...), auto...)...); err != nil {
			return err
		}
		// The extras installed here go when nothing needs them.
		if len(auto) > 0 {
			r.cmd(ctx, "apt-mark", append([]string{"auto"}, auto...)...)
		}
	}
	for _, path := range sortedKeys(p.Files) {
		dst := r.path(path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := storeWrite(dst, []byte(p.Files[path]), 0o644); err != nil {
			return err
		}
		r.say("wrote %s", path)
	}
	if p.Unit == "" {
		return nil
	}
	if p.Method == catalog.MethodBin {
		r.cmd(ctx, "systemctl", "daemon-reload")
	}
	r.cmd(ctx, "systemctl", "enable", p.Unit)
	if err := r.cmd(ctx, "systemctl", "restart", p.Unit); err != nil {
		return r.unitFailed(ctx, p.Unit)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(r.o.Settle):
	}
	if st, _ := r.o.Systemctl(ctx, "is-active", p.Unit); strings.TrimSpace(st) != "active" {
		return r.unitFailed(ctx, p.Unit)
	}
	return nil
}

// unitFailed copies the last journal lines of unit into the job log and
// returns an error pointing at them.
func (r *runner) unitFailed(ctx context.Context, unit string) error {
	r.cmd(ctx, "journalctl", "-u", unit, "-n", "30", "--no-pager", "-o", "cat")
	return fmt.Errorf("%s did not start (its log is above)", unit)
}

// remove uninstalls one apt or bin package. It stops the service first, then
// deletes the binary (bin) or removes the apt packages together with the
// dependencies only they needed, and the apt repository the package added.
// With purge it also wipes the package's data directories (mailboxes,
// databases), so there is no way back.
func (r *runner) remove(ctx context.Context, p catalog.Package, purge bool) error {
	if p.Unit != "" {
		r.cmd(ctx, "systemctl", "disable", "--now", p.Unit)
	}
	if p.Method == catalog.MethodBin {
		if p.Bin != nil {
			os.Remove(r.path(p.Bin.Path))
			os.Remove(filepath.Dir(r.path(p.Bin.Path)))
			r.say("removed %s", p.Bin.Path)
		}
		for _, path := range sortedKeys(p.Files) {
			os.Remove(r.path(path))
		}
		if p.Unit != "" {
			r.cmd(ctx, "systemctl", "daemon-reload")
		}
	} else {
		verb := "remove"
		if purge {
			verb = "purge"
		}
		deps := r.depClosure(ctx, append(append([]string(nil), p.Apt...), p.Extra...))
		if err := r.apt(ctx, append([]string{verb}, p.Apt...)...); err != nil {
			return err
		}
		// What apt pulled in for these packages (dovecot-core, php8.3-fpm) would
		// stay and keep running. Remove those of them apt itself now calls
		// unused; nothing installed needs them. Not a plain autoremove: that
		// would take whatever else is unused on the server.
		if left := r.unusedOf(ctx, deps); len(left) > 0 {
			r.say("remove what only %s needed: %s", p.Key, strings.Join(left, " "))
			if err := r.apt(ctx, append([]string{verb}, left...)...); err != nil {
				return err
			}
		}
		for _, path := range sortedKeys(p.Files) {
			os.Remove(r.path(path))
		}
		if p.Repo != nil {
			os.Remove(r.path(repoList(p.Repo)))
			os.Remove(r.path(repoKey(p.Repo)))
			r.say("removed the %s apt repository", p.Repo.Name)
		}
	}
	if purge {
		for _, d := range p.DataDirs {
			if err := os.RemoveAll(r.path(d)); err != nil {
				return err
			}
			r.say("wiped %s", d)
		}
	}
	return nil
}

// repoList and repoKey are where a package's own apt repository list and
// signing key are written; the "loft-" prefix marks them as the panel's.
func repoList(rp *catalog.Repo) string {
	return "/etc/apt/sources.list.d/loft-" + rp.Name + ".list"
}
func repoKey(rp *catalog.Repo) string { return "/etc/apt/keyrings/loft-" + rp.Name + ".asc" }

// addRepo writes the repository's key (from the binary) and its list.
func (r *runner) addRepo(p catalog.Package) error {
	code := r.codename()
	if !slices.Contains(p.Repo.Codenames, code) {
		return fmt.Errorf("the %s repository has no packages for this OS (%s)", p.Repo.Name, firstNonEmpty(code, "unknown"))
	}
	if err := os.MkdirAll(r.path("/etc/apt/keyrings"), 0o755); err != nil {
		return err
	}
	if err := storeWrite(r.path(repoKey(p.Repo)), []byte(p.Repo.Key), 0o644); err != nil {
		return err
	}
	line := fmt.Sprintf("# Written by loft-panel; removed with the package.\ndeb [signed-by=%s] %s %s main\n",
		repoKey(p.Repo), fmt.Sprintf(p.Repo.URL, code), code)
	if err := os.MkdirAll(r.path("/etc/apt/sources.list.d"), 0o755); err != nil {
		return err
	}
	if err := storeWrite(r.path(repoList(p.Repo)), []byte(line), 0o644); err != nil {
		return err
	}
	r.say("added the %s apt repository for %s", p.Repo.Name, code)
	return nil
}

// codename is VERSION_CODENAME from os-release.
func (r *runner) codename() string {
	f, err := os.Open(r.path("/etc/os-release"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VERSION_CODENAME="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

// sortedKeys returns the keys of m in order, so files are written and logged
// in the same order every time.
func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for s := range m {
		k = append(k, s)
	}
	slices.Sort(k)
	return k
}

// depClosure is every package names depend on, recursively (names included).
func (r *runner) depClosure(ctx context.Context, names []string) map[string]bool {
	seen := map[string]bool{}
	todo := append([]string(nil), names...)
	for len(todo) > 0 && len(seen) < 2000 {
		n := todo[0]
		todo = todo[1:]
		if seen[n] {
			continue
		}
		seen[n] = true
		out, err := r.o.exec(ctx, "dpkg-query", "-W", "-f=${Depends}, ${Pre-Depends}", n)
		if err != nil {
			continue
		}
		for _, alt := range strings.Split(out, ",") {
			for _, d := range strings.Split(alt, "|") {
				d = strings.TrimSpace(d)
				if i := strings.IndexAny(d, " (:"); i >= 0 {
					d = d[:i]
				}
				if d != "" && !seen[d] {
					todo = append(todo, d)
				}
			}
		}
	}
	return seen
}

// unusedOf is the part of deps that apt-get autoremove would remove now.
func (r *runner) unusedOf(ctx context.Context, deps map[string]bool) []string {
	out, _ := r.o.exec(ctx, "apt-get", "-s", "-o", "DPkg::Lock::Timeout=300", "autoremove")
	var left []string
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && f[0] == "Remv" && deps[f[1]] {
			left = append(left, f[1])
		}
	}
	slices.Sort(left)
	return left
}
