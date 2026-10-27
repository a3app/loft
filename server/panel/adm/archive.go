package adm

// This file unpacks downloaded archives safely. The package installer
// (bin.go) unpacks GitHub release tarballs with untar, and the release
// installer (release.go) unpacks zip assets with unzipFlat. Because adm runs
// as root, every entry name is checked so a hostile archive cannot write
// outside the target directory, follow a symlink, or fill the disk.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// maxArchiveFiles caps entries in one tarball or zip.
const maxArchiveFiles = 4096

// destIn maps an archive entry name onto a path inside dir.
// Empty, NUL, absolute, a ".." component, or a name that lexically
// leaves dir is rejected. The dest directory itself is allowed (".").
func destIn(dir, name string) (string, error) {
	if name == "" || strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("bad path %q in archive", name)
	}
	local := filepath.FromSlash(name)
	for _, p := range strings.Split(filepath.ToSlash(local), "/") {
		if p == ".." {
			return "", fmt.Errorf("bad path %q in archive", name)
		}
	}
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("bad path %q in archive", name)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	dst := filepath.Join(root, local)
	rel, err := filepath.Rel(root, dst)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("bad path %q in archive", name)
	}
	if dst != root && !strings.HasPrefix(dst, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("bad path %q in archive", name)
	}
	return dst, nil
}

// createFile creates a new file for an archive entry. O_EXCL refuses to
// reuse an existing path and O_NOFOLLOW refuses to write through a symlink,
// so a crafted archive cannot redirect the write elsewhere.
func createFile(dst string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
}

// untar extracts regular files and directories of a .tar.gz into dir.
// Symlinks, devices and other entry types are refused. File modes keep at
// most the 0o755 bits (and always owner read/write), and each file is cut
// at maxAsset bytes.
func untar(tgz, dir string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	root = filepath.Clean(root)
	tr := tar.NewReader(zr)
	n := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		n++
		if n > maxArchiveFiles {
			return fmt.Errorf("too many files in archive")
		}
		dst, err := destIn(dir, h.Name)
		if err != nil {
			return err
		}
		// Only plain directories and regular files are allowed; the
		// target directory itself cannot be replaced by a file.
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if dst == root {
				return fmt.Errorf("bad path %q in archive", h.Name)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			w, err := createFile(dst, os.FileMode(h.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, io.LimitReader(tr, maxAsset))
			if cerr := w.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tar type %q for %q", h.Typeflag, h.Name)
		}
	}
}

// unzipFlat extracts regular files of a zip into dir, using only the
// final path element of each name. Directory entries are skipped, so the
// result is one flat directory. Each file is cut at maxAsset bytes.
func unzipFlat(zp, dir string) error {
	z, err := zip.OpenReader(zp)
	if err != nil {
		return err
	}
	defer z.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	n := 0
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		n++
		if n > maxArchiveFiles {
			return fmt.Errorf("too many files in archive")
		}
		base := path.Base(filepath.ToSlash(f.Name))
		if base == "." || base == ".." {
			return fmt.Errorf("bad path %q in archive", f.Name)
		}
		dst, err := destIn(dir, base)
		if err != nil {
			return fmt.Errorf("bad path %q in archive", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := createFile(dst, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, io.LimitReader(rc, maxAsset))
		rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
