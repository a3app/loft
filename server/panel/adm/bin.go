package adm

// This file installs catalog packages that ship as a single prebuilt binary
// (for example mtproto) instead of a Debian package. The catalog
// (server/panel/catalog) pins the GitHub release tag, the asset name and its
// SHA-256 per CPU architecture; installBin is called from runner.install in
// runjob.go while a package job runs.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/a3app/loft/server/panel/catalog"
)

// binURL is the download address of a release asset. BinBase overrides the
// GitHub releases address (tests and mirrors use it).
func (r *runner) binURL(p catalog.Package, asset string) string {
	base := firstNonEmpty(r.o.BinBase, "https://github.com/"+p.Bin.Repo+"/releases/download")
	return strings.TrimRight(base, "/") + "/" + p.Bin.Tag + "/" + asset
}

// installBin downloads the pinned GitHub tarball, checks SHA-256 against the
// catalog (not a checksum file), and copies the named binary into place.
// The work happens in a scratch directory under the job state dir that is
// removed afterwards. It also writes a "version" file into the package's
// first data directory so the panel can show what is installed.
func (r *runner) installBin(ctx context.Context, p catalog.Package) error {
	if p.Bin == nil {
		return fmt.Errorf("package %q has no binary", p.Key)
	}
	// 1. Pick the asset for this CPU and the checksum it must match.
	arch := r.o.arch()
	asset := fmt.Sprintf(p.Bin.Asset, arch)
	sum, ok := p.Bin.SHA256[arch]
	if s, ok2 := r.o.BinSHA256[asset]; ok2 {
		sum, ok = s, true
	}
	if !ok {
		return fmt.Errorf("package %q has no build for %s", p.Key, arch)
	}
	work := filepath.Join(r.o.stateDir(), "bin-"+p.Key)
	os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(work)

	// 2. Download and verify the tarball before anything is unpacked.
	dst := filepath.Join(work, asset)
	r.step("download:" + p.Key)
	if err := r.download(ctx, r.binURL(p, asset), dst); err != nil {
		return err
	}
	got, err := fileSum(dst)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, sum) {
		return fmt.Errorf("%s: SHA-256 %s does not match the catalog", asset, got)
	}
	r.say("%s: SHA-256 matches the catalog", asset)

	// 3. Unpack and copy the one binary the catalog names into place.
	xdir := filepath.Join(work, "x")
	if err := untar(dst, xdir); err != nil {
		return fmt.Errorf("%s: %w", asset, err)
	}
	src, err := findNamed(xdir, p.Bin.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path(p.Bin.Path)), 0o755); err != nil {
		return err
	}
	if err := copyFile(src, r.path(p.Bin.Path), 0o755); err != nil {
		return err
	}
	r.say("installed %s", p.Bin.Path)

	// 4. Package-specific setup and the version marker.
	if p.Key == "mtproto" {
		if err := r.ensureMTProto(); err != nil {
			return err
		}
	}
	if len(p.DataDirs) > 0 {
		if err := os.MkdirAll(r.path(p.DataDirs[0]), 0o755); err != nil {
			return err
		}
		ver := strings.TrimPrefix(p.Bin.Tag, "v") + "\n"
		if err := storeWrite(r.path(filepath.Join(p.DataDirs[0], "version")), []byte(ver), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// findNamed returns the first regular file called name anywhere under dir.
// Release tarballs put the binary at different depths, so it is searched for.
func findNamed(dir, name string) (string, error) {
	var found string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if d.Name() == name {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("archive has no file named %s", name)
	}
	return found, nil
}
