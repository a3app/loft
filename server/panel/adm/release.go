package adm

// This file installs and removes "release" packages: catalog entries (see
// server/panel/catalog) that come as a zip from a project's GitHub releases
// rather than from apt. It runs inside a package job (runjob.go), so its
// progress goes to job.log. bin.go handles the similar "bin" method, which
// uses tarballs with hashes pinned in the catalog instead.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/catalog"
)

// Release packages: the project's newest GitHub release for this
// architecture, checked against the SHA2-256 line of ASSET.dgst.

// latestTag asks the GitHub API for the newest release tag of repo (for
// example "owner/name"). Tags that do not look like a version are refused.
func (r *runner) latestTag(ctx context.Context, repo string) (string, error) {
	base := firstNonEmpty(r.o.ReleaseAPI, "https://api.github.com/repos")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	c := r.o.HTTP
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered HTTP %d for %s", resp.StatusCode, repo)
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil || !tagRe.MatchString(rel.Tag) {
		return "", fmt.Errorf("GitHub: no release tag for %s", repo)
	}
	return rel.Tag, nil
}

// installRelease installs or upgrades a release package. Steps:
//  1. find the newest tag and the asset for this CPU;
//  2. download ASSET.dgst and the asset into a private work directory and
//     check the asset's SHA-256 against the SHA2-256 line of the .dgst;
//  3. unpack it beside the package directory (DIR.new), check the main
//     binary is there, then swap it in with a rename, so a failed unpack
//     never leaves a half-written install behind;
//  4. write the package's extra files from the catalog and enable the
//     service, restarting it only if it already runs (it starts on its
//     own once it has a config).
func (r *runner) installRelease(ctx context.Context, p catalog.Package) error {
	rel := p.Release
	asset := rel.Assets[runtime.GOARCH]
	if asset == "" {
		return fmt.Errorf("no %s build for %s", p.Key, runtime.GOARCH)
	}
	tag, err := r.latestTag(ctx, rel.Repo)
	if err != nil {
		return err
	}
	r.say("%s %s", rel.Repo, tag)
	work := filepath.Join(r.o.stateDir(), "release-"+p.Key)
	os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(work)
	base := firstNonEmpty(r.o.ReleaseDL, "https://github.com") + "/" + rel.Repo + "/releases/download/" + tag + "/"
	dg := filepath.Join(work, asset+".dgst")
	if err := r.download(ctx, base+asset+".dgst", dg); err != nil {
		return err
	}
	want := ""
	if b, err := os.ReadFile(dg); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "SHA2-256="); ok {
				want = strings.ToLower(strings.TrimSpace(v))
			}
		}
	}
	if len(want) != 64 {
		return fmt.Errorf("%s.dgst has no SHA2-256 line", asset)
	}
	zp := filepath.Join(work, asset)
	if err := r.download(ctx, base+asset, zp); err != nil {
		return err
	}
	if got, err := fileSum(zp); err != nil || got != want {
		return fmt.Errorf("%s: SHA-256 %s does not match %s.dgst", asset, got, asset)
	}
	r.say("%s: SHA-256 matches %s.dgst", asset, asset)
	dir := r.path(rel.Dir)
	tmp := dir + ".new"
	os.RemoveAll(tmp)
	if err := unzipFlat(zp, tmp); err != nil {
		return fmt.Errorf("%s: %v", asset, err)
	}
	if _, err := os.Stat(filepath.Join(tmp, rel.Bin)); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("%s has no %s", asset, rel.Bin)
	}
	os.Chmod(filepath.Join(tmp, rel.Bin), 0o755)
	os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	r.say("unpacked into %s", rel.Dir)
	for _, f := range sortedKeys(p.Files) {
		if err := os.MkdirAll(filepath.Dir(r.path(f)), 0o755); err != nil {
			return err
		}
		if err := storeWrite(r.path(f), []byte(p.Files[f]), 0o644); err != nil {
			return err
		}
	}
	if p.Unit != "" {
		r.cmd(ctx, "systemctl", "daemon-reload")
		r.cmd(ctx, "systemctl", "enable", p.Unit)
		// It starts once there is a config (ConditionPathExists); restart picks the new binary.
		r.cmd(ctx, "systemctl", "try-restart", p.Unit)
	}
	return nil
}

// removeRelease stops and disables the package's service, deletes its extra
// files and the unpacked release directory.
func (r *runner) removeRelease(ctx context.Context, p catalog.Package) error {
	if p.Unit != "" {
		r.cmd(ctx, "systemctl", "disable", "--now", p.Unit)
	}
	for _, f := range sortedKeys(p.Files) {
		os.Remove(r.path(f))
	}
	if p.Unit != "" {
		r.cmd(ctx, "systemctl", "daemon-reload")
	}
	return os.RemoveAll(r.path(p.Release.Dir))
}
