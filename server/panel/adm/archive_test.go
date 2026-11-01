package adm

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDestIn(t *testing.T) {
	dir := t.TempDir()
	root, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(root)
	ok := []struct{ name, want string }{
		{"foo", filepath.Join(root, "foo")},
		{"foo/bar", filepath.Join(root, "foo", "bar")},
		{"foo/./bar", filepath.Join(root, "foo", "bar")},
		{"notes..txt", filepath.Join(root, "notes..txt")},
		{".", root},
		{"foo/", filepath.Join(root, "foo")},
	}
	for _, c := range ok {
		got, err := destIn(dir, c.name)
		if err != nil || got != c.want {
			t.Fatalf("destIn(%q) = %q %v, want %q", c.name, got, err, c.want)
		}
	}
	for _, name := range []string{
		"",
		"..",
		"../evil",
		"/etc/passwd",
		"foo/../../outside",
		"foo/../bar",
		"foo/bar/..",
		"foo/\x00bar",
	} {
		if _, err := destIn(dir, name); err == nil {
			t.Fatalf("destIn(%q) accepted", name)
		}
	}
}

func TestUntar(t *testing.T) {
	d := t.TempDir()
	x := filepath.Join(d, "x")
	if err := os.MkdirAll(x, 0o700); err != nil {
		t.Fatal(err)
	}

	good := filepath.Join(d, "ok.tgz")
	writeTGZ(t, good,
		tarEnt{name: "dir/", typeflag: tar.TypeDir, mode: 0o755},
		tarEnt{name: "dir/ok.txt", body: "hi"},
		tarEnt{name: "notes..txt", body: "dots"},
	)
	if err := untar(good, x); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(x, "dir", "ok.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("ok.txt: %s %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(x, "notes..txt")); err != nil || string(b) != "dots" {
		t.Fatalf("notes..txt: %s %v", b, err)
	}
	assertInside(t, d, x, good)

	secret := filepath.Join(d, "secret")
	if err := os.WriteFile(secret, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ents := range [][]tarEnt{
		{{name: "../evil", body: "x"}},
		{{name: "/etc/passwd", body: "x"}},
		{{name: "foo/../../outside", body: "x"}},
		{{name: "foo/../bar", body: "x"}},
		{{name: "l", typeflag: tar.TypeSymlink, link: secret}},
		{{name: "h", typeflag: tar.TypeLink, link: "dir/ok.txt"}},
		{{name: ".", body: "x"}},
		{{name: "dir/ok.txt", body: "second"}},
	} {
		p := filepath.Join(d, "bad.tgz")
		writeTGZ(t, p, ents...)
		if err := untar(p, x); err == nil {
			t.Fatalf("accepted %+v", ents[0])
		}
		if b, err := os.ReadFile(secret); err != nil || string(b) != "keep" {
			t.Fatalf("secret mutated: %s %v", b, err)
		}
		assertInside(t, d, x, good, p, secret)
	}
}

func TestUnzipFlat(t *testing.T) {
	d := t.TempDir()
	x := filepath.Join(d, "x")
	good := filepath.Join(d, "ok.zip")
	writeZip(t, good, map[string]string{
		"bin/xray":  "exe",
		"geoip.dat": "geo",
	})
	if err := unzipFlat(good, x); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(x, "xray")); err != nil || string(b) != "exe" {
		t.Fatalf("xray: %s %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(x, "geoip.dat")); err != nil || string(b) != "geo" {
		t.Fatalf("geoip: %s %v", b, err)
	}
	assertInside(t, d, x, good)

	flat := filepath.Join(d, "flat.zip")
	writeZip(t, flat, map[string]string{"../evil": "x", "/etc/passwd": "y"})
	if err := unzipFlat(flat, x); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(x, "evil")); err != nil || string(b) != "x" {
		t.Fatalf("flattened ../evil: %s %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(x, "passwd")); err != nil || string(b) != "y" {
		t.Fatalf("flattened /etc/passwd: %s %v", b, err)
	}
	assertInside(t, d, x, good, flat)

	secret := filepath.Join(d, "secret")
	if err := os.WriteFile(secret, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, "bad.zip")
	writeZip(t, p, map[string]string{"..": "x"})
	if err := unzipFlat(p, x); err == nil {
		t.Fatal("accepted ..")
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "keep" {
		t.Fatalf("secret mutated: %s %v", b, err)
	}
	assertInside(t, d, x, good, flat, p, secret)
}

type tarEnt struct {
	name     string
	body     string
	typeflag byte
	link     string
	mode     int64
}

func writeTGZ(t *testing.T, path string, ents ...tarEnt) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	tw := tar.NewWriter(zw)
	for _, e := range ents {
		h := &tar.Header{
			Name:     e.name,
			Size:     int64(len(e.body)),
			Mode:     e.mode,
			Typeflag: e.typeflag,
			Linkname: e.link,
		}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, c := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertInside(t *testing.T, root, dest string, extra ...string) {
	t.Helper()
	allow := map[string]bool{filepath.Clean(dest): true}
	for _, e := range extra {
		allow[filepath.Clean(e)] = true
	}
	err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		c := filepath.Clean(p)
		if allow[c] {
			return nil
		}
		for a := range allow {
			if strings.HasPrefix(c, a+string(os.PathSeparator)) {
				return nil
			}
		}
		t.Errorf("wrote %s", p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
