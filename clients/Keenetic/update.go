package keenetic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/internal/version"
)

// AssetArch is the release filename arch of this binary.
func AssetArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	case "arm":
		return "armv7"
	case "mipsle":
		return "mipsel"
	default:
		return runtime.GOARCH
	}
}

// Update is a newer package the panel can install.
type Update struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Ready   bool   `json:"ready"`
	Arch    string `json:"arch"`
	IPK     string `json:"-"`
	SHA     string `json:"-"`
}

// ghAsset is one file on a GitHub release.
type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// ghRelease is the part of releases/latest this client reads.
type ghRelease struct {
	Tag    string    `json:"tag_name"`
	Assets []ghAsset `json:"assets"`
}

// PickAssets finds the ipk and its .sha256 sidecar for arch.
func PickAssets(assets []ghAsset, arch string) (ipk, sum ghAsset, err error) {
	re := regexp.MustCompile(`^loft-keenetic-(?:[0-9][0-9A-Za-z.+-]*-)?` + regexp.QuoteMeta(arch) + `\.ipk$`)
	for _, a := range assets {
		if re.MatchString(a.Name) {
			ipk = a
			break
		}
	}
	if ipk.Name == "" {
		return ghAsset{}, ghAsset{}, fmt.Errorf("no %s package in the release", arch)
	}
	want := ipk.Name + ".sha256"
	for _, a := range assets {
		if a.Name == want {
			return ipk, a, nil
		}
	}
	return ghAsset{}, ghAsset{}, fmt.Errorf("no sha256 for %s", ipk.Name)
}

// LatestRelease reads GitHub's latest loft release for this arch.
func LatestRelease(ctx context.Context, arch string) (Update, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/a3app/loft/releases/latest", nil)
	if err != nil {
		return Update{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return Update{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return Update{}, fmt.Errorf("github %d", res.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&rel); err != nil {
		return Update{}, err
	}
	ipk, sum, err := PickAssets(rel.Assets, arch)
	if err != nil {
		return Update{}, err
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	cur := strings.TrimPrefix(currentVersion(), "v")
	return Update{
		Current: cur,
		Latest:  latest,
		Ready:   Newer(latest, cur),
		Arch:    arch,
		IPK:     ipk.URL,
		SHA:     sum.URL,
	}, nil
}

// currentVersion is version.Version without a leading v.
// An ldflags value of "dev", or an empty injection, stays non-numeric,
// and Newer will not report that build as Ready against a real tag.
func currentVersion() string {
	return strings.TrimPrefix(strings.TrimSpace(version.Version), "v")
}

// userAgent is loft-keenetic/<version>. A dev or empty build omits the
// version so the request is not attributed to a release that does not exist.
func userAgent() string {
	v := currentVersion()
	if v == "" || v == "dev" {
		return "loft-keenetic"
	}
	return "loft-keenetic/" + v
}

// Newer reports whether latest is a higher dotted version than current.
// Empty current is older than any dotted latest. Non-numeric tags are not newer.
func Newer(latest, current string) bool {
	ls := parts(latest)
	cs := parts(current)
	if ls == nil {
		return false
	}
	n := len(ls)
	if len(cs) > n {
		n = len(cs)
	}
	for i := 0; i < n; i++ {
		var a, b int
		if i < len(ls) {
			a = ls[i]
		}
		if i < len(cs) {
			b = cs[i]
		}
		if a != b {
			return a > b
		}
	}
	return false
}

// parts splits a dotted numeric version. dev, empty, and any non-numeric
// component return nil. Newer treats nil latest as "not a release" and
// nil current as older than any dotted latest.
func parts(s string) []int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if s == "" || s == "dev" {
		return nil
	}
	raw := strings.Split(s, ".")
	out := make([]int, len(raw))
	for i, p := range raw {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// VerifySHA256 checks body against a hex digest or a "hex  name" line.
func VerifySHA256(body []byte, text string) error {
	field := strings.Fields(strings.TrimSpace(text))
	if len(field) == 0 {
		return fmt.Errorf("sha256")
	}
	want, err := hex.DecodeString(field[0])
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("sha256")
	}
	sum := sha256.Sum256(body)
	for i := range want {
		if want[i] != sum[i] {
			return fmt.Errorf("sha256 mismatch")
		}
	}
	return nil
}

// DownloadIPK fetches the package and its digest and checks them.
func DownloadIPK(ctx context.Context, up Update, dst string) error {
	if up.IPK == "" || up.SHA == "" {
		return fmt.Errorf("no package url")
	}
	body, err := getLimited(ctx, up.IPK, 80<<20)
	if err != nil {
		return err
	}
	sum, err := getLimited(ctx, up.SHA, 1<<16)
	if err != nil {
		return err
	}
	if err := VerifySHA256(body, string(sum)); err != nil {
		return err
	}
	return os.WriteFile(dst, body, 0o644)
}

// getLimited downloads at most n bytes. The package cap is 80 MiB and
// the digest cap is 64 KiB. A non-200 is an error; the body is not kept.
func getLimited(ctx context.Context, raw string, n int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("get %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, n))
}

// OpkgInstall runs opkg install --force-reinstall. prerm stops this process.
func OpkgInstall(ctx context.Context, ipk string) error {
	bin := "/opt/bin/opkg"
	if _, err := os.Stat(bin); err != nil {
		bin = "opkg"
	}
	cmd := exec.CommandContext(ctx, bin, "install", "--force-reinstall", ipk)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
