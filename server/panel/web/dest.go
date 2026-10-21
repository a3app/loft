package web

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/a3app/loft/server/panel/admproto"
)

// Stores for copies the panel carries away itself: S3 and SFTP. A directory
// on the server is the helper's (root writes it), not here.

type dest interface {
	Check(ctx context.Context) error // writes and removes a probe
	Put(ctx context.Context, name string, body io.Reader, size int64) error
	Get(ctx context.Context, name string) (io.ReadCloser, error)
	List(ctx context.Context) ([]admproto.BackupFile, error)
	Delete(ctx context.Context, name string) error
}

var copyRe = regexp.MustCompile(`^loft-[a-z0-9.-]{1,100}-\d{8}-\d{6}(-before-restore)?\.lpbak$`)

// copyIn maps a copy's name onto a path inside dir. The name must be a
// loft copy filename: one path component, matching copyRe. A name that
// lexically leaves dir is rejected.
func copyIn(dir, name string) (string, error) {
	if !copyRe.MatchString(name) {
		return "", errors.New("not a copy")
	}
	return fileIn(dir, name)
}

// fileIn maps name onto a path inside dir. name must be one path
// component: no separator, no "..", and the joined path must stay in dir.
func fileIn(dir, name string) (string, error) {
	if name == "" || strings.IndexByte(name, 0) >= 0 {
		return "", errors.New("bad name")
	}
	if strings.Contains(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "..") {
		return "", errors.New("bad name")
	}
	if !filepath.IsLocal(name) {
		return "", errors.New("bad name")
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	dst := filepath.Join(root, name)
	rel, err := filepath.Rel(root, dst)
	if err != nil || rel != name || !filepath.IsLocal(rel) {
		return "", errors.New("bad name")
	}
	if !strings.HasPrefix(dst, root+string(os.PathSeparator)) {
		return "", errors.New("bad name")
	}
	return dst, nil
}

/* ── S3 ── */

type s3dest struct {
	st     backupStore
	scheme string
	host   string
	region string
	path   bool // bucket in the path rather than the host
	cli    *http.Client
}

var (
	awsRegionRe = regexp.MustCompile(`^s3[.-]([a-z0-9-]+)\.amazonaws\.com$`)
	// Host of an S3 endpoint: DNS labels, a single name, or an IP.
	s3HostRe   = regexp.MustCompile(`^(?i)[0-9a-z.:\[\]-]{1,253}$`)
	s3BucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	s3PrefixRe = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
	s3RegionRe = regexp.MustCompile(`^[a-z0-9-]+$`)
)

func newS3(st backupStore) (*s3dest, error) {
	if st.AccessKey == "" || st.SecretKey == "" || st.Bucket == "" {
		return nil, errors.New("S3 needs an address, a bucket and both keys")
	}
	if !s3BucketRe.MatchString(st.Bucket) {
		return nil, errors.New("S3 needs a bucket and an access key")
	}
	if p := strings.Trim(st.Prefix, "/"); p != "" && (!s3PrefixRe.MatchString(p) || strings.Contains(p, "..")) {
		return nil, errors.New("S3 needs a bucket and an access key")
	}
	region := firstOf(st.Region, "us-east-1")
	if !s3RegionRe.MatchString(region) {
		return nil, fmt.Errorf("%q is not an https:// address", st.Endpoint)
	}
	u, err := url.Parse(st.Endpoint)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("%q is not an https:// address", st.Endpoint)
	}
	h := u.Hostname()
	if !s3HostRe.MatchString(h) {
		return nil, fmt.Errorf("%q is not an https:// address", st.Endpoint)
	}
	if ip := net.ParseIP(h); ip != nil && !s3DialIP(ip) {
		return nil, fmt.Errorf("%q is not a usable S3 address", st.Endpoint)
	}
	host := h
	if p := u.Port(); p != "" {
		if _, err := strconv.Atoi(p); err != nil {
			return nil, fmt.Errorf("%q is not an https:// address", st.Endpoint)
		}
		host = net.JoinHostPort(h, p)
	}
	d := &s3dest{st: st, scheme: u.Scheme, host: host, region: region, path: true,
		cli: &http.Client{
			Timeout:       6 * time.Hour,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport:     &http.Transport{Proxy: nil, DialContext: s3Dial},
		}}
	if m := awsRegionRe.FindStringSubmatch(h); m != nil {
		d.region, d.path = m[1], false // AWS: the bucket is a host name
	}
	return d, nil
}

// s3DialIP is a host the panel may open for an S3 store: public, private
// LAN and loopback (MinIO, tests). Link-local is refused so a store cannot
// be aimed at cloud metadata.
func s3DialIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return (ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate()) && !ip.IsLinkLocalUnicast() && !ip.IsMulticast() && !ip.IsUnspecified()
}

func s3Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var last error
	nd := net.Dialer{Timeout: 20 * time.Second}
	for _, a := range ips {
		if !s3DialIP(a.IP) {
			last = fmt.Errorf("not a usable S3 address")
			continue
		}
		c, err := nd.DialContext(ctx, "tcp", net.JoinHostPort(a.IP.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	if last == nil {
		last = errors.New("no address")
	}
	return nil, last
}

func (d *s3dest) key(name string) string {
	if p := strings.Trim(d.st.Prefix, "/"); p != "" {
		return p + "/" + name
	}
	return name
}

func (d *s3dest) reqURL(key string, q url.Values) url.URL {
	u := url.URL{Scheme: d.scheme, Host: d.host}
	var segs []string
	if d.path {
		segs = append(segs, d.st.Bucket)
	} else {
		h, p, err := net.SplitHostPort(d.host)
		if err != nil {
			u.Host = d.st.Bucket + "." + d.host
		} else {
			u.Host = net.JoinHostPort(d.st.Bucket+"."+h, p)
		}
	}
	if key != "" {
		segs = append(segs, strings.Split(key, "/")...)
	}
	if len(segs) == 0 {
		u.Path = "/"
	} else {
		u.Path = "/" + strings.Join(segs, "/")
	}
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	return u
}

// wrongRegion is the region an S3 server says it expects
// (AuthorizationHeaderMalformed names it).
var wrongRegion = regexp.MustCompile(`expecting '([a-z0-9-]+)'`)

func (d *s3dest) do(ctx context.Context, method, key string, q url.Values, body io.Reader, size int64) (*http.Response, error) {
	for try := 0; ; try++ {
		u := d.reqURL(key, q)
		req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
		if err != nil {
			return nil, err
		}
		req.Host = u.Host
		if size >= 0 {
			req.ContentLength = size
		}
		d.sign(req, q)
		resp, err := d.cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", u.Host, err)
		}
		if resp.StatusCode < 300 {
			return resp, nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if m := wrongRegion.FindSubmatch(raw); m != nil && try == 0 && (body == nil || size == 0) {
			d.region = string(m[1])
			continue
		}
		return nil, fmt.Errorf("%s answered %s: %s", u.Host, resp.Status, s3Reason(raw))
	}
}

func s3Reason(raw []byte) string {
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if xml.Unmarshal(raw, &e) == nil && e.Code != "" {
		return strings.TrimSuffix(e.Code+": "+e.Message, ": ")
	}
	if s := strings.TrimSpace(string(raw)); s != "" {
		return s
	}
	return "no reason given"
}

// sign is AWS Signature V4 with an unsigned payload: the copy is not read
// twice to hash it, and TLS protects it on the way.
func (d *s3dest) sign(req *http.Request, q url.Values) {
	now := time.Now().UTC()
	stamp, day := now.Format("20060102T150405Z"), now.Format("20060102")
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	names := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	vals := map[string]string{"host": req.Host, "x-amz-content-sha256": "UNSIGNED-PAYLOAD", "x-amz-date": stamp}
	var ch strings.Builder
	for _, n := range names {
		ch.WriteString(n + ":" + vals[n] + "\n")
	}
	query := ""
	if len(q) > 0 {
		query = strings.ReplaceAll(q.Encode(), "+", "%20")
	}
	canon := strings.Join([]string{req.Method, req.URL.EscapedPath(), query, ch.String(), strings.Join(names, ";"), "UNSIGNED-PAYLOAD"}, "\n")
	scope := day + "/" + d.region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canon))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	k := mac([]byte("AWS4"+d.st.SecretKey), day)
	k = mac(mac(mac(k, d.region), "s3"), "aws4_request")
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		d.st.AccessKey, scope, strings.Join(names, ";"), hex.EncodeToString(mac(k, toSign))))
}

func mac(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func (d *s3dest) Check(ctx context.Context) error {
	// A listing first: it carries no body, so a wrong region is learnt
	// from the answer and the probe below is signed right.
	resp, err := d.do(ctx, http.MethodGet, "", url.Values{"list-type": {"2"}, "max-keys": {"1"}}, nil, 0)
	if err != nil {
		return err
	}
	resp.Body.Close()
	probe := "loft-panel write probe\n"
	resp, err = d.do(ctx, http.MethodPut, d.key(".loft-probe"), nil, strings.NewReader(probe), int64(len(probe)))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp, err := d.do(ctx, http.MethodDelete, d.key(".loft-probe"), nil, nil, 0); err == nil {
		resp.Body.Close()
	}
	return nil
}

func (d *s3dest) Put(ctx context.Context, name string, body io.Reader, size int64) error {
	if size > 5<<30 {
		return errors.New("the copy is over 5 GB, the most one S3 upload takes")
	}
	// The body cannot be sent twice: learn the region with a listing first.
	if lr, err := d.do(ctx, http.MethodGet, "", url.Values{"list-type": {"2"}, "max-keys": {"1"}}, nil, 0); err == nil {
		lr.Body.Close()
	}
	resp, err := d.do(ctx, http.MethodPut, d.key(name), nil, body, size)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (d *s3dest) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	resp, err := d.do(ctx, http.MethodGet, d.key(name), nil, nil, 0)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (d *s3dest) Delete(ctx context.Context, name string) error {
	resp, err := d.do(ctx, http.MethodDelete, d.key(name), nil, nil, 0)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (d *s3dest) List(ctx context.Context) ([]admproto.BackupFile, error) {
	out := []admproto.BackupFile{}
	token := ""
	for {
		q := url.Values{"list-type": {"2"}, "max-keys": {"1000"}}
		if p := strings.Trim(d.st.Prefix, "/"); p != "" {
			q.Set("prefix", p+"/")
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := d.do(ctx, http.MethodGet, "", q, nil, 0)
		if err != nil {
			return nil, err
		}
		var res struct {
			Contents []struct {
				Key          string    `xml:"Key"`
				Size         int64     `xml:"Size"`
				LastModified time.Time `xml:"LastModified"`
			} `xml:"Contents"`
			Next      string `xml:"NextContinuationToken"`
			Truncated bool   `xml:"IsTruncated"`
		}
		err = xml.NewDecoder(resp.Body).Decode(&res)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, c := range res.Contents {
			if n := path.Base(c.Key); copyRe.MatchString(n) {
				out = append(out, admproto.BackupFile{Name: n, Size: c.Size, At: c.LastModified})
			}
		}
		if !res.Truncated || res.Next == "" {
			break
		}
		token = res.Next
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

/* ── SFTP ── */

// sftpKey is the panel's own SSH key for SFTP stores, made on first use;
// the other side puts its public half in authorized_keys. No passwords.
func (s *Server) sftpKey() (ssh.Signer, error) {
	p := s.backupPath("backup-ssh.key")
	if b, err := os.ReadFile(p); err == nil {
		return ssh.ParsePrivateKey(b)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	blk, err := ssh.MarshalPrivateKey(priv, "loft-panel backups")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, pem.EncodeToMemory(blk), 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

type sftpDest struct {
	st     backupStore
	signer ssh.Signer
	// pin records the server's host key the first time (Check); later a
	// different key is refused.
	pin func(fp string)
}

func (d *sftpDest) addr() string {
	h, p, err := net.SplitHostPort(d.st.Host)
	if err != nil {
		return net.JoinHostPort(d.st.Host, "22")
	}
	if _, err := strconv.Atoi(p); err != nil {
		return net.JoinHostPort(h, "22")
	}
	return d.st.Host
}

func (d *sftpDest) dial(ctx context.Context) (*ssh.Client, *sftp.Client, error) {
	cfg := &ssh.ClientConfig{User: d.st.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(d.signer)}, Timeout: 20 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(k)
			if d.st.HostKey == "" {
				if d.pin != nil {
					d.pin(fp)
				}
				return nil
			}
			if fp != d.st.HostKey {
				return fmt.Errorf("the server's host key changed (%s, was %s): if it was reinstalled, add the store again", fp, d.st.HostKey)
			}
			return nil
		}}
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, "tcp", d.addr())
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %v", d.addr(), err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, d.addr(), cfg)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("%s did not let the key in: %v", d.addr(), err)
	}
	cli := ssh.NewClient(c, chans, reqs)
	fs, err := sftp.NewClient(cli)
	if err != nil {
		cli.Close()
		return nil, nil, fmt.Errorf("%s has no SFTP: %v", d.addr(), err)
	}
	return cli, fs, nil
}

func (d *sftpDest) dir() string { return firstOf(strings.TrimRight(d.st.Dir, "/"), ".") }

func (d *sftpDest) Check(ctx context.Context) error {
	cli, fs, err := d.dial(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()
	defer fs.Close()
	if err := fs.MkdirAll(d.dir()); err != nil {
		return fmt.Errorf("%s: %v", d.dir(), err)
	}
	p := path.Join(d.dir(), ".loft-probe")
	f, err := fs.Create(p)
	if err != nil {
		return fmt.Errorf("%s is not writable: %v", d.dir(), err)
	}
	f.Write([]byte("loft-panel write probe\n"))
	f.Close()
	fs.Remove(p)
	return nil
}

func (d *sftpDest) Put(ctx context.Context, name string, body io.Reader, _ int64) error {
	cli, fs, err := d.dial(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()
	defer fs.Close()
	if err := fs.MkdirAll(d.dir()); err != nil {
		return err
	}
	final := path.Join(d.dir(), name)
	tmp := final + ".part"
	f, err := fs.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.ReadFrom(body); err != nil {
		f.Close()
		fs.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		fs.Remove(tmp)
		return err
	}
	fs.Remove(final)
	return fs.Rename(tmp, final)
}

type sftpFile struct {
	*sftp.File
	fs  *sftp.Client
	cli *ssh.Client
}

func (f *sftpFile) Close() error {
	err := f.File.Close()
	f.fs.Close()
	f.cli.Close()
	return err
}

func (d *sftpDest) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	cli, fs, err := d.dial(ctx)
	if err != nil {
		return nil, err
	}
	f, err := fs.Open(path.Join(d.dir(), name))
	if err != nil {
		fs.Close()
		cli.Close()
		return nil, err
	}
	return &sftpFile{File: f, fs: fs, cli: cli}, nil
}

func (d *sftpDest) List(ctx context.Context) ([]admproto.BackupFile, error) {
	cli, fs, err := d.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	defer fs.Close()
	ents, err := fs.ReadDir(d.dir())
	if err != nil {
		return []admproto.BackupFile{}, nil
	}
	out := []admproto.BackupFile{}
	for _, e := range ents {
		if !e.IsDir() && copyRe.MatchString(e.Name()) {
			out = append(out, admproto.BackupFile{Name: e.Name(), Size: e.Size(), At: e.ModTime().UTC()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

func (d *sftpDest) Delete(ctx context.Context, name string) error {
	cli, fs, err := d.dial(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()
	defer fs.Close()
	return fs.Remove(path.Join(d.dir(), name))
}
