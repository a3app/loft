package adm

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/a3app/loft/server/panel/catalog"
)

func (r *runner) binURL(p catalog.Package, asset string) string {
	base := firstNonEmpty(r.o.BinBase, "https://github.com/"+p.Bin.Repo+"/releases/download")
	return strings.TrimRight(base, "/") + "/" + p.Bin.Tag + "/" + asset
}

// installBin downloads the pinned GitHub tarball, checks SHA-256 against the
// catalog (not a checksum file), and copies the named binary into place.
func (r *runner) installBin(ctx context.Context, p catalog.Package) error {
	if p.Bin == nil {
		return fmt.Errorf("package %q has no binary", p.Key)
	}
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
