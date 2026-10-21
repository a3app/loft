package web

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeS3 keeps objects in memory and insists on a signature with the region
// it names in its first refusal, as real ones do.
type fakeS3 struct {
	mu   sync.Mutex
	objs map[string][]byte
	auth []string
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := r.Header.Get("Authorization")
	f.auth = append(f.auth, a)
	if !strings.Contains(a, "/eu-west-9/s3/aws4_request") {
		w.WriteHeader(400)
		io.WriteString(w, `<Error><Code>AuthorizationHeaderMalformed</Code><Message>the region 'us-east-1' is wrong; expecting 'eu-west-9'</Message></Error>`)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/bkt/")
	switch {
	case r.Method == "PUT":
		b, _ := io.ReadAll(r.Body)
		f.objs[key] = b
	case r.Method == "DELETE":
		delete(f.objs, key)
	case r.Method == "GET" && r.URL.Query().Get("list-type") == "2":
		var keys []string
		for k := range f.objs {
			if strings.HasPrefix(k, r.URL.Query().Get("prefix")) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		io.WriteString(w, "<ListBucketResult>")
		for _, k := range keys {
			io.WriteString(w, "<Contents><Key>"+k+"</Key><Size>1</Size><LastModified>2026-10-20T10:00:00Z</LastModified></Contents>")
		}
		io.WriteString(w, "<IsTruncated>false</IsTruncated></ListBucketResult>")
	case r.Method == "GET":
		b, okk := f.objs[key]
		if !okk {
			w.WriteHeader(404)
			return
		}
		w.Write(b)
	}
}

func TestS3Store(t *testing.T) {
	f := &fakeS3{objs: map[string][]byte{}}
	ts := httptest.NewServer(f)
	defer ts.Close()
	st, err := cleanStore(backupStore{Kind: "s3", Endpoint: ts.URL, Bucket: "bkt/loft", AccessKey: "AK", SecretKey: "SK"})
	if err != nil || st.Bucket != "bkt" || st.Prefix != "loft" {
		t.Fatalf("%+v %v", st, err)
	}
	d, err := newS3(st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if d.region != "eu-west-9" {
		t.Fatalf("region not learnt: %s", d.region)
	}
	if _, left := f.objs["loft/.loft-probe"]; left {
		t.Fatal("the probe stays behind")
	}
	s := &Server{}
	s.SetBackupDir(t.TempDir())
	os.MkdirAll(s.backupPath("backup"), 0o700)
	for _, n := range []string{"loft-h-20261018-040000.lpbak", "loft-h-20261019-040000.lpbak", "loft-h-20261020-040000.lpbak"} {
		os.WriteFile(filepath.Join(s.backupPath("backup"), n), []byte("LOFTBAK1 "+n), 0o600)
		if err := s.carry(ctx, st, n, 2); err != nil {
			t.Fatal(err)
		}
	}
	l, _ := d.List(ctx)
	if len(l) != 2 || l[0].Name != "loft-h-20261020-040000.lpbak" {
		t.Fatalf("keep 2, newest first: %+v", l)
	}
	rc, err := d.Get(ctx, l[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	if string(b) != "LOFTBAK1 loft-h-20261020-040000.lpbak" {
		t.Fatalf("got %q", b)
	}
	for _, a := range f.auth {
		if strings.Contains(a, "SK") {
			t.Fatal("the secret key on the wire")
		}
	}
}

func TestStoreChecks(t *testing.T) {
	for _, bad := range []backupStore{
		{Kind: "s3", Endpoint: "https://s3.example.net", Bucket: "", AccessKey: "a"},
		{Kind: "s3", Endpoint: "http://169.254.169.254", Bucket: "bkt", AccessKey: "a", SecretKey: "s"},
		{Kind: "s3", Endpoint: "https://x@s3.example.net", Bucket: "bkt", AccessKey: "a", SecretKey: "s"},
		{Kind: "s3", Endpoint: "ftp://s3.example.net", Bucket: "bkt", AccessKey: "a", SecretKey: "s"},
		{Kind: "s3", Endpoint: "https://s3.example.net", Bucket: "bk", AccessKey: "a", SecretKey: "s"},
		{Kind: "sftp", Host: "h"},
		{Kind: "dir", Dir: "relative"},
		{Kind: "ftp"},
	} {
		if _, err := cleanStore(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if !covers("203.0.113.0/24", "203.0.113.9") || covers("203.0.113.0/24", "198.51.100.1") || !covers("198.51.100.1", "198.51.100.1") {
		t.Fatal("covers")
	}
	if s3DialIP(net.ParseIP("169.254.169.254")) || s3DialIP(net.ParseIP("::ffff:169.254.169.254")) || s3DialIP(net.IPv4zero) {
		t.Fatal("link-local and unspecified")
	}
	if !s3DialIP(net.IPv4(127, 0, 0, 1)) || !s3DialIP(net.IPv4(10, 0, 0, 5)) || !s3DialIP(net.IPv4(203, 0, 113, 10)) {
		t.Fatal("loopback, LAN and public")
	}
}

func TestCopyIn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "backup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret")
	if err := os.WriteFile(secret, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := "loft-h-20261021-040000.lpbak"
	want, err := filepath.Abs(filepath.Join(dir, good))
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Clean(want)
	got, err := copyIn(dir, good)
	if err != nil || got != want {
		t.Fatalf("copyIn(%q) = %q %v, want %q", good, got, err, want)
	}
	before := "loft-h-20261021-040000-before-restore.lpbak"
	if _, err := copyIn(dir, before); err != nil {
		t.Fatalf("before-restore: %v", err)
	}
	part, err := fileIn(dir, "."+good+".part")
	if err != nil || filepath.Base(part) != "."+good+".part" {
		t.Fatalf("part: %q %v", part, err)
	}
	for _, name := range []string{
		"",
		"..",
		"../evil",
		"/etc/passwd",
		"foo/../../outside",
		"upload.lpbak",
		"loft-h-20261021-040000.lpbak/../x",
		"loft-..-20261021-040000.lpbak",
		"loft-h-20261021-040000.lpbak\x00",
	} {
		if _, err := copyIn(dir, name); err == nil {
			t.Fatalf("copyIn(%q) accepted", name)
		}
	}
	if _, err := fileIn(dir, "../."+good+".part"); err == nil {
		t.Fatal("fileIn accepted a parent path")
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "keep" {
		t.Fatalf("secret mutated: %s %v", b, err)
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
		case ".", "backup", "secret":
			return nil
		}
		t.Fatalf("extra path %q", rel)
		return nil
	})
}
