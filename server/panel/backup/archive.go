package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Part is a piece of the server a copy may hold. The panel's own data and
// the node's always go together; mail is optional because it can be large.
type Part struct {
	Name  string
	Roots []string // absolute paths; missing ones are skipped
	Skip  []string // paths under Roots that stay out
}

// Parts are what a copy can hold, in restore order.
var Parts = []Part{
	{Name: "node", Roots: []string{"/var/lib/loft", "/etc/loft"}},
	{Name: "panel", Roots: []string{"/var/lib/loft-panel", "/etc/loft-panel", "/var/lib/loft-mtproto"},
		Skip: []string{"/var/lib/loft-panel/backup"}},
	{Name: "mail", Roots: []string{"/var/vmail", "/etc/opendkim/keys"}},
}

// PartByName finds a part.
func PartByName(n string) (Part, bool) {
	i := slices.IndexFunc(Parts, func(p Part) bool { return p.Name == n })
	if i < 0 {
		return Part{}, false
	}
	return Parts[i], true
}

// File is one entry of the manifest.
type File struct {
	Path   string `json:"path"` // part/absolute/path
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

// Manifest is the archive's last entry.
type Manifest struct {
	Created time.Time `json:"created"`
	Host    string    `json:"host"`
	Parts   []string  `json:"parts"`
	Files   []File    `json:"files"`
}

// Write archives parts (Root prefixes system paths, for tests) as tar.gz
// to w, the manifest last.
func Write(w io.Writer, root, host string, parts []string) (Manifest, error) {
	m := Manifest{Created: time.Now().UTC(), Host: host, Parts: parts, Files: []File{}}
	gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	for _, name := range parts {
		p, ok := PartByName(name)
		if !ok {
			return m, fmt.Errorf("unknown part %q", name)
		}
		for _, r := range p.Roots {
			if _, err := os.Lstat(filepath.Join(root, r)); err != nil {
				continue
			}
			err := filepath.WalkDir(filepath.Join(root, r), func(full string, d fs.DirEntry, err error) error {
				// Mail moves files from tmp/ to new/ and cur/ all the time:
				// a file gone since the directory was read is skipped.
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				if err != nil {
					return err
				}
				sys := "/" + strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(full, root)), "/")
				for _, s := range p.Skip {
					if sys == s || strings.HasPrefix(sys, s+"/") {
						if d.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}
				}
				info, err := d.Info()
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				if err != nil {
					return err
				}
				if info.Mode()&(fs.ModeSocket|fs.ModeNamedPipe|fs.ModeDevice) != 0 {
					return nil
				}
				link := ""
				if info.Mode()&fs.ModeSymlink != 0 {
					if link, err = os.Readlink(full); err != nil {
						return err
					}
				}
				h, err := tar.FileInfoHeader(info, link)
				if err != nil {
					return err
				}
				h.Name = name + sys
				if d.IsDir() {
					h.Name += "/"
				}
				if err := tw.WriteHeader(h); err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return nil
				}
				f, err := os.Open(full)
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				if err != nil {
					return err
				}
				defer f.Close()
				sum := sha256.New()
				n, err := io.Copy(io.MultiWriter(tw, sum), io.LimitReader(f, h.Size))
				if err != nil {
					return err
				}
				if n != h.Size { // the file shrank while being read
					return fmt.Errorf("%s changed while being copied", sys)
				}
				m.Files = append(m.Files, File{Path: h.Name, Size: n, SHA256: hex.EncodeToString(sum.Sum(nil))})
				return nil
			})
			if err != nil {
				return m, err
			}
		}
	}
	mb, _ := json.MarshalIndent(m, "", " ")
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(mb)), ModTime: m.Created, Typeflag: tar.TypeReg}); err != nil {
		return m, err
	}
	if _, err := tw.Write(mb); err != nil {
		return m, err
	}
	if err := tw.Close(); err != nil {
		return m, err
	}
	return m, gz.Close()
}

// Owner sets an extracted entry's owner by name (accounts get other
// numbers on another server), falling back to the numbers.
type Owner func(path string, h *tar.Header) error

// destIn maps an archive entry name onto a path inside dir.
// Empty, NUL, absolute, a ".." component, or a name that lexically
// leaves dir is rejected.
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
	if !inside(root, dst) {
		return "", fmt.Errorf("bad path %q in archive", name)
	}
	return dst, nil
}

func inside(root, p string) bool {
	p = filepath.Clean(p)
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if !filepath.IsLocal(rel) {
		return false
	}
	return strings.HasPrefix(p, root+string(os.PathSeparator))
}

// resolveExisting EvalSymlinks the longest existing prefix of p and
// appends the missing tail. EvalSymlinks(p) is tried first so a planted
// link already on the path is followed before the inside-root check.
func resolveExisting(p string) (string, error) {
	p = filepath.Clean(p)
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		return real, nil
	}
	tail := ""
	cur := p
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		name := filepath.Base(cur)
		if tail == "" {
			tail = name
		} else {
			tail = filepath.Join(name, tail)
		}
		if real, err = filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(real, tail), nil
		}
		cur = parent
	}
	if !filepath.IsAbs(p) {
		return filepath.Abs(p)
	}
	return p, nil
}

// evalInside resolves p through any already-extracted links and checks
// the result stays in root.
func evalInside(root, p string) error {
	realRoot, err := resolveExisting(root)
	if err != nil {
		return err
	}
	real, err := resolveExisting(p)
	if err != nil {
		return err
	}
	if !inside(realRoot, real) {
		return fmt.Errorf("bad path %q", p)
	}
	return nil
}

func hasDotDot(p string) bool {
	for _, c := range strings.Split(filepath.ToSlash(p), "/") {
		if c == ".." {
			return true
		}
	}
	return false
}

// mkdirAllNoFollow creates dir and its parents as real directories.
// A symlink anywhere on the path is refused so a planted link cannot
// smuggle later files onto the live system.
func mkdirAllNoFollow(dir string, perm os.FileMode) error {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil
	}
	var missing []string
	p := dir
	for {
		fi, err := os.Lstat(p)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in path %q", p)
			}
			if !fi.IsDir() {
				return fmt.Errorf("not a directory: %q", p)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], perm); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		fi, err := os.Lstat(missing[i])
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("symlink in path %q", missing[i])
		}
	}
	return nil
}

func createFile(dst string) (*os.File, error) {
	return os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
}

// symlinkIn creates a link at dst. A ".." component is refused so a pair
// like subdir/parent -> .. then escape -> subdir/parent/.. cannot leave
// dir. Absolute targets are stored as-is (a restored /etc/loft may point
// at /etc/letsencrypt) and never followed during extract.
func symlinkIn(root, dst, link string) error {
	if link == "" || strings.IndexByte(link, 0) >= 0 || hasDotDot(link) {
		return fmt.Errorf("bad link %q", link)
	}
	parent := filepath.Dir(dst)
	if err := evalInside(root, parent); err != nil {
		return err
	}
	target := filepath.Join(parent, link)
	if err := evalInside(root, target); err != nil {
		if !filepath.IsAbs(link) {
			return fmt.Errorf("bad link %q", link)
		}
		// Absolute and outside dir: EvalSymlinks already ran in evalInside.
	}
	_ = os.Remove(dst)
	return os.Symlink(link, dst)
}

// Extract unpacks the wanted parts into dir (dir/part/absolute/path) and
// checks every file against the manifest: a missing, extra or changed file
// fails the whole restore before anything on the server is touched.
func Extract(r io.Reader, dir string, want []string, own Owner) (Manifest, error) {
	var m Manifest
	gz, err := gzip.NewReader(r)
	if err != nil {
		return m, errors.New("the copy's archive is damaged")
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return m, err
	}
	root = filepath.Clean(root)
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("the copy's archive is damaged: %v", err)
		}
		if h.Name == "manifest.json" {
			if err := json.NewDecoder(io.LimitReader(tr, 64<<20)).Decode(&m); err != nil {
				return m, errors.New("the copy's manifest is damaged")
			}
			continue
		}
		dst, err := destIn(dir, h.Name)
		if err != nil {
			return m, err
		}
		part, _, _ := strings.Cut(strings.TrimSuffix(h.Name, "/"), "/")
		if part == "" || !slices.Contains(want, part) {
			continue
		}
		if err := mkdirAllNoFollow(filepath.Dir(dst), 0o700); err != nil {
			return m, err
		}
		if err := evalInside(root, filepath.Dir(dst)); err != nil {
			return m, err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := mkdirAllNoFollow(dst, 0o700); err != nil {
				return m, err
			}
		case tar.TypeSymlink:
			if err := symlinkIn(root, dst, h.Linkname); err != nil {
				return m, err
			}
		case tar.TypeReg, tar.TypeRegA:
			f, err := createFile(dst)
			if err != nil {
				return m, err
			}
			sum := sha256.New()
			_, err = io.Copy(io.MultiWriter(f, sum), tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return m, fmt.Errorf("the copy's archive is damaged: %v", err)
			}
			got[strings.TrimSuffix(h.Name, "/")] = hex.EncodeToString(sum.Sum(nil))
		default:
			return m, fmt.Errorf("the copy holds an unexpected %s entry", h.Name)
		}
		if h.Typeflag != tar.TypeSymlink {
			os.Chmod(dst, h.FileInfo().Mode().Perm())
			if own != nil {
				if err := own(dst, h); err != nil {
					return m, err
				}
			}
			os.Chtimes(dst, h.ModTime, h.ModTime)
		}
	}
	if m.Files == nil {
		return m, errors.New("the copy has no manifest: it was cut off")
	}
	for _, f := range m.Files {
		part, _, _ := strings.Cut(f.Path, "/")
		if !slices.Contains(want, part) {
			continue
		}
		if got[f.Path] != f.SHA256 {
			return m, fmt.Errorf("the copy is damaged: %s does not match", strings.TrimPrefix(f.Path, part))
		}
		delete(got, f.Path)
	}
	for p := range got {
		return m, fmt.Errorf("the copy holds %s, not in its manifest", p)
	}
	for _, w := range want {
		if !slices.Contains(m.Parts, w) {
			return m, fmt.Errorf("the copy has no %s part", w)
		}
	}
	return m, nil
}
