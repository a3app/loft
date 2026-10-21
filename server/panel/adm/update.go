package adm

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/a3app/loft/internal/version"
)

// Releases is where release assets are downloaded from.
const Releases = "https://github.com/a3app/loft/releases/download"

// maxAsset bounds one download.
const maxAsset = 256 << 20

// panelUnits must all be active after an update, or it is rolled back.
var panelUnits = []string{"loft-node.service", "loft-panel-adm.service", "loft-panel.service"}

// update installs release tag of node and panel. A tarball install
// (/usr/local/bin) runs the release's own install.sh; a .deb install gets
// the release's two .deb files. Every download is checked against the
// release's SHA256SUMS. If the services do not come back, the previous
// version is put back.
func (r *runner) update(ctx context.Context, tag string) error {
	if !tagRe.MatchString(tag) {
		return fmt.Errorf("%q is not a release tag", tag)
	}
	ver := strings.TrimPrefix(tag, "v")
	if ver == version.Version {
		return fmt.Errorf("version %s is already installed", ver)
	}
	work := filepath.Join(r.o.stateDir(), "update")
	os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if r.o.dpkgState(ctx, []string{"loft-panel"})["loft-panel"].ok {
		return r.updateDeb(ctx, tag, ver, work)
	}
	return r.updateTar(ctx, tag, ver, work)
}

func (r *runner) updateTar(ctx context.Context, tag, ver, work string) error {
	r.step("download")
	name := fmt.Sprintf("loft-panel-%s-linux-%s.tar.gz", ver, runtime.GOARCH)
	tgz, err := r.fetchChecked(ctx, tag, fmt.Sprintf("loft-panel-%s-SHA256SUMS.txt", ver), name, work)
	if err != nil {
		return err
	}
	dir := filepath.Join(work, "x")
	if err := untar(tgz, dir); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	inst := filepath.Join(dir, strings.TrimSuffix(name, ".tar.gz"), "install.sh")
	if _, err := os.Stat(inst); err != nil {
		return fmt.Errorf("%s has no install.sh", name)
	}

	r.step("backup")
	bins := r.binaries()
	back := filepath.Join(r.o.stateDir(), "rollback")
	os.RemoveAll(back)
	if err := os.MkdirAll(back, 0o700); err != nil {
		return err
	}
	for i, b := range bins {
		if err := copyFile(b, filepath.Join(back, fmt.Sprint(i)), 0o755); err != nil {
			return fmt.Errorf("back up %s: %w", b, err)
		}
	}
	r.say("backed up %s", strings.Join(bins, ", "))

	r.step("install")
	r.say("install %s", tag)
	ierr := r.cmd(ctx, "sh", inst, "--unattended")
	if ierr == nil {
		ierr = r.settled(ctx, ver)
	}
	if ierr == nil {
		return nil
	}
	r.step("rollback")
	r.say("roll back to %s: %v", version.Version, ierr)
	for i, b := range bins {
		if err := copyFile(filepath.Join(back, fmt.Sprint(i)), b, 0o755); err != nil {
			return fmt.Errorf("%v; rollback failed: %v", ierr, err)
		}
	}
	// A unit that crashed in a loop hit its start limit: clear it first.
	r.cmd(ctx, "systemctl", append([]string{"reset-failed"}, panelUnits...)...)
	r.cmd(ctx, "systemctl", append([]string{"restart"}, panelUnits...)...)
	if err := r.settled(ctx, version.Version); err != nil {
		return fmt.Errorf("%v; after rollback: %v", ierr, err)
	}
	return fmt.Errorf("release %s did not come up (%v): %s is back", ver, ierr, version.Version)
}

func (r *runner) updateDeb(ctx context.Context, tag, ver, work string) error {
	r.step("download")
	debs, err := r.fetchDebs(ctx, tag, ver, work)
	if err != nil {
		return err
	}
	r.step("install")
	r.say("install %s", tag)
	ierr := r.apt(ctx, append([]string{"install", "--allow-downgrades"}, debs...)...)
	if ierr == nil {
		ierr = r.settled(ctx, ver)
	}
	if ierr == nil {
		return nil
	}
	// The .deb files of the running version are in its own release.
	r.step("rollback")
	cur := version.Version
	r.say("roll back to %s: %v", cur, ierr)
	back := filepath.Join(work, "back")
	os.MkdirAll(back, 0o700)
	old, err := r.fetchDebs(ctx, "v"+cur, cur, back)
	if err != nil {
		return fmt.Errorf("%v; rollback failed: %v", ierr, err)
	}
	if err := r.apt(ctx, append([]string{"install", "--allow-downgrades"}, old...)...); err != nil {
		return fmt.Errorf("%v; rollback failed: %v", ierr, err)
	}
	if err := r.settled(ctx, cur); err != nil {
		return fmt.Errorf("%v; after rollback: %v", ierr, err)
	}
	return fmt.Errorf("release %s did not come up (%v): %s is back", ver, ierr, cur)
}

func (r *runner) fetchDebs(ctx context.Context, tag, ver, dir string) ([]string, error) {
	arch := runtime.GOARCH
	node, err := r.fetchChecked(ctx, tag, fmt.Sprintf("loft-node-%s-SHA256SUMS.txt", ver), fmt.Sprintf("loft-node_%s_%s.deb", ver, arch), dir)
	if err != nil {
		return nil, err
	}
	panel, err := r.fetchChecked(ctx, tag, fmt.Sprintf("loft-panel-%s-SHA256SUMS.txt", ver), fmt.Sprintf("loft-panel_%s_%s.deb", ver, arch), dir)
	if err != nil {
		return nil, err
	}
	return []string{node, panel}, nil
}

// binaries are the files a tarball install replaces.
func (r *runner) binaries() []string {
	exe := r.o.Exe
	if exe == "" {
		exe, _ = os.Executable()
	}
	d := filepath.Dir(exe)
	var out []string
	for _, b := range []string{filepath.Join(d, "loft-panel"), filepath.Join(d, "loft-panel-adm"), r.o.NodeBin} {
		if _, err := os.Stat(b); err == nil {
			out = append(out, b)
		}
	}
	return out
}

// settled waits for the services and checks the panel reports ver. A unit
// may still be activating (its restart delay): it gets up to 30 seconds. A
// crashing Type=simple unit looks active for a moment, so all of them must
// be active in two checks in a row.
func (r *runner) settled(ctx context.Context, ver string) error {
	deadline := time.Now().Add(3*r.o.Settle + 30*time.Second)
	for good := 0; good < 2; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.o.Settle):
		}
		bad, st := "", ""
		for _, u := range panelUnits {
			if st, _ = r.o.Systemctl(ctx, "is-active", u); strings.TrimSpace(st) != "active" {
				bad = u
				break
			}
		}
		if bad == "" {
			good++
			continue
		}
		good = 0
		if time.Now().After(deadline) {
			r.cmd(ctx, "journalctl", "-u", bad, "-n", "20", "--no-pager", "-o", "cat")
			return fmt.Errorf("%s is %s", bad, firstNonEmpty(strings.TrimSpace(st), "not active"))
		}
	}
	exe := r.o.Exe
	if exe == "" {
		exe, _ = os.Executable()
	}
	got, _ := r.o.exec(ctx, filepath.Join(filepath.Dir(exe), "loft-panel"), "version")
	if !strings.Contains(got, " "+ver) {
		return fmt.Errorf("loft-panel reports %q, not %s", strings.TrimSpace(got), ver)
	}
	r.say("loft-panel %s is up", ver)
	return nil
}

// fetchChecked downloads sums and asset from release tag and returns the
// asset's path once its SHA-256 matches.
func (r *runner) fetchChecked(ctx context.Context, tag, sums, asset, dir string) (string, error) {
	base := firstNonEmpty(r.o.ReleaseBase, Releases) + "/" + tag + "/"
	sp := filepath.Join(dir, sums)
	if err := r.download(ctx, base+sums, sp); err != nil {
		return "", err
	}
	want, err := sumFor(sp, asset)
	if err != nil {
		return "", err
	}
	ap := filepath.Join(dir, asset)
	if err := r.download(ctx, base+asset, ap); err != nil {
		return "", err
	}
	got, err := fileSum(ap)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf("%s: SHA-256 %s does not match %s from %s", asset, got, want, sums)
	}
	r.say("%s: SHA-256 matches %s", asset, sums)
	return ap, nil
}

func (r *runner) download(ctx context.Context, url, dst string) error {
	r.say("download %s", url)
	c := r.o.HTTP
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "loft-panel/"+version.Version)
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", path.Base(url), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", path.Base(url), resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxAsset+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxAsset {
		err = fmt.Errorf("larger than %d MiB", maxAsset>>20)
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", path.Base(url), err)
	}
	return nil
}

// sumFor finds asset in a sha256sum listing; names may carry a directory.
func sumFor(sums, asset string) (string, error) {
	f, err := os.Open(sums)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		h, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		name = strings.TrimLeft(strings.TrimSpace(name), "*")
		if ok && path.Base(name) == asset && len(h) == 64 {
			return strings.ToLower(h), nil
		}
	}
	return "", fmt.Errorf("%s is not listed in %s", asset, filepath.Base(sums))
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// Write beside and rename: a running binary is replaced, not truncated.
	return storeWrite(dst, b, mode)
}
