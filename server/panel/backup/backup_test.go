package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sealed(t *testing.T, phrase string, data []byte) []byte {
	k, err := NewKey(phrase)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := Seal(&buf, k, Header{Host: "vpn.example.net", Parts: []string{"node"}})
	if err != nil {
		t.Fatal(err)
	}
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

func TestSealOpen(t *testing.T) {
	phrase := NewPhrase()
	if len(phrase) != 35 || strings.Count(phrase, "-") != 5 || strings.ContainsAny(phrase, "01OIL") {
		t.Fatalf("phrase %q", phrase)
	}
	data := bytes.Repeat([]byte("loft "), 600000) // three chunks
	c := sealed(t, phrase, data)
	if bytes.Contains(c, []byte("loft loft")) {
		t.Fatal("plain text in the copy")
	}
	h, r, err := Open(bytes.NewReader(c), strings.ToLower(strings.ReplaceAll(phrase, "-", " ")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, data) || h.Host != "vpn.example.net" {
		t.Fatalf("round trip: %v", err)
	}
	if _, _, err := Open(bytes.NewReader(c), NewPhrase()); !errors.Is(err, ErrPhrase) {
		t.Fatalf("another phrase: %v", err)
	}
	// Cut off after a whole chunk, or with a flipped byte: reading fails.
	for _, bad := range [][]byte{c[:len(c)-200], c[:len(c)/2], append(append([]byte{}, c[:len(c)-10]...), c[len(c)-10]^1)} {
		_, r, err := Open(bytes.NewReader(bad), phrase)
		if err == nil {
			_, err = io.ReadAll(r)
		}
		if err == nil {
			t.Fatal("a damaged copy opened")
		}
	}
	// An empty copy still has its last chunk.
	_, r, _ = Open(bytes.NewReader(sealed(t, phrase, nil)), phrase)
	if b, err := io.ReadAll(r); err != nil || len(b) != 0 {
		t.Fatalf("empty: %v", err)
	}
}

func TestArchive(t *testing.T) {
	root := t.TempDir()
	put := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(s), 0o640)
	}
	put("/var/lib/loft/network.conf", "network main\n")
	put("/etc/loft/node.conf", "role controller\n")
	put("/var/lib/loft-panel/users.json", "{}")
	put("/var/lib/loft-panel/backup/old.lpbak", "not me")
	put("/var/vmail/example.net/a/Maildir/new/1", "letter")
	var buf bytes.Buffer
	m, err := Write(&buf, root, "h", []string{"node", "panel"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if strings.Contains(f.Path, "backup/old") || strings.HasPrefix(f.Path, "mail/") {
			t.Fatalf("%s should stay out", f.Path)
		}
	}
	dst := t.TempDir()
	if _, err := Extract(bytes.NewReader(buf.Bytes()), dst, []string{"node"}, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "node/etc/loft/node.conf")); string(b) != "role controller\n" {
		t.Fatal("node.conf not restored")
	}
	if _, err := os.Stat(filepath.Join(dst, "panel")); err == nil {
		t.Fatal("an unwanted part was unpacked")
	}
	if _, err := Extract(bytes.NewReader(buf.Bytes()), t.TempDir(), []string{"mail"}, nil); err == nil {
		t.Fatal("restoring a part the copy lacks")
	}
	if _, err := Extract(bytes.NewReader(buf.Bytes()[:buf.Len()/2]), t.TempDir(), []string{"node"}, nil); err == nil {
		t.Fatal("a cut archive restored")
	}
}

func TestDestIn(t *testing.T) {
	dir := t.TempDir()
	root, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(root)
	ok := []struct{ name, want string }{
		{"node/etc/loft/node.conf", filepath.Join(root, "node", "etc", "loft", "node.conf")},
		{"node/notes..txt", filepath.Join(root, "node", "notes..txt")},
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
		"node/foo/../../outside",
		"node/foo/../bar",
		"node/\x00bar",
	} {
		if _, err := destIn(dir, name); err == nil {
			t.Fatalf("destIn(%q) accepted", name)
		}
	}
}

func TestExtractZipSlip(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "stage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret")
	if err := os.WriteFile(secret, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ents := range [][]tarEnt{
		{{name: "../evil", body: "x"}},
		{{name: "/etc/passwd", body: "x"}},
		{{name: "node/foo/../../outside", body: "x"}},
		{{name: "node/etc/loft/x", typeflag: tar.TypeSymlink, link: "../../../../secret"}},
		{{name: "node/subdir/parent", typeflag: tar.TypeSymlink, link: ".."}},
		{
			{name: "node/subdir/parent", typeflag: tar.TypeSymlink, link: ".."},
			{name: "node/escape", typeflag: tar.TypeSymlink, link: "subdir/parent/.."},
		},
		{
			{name: "node/a", typeflag: tar.TypeSymlink, link: root},
			{name: "node/a/secret", body: "pwn"},
		},
		{{name: "l", typeflag: tar.TypeLink, link: "node/etc/loft/x"}},
	} {
		raw := writeTGZ(t, ents...)
		if _, err := Extract(bytes.NewReader(raw), dir, []string{"node"}, nil); err == nil {
			t.Fatalf("accepted %+v", ents[0])
		}
		if b, err := os.ReadFile(secret); err != nil || string(b) != "keep" {
			t.Fatalf("secret mutated: %s %v", b, err)
		}
	}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		switch rel {
		case ".", "stage", "secret":
			return nil
		}
		if strings.HasPrefix(rel, "stage"+string(os.PathSeparator)) {
			return nil
		}
		t.Fatalf("extra path %q", rel)
		return nil
	})
}

func TestExtractRelativeLink(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "etc", "loft")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("data"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(dir, "current")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := Write(&buf, root, "h", []string{"node"}); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if _, err := Extract(bytes.NewReader(buf.Bytes()), dst, []string{"node"}, nil); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dst, "node", "etc", "loft", "current")
	got, err := os.Readlink(link)
	if err != nil || got != "target.txt" {
		t.Fatalf("link %q %v", got, err)
	}
	b, err := os.ReadFile(link)
	if err != nil || string(b) != "data" {
		t.Fatalf("follow %s %v", b, err)
	}
}

func TestExtractAbsoluteLink(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "etc", "loft")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node.conf"), []byte("role controller\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "tz")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := Write(&buf, root, "h", []string{"node"}); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if _, err := Extract(bytes.NewReader(buf.Bytes()), dst, []string{"node"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(dst, "node", "etc", "loft", "tz"))
	if err != nil || got != target {
		t.Fatalf("link %q %v", got, err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
		t.Fatalf("target mutated: %s %v", b, err)
	}
}

type tarEnt struct {
	name     string
	body     string
	typeflag byte
	link     string
}

func writeTGZ(t *testing.T, ents ...tarEnt) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range ents {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body))}
		if e.typeflag != 0 {
			h.Typeflag = e.typeflag
			h.Linkname = e.link
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == 0 || h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
