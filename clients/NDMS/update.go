package ndms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/internal/version"
)

// githubAPI lists stable releases. webIndex is the loft.cx catalog.
// Tests point these at a local server and restore them.
var (
	githubAPI = "https://api.github.com/repos/a3app/loft/releases?per_page=30"
	webIndex  = "https://loft.cx/dist/latest.json"
)

// updateHTTP fetches release indexes and packages. Tests replace it.
var updateHTTP = http.DefaultClient

// packageArch is the ipk arch of this process. Tests pin it.
var packageArch = AssetArch

// stableTag is a dotted release. Pre-release suffixes are not installed.
var stableTag = regexp.MustCompile(`^v\d{1,4}\.\d{1,4}\.\d{1,6}$`)

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

// ghAsset is one file on a release. GitHub uses browser_download_url.
// A web catalog may use url instead.
type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Href string `json:"url"`
}

// link is the download URL, GitHub's field first.
func (a ghAsset) link() string {
	if a.URL != "" {
		return a.URL
	}
	return a.Href
}

// ghRelease is the part of a GitHub release this client reads.
// Drafts and pre-releases are skipped.
type ghRelease struct {
	Tag        string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

// PickAssets finds the ipk and its .sha256 sidecar for arch.
func PickAssets(assets []ghAsset, arch string) (ipk, sum ghAsset, err error) {
	re := regexp.MustCompile(`^loft-ndms-(?:[0-9][0-9A-Za-z.+-]*-)?` + regexp.QuoteMeta(arch) + `\.ipk$`)
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

// LatestRelease reads the channel baked into this binary (version.Channel)
// and picks the ipk for arch. github walks the release list, skips drafts
// and pre-releases, and keeps the newest stable tag that has this arch.
// web and oem read a latest.json catalog. store and anything else do not poll.
func LatestRelease(ctx context.Context, arch string) (Update, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch channelKind() {
	case "github":
		return fetchGitHub(ctx, arch, githubAPI)
	case "web":
		return fetchWeb(ctx, arch, webIndex)
	case "oem":
		raw := indexURL(version.UpdateBase)
		if raw == "" {
			return Update{}, fmt.Errorf("update base")
		}
		return fetchWeb(ctx, arch, raw)
	default:
		return quietUpdate(arch), nil
	}
}

// channelKind is the trimmed, lower-cased build channel.
// Empty, store, and unknown names do not poll.
func channelKind() string {
	switch strings.ToLower(strings.TrimSpace(version.Channel)) {
	case "github", "web", "oem":
		return strings.ToLower(strings.TrimSpace(version.Channel))
	default:
		return ""
	}
}

// quietUpdate is a check that found nothing to install and made no request.
func quietUpdate(arch string) Update {
	return Update{Current: currentVersion(), Arch: arch}
}

// fetchGitHub picks the newest stable release that ships this arch.
func fetchGitHub(ctx context.Context, arch, api string) (Update, error) {
	body, err := doGet(ctx, api, 4<<20, "application/vnd.github+json")
	if err != nil {
		return Update{}, err
	}
	var rels []ghRelease
	if err := json.Unmarshal(body, &rels); err != nil {
		return Update{}, err
	}
	var best string
	var assets []ghAsset
	found := false
	for _, r := range rels {
		if r.Draft || r.Prerelease || !stableTag.MatchString(r.Tag) {
			continue
		}
		if _, _, err := PickAssets(r.Assets, arch); err != nil {
			continue
		}
		ver := strings.TrimPrefix(r.Tag, "v")
		if !found || Newer(ver, best) {
			best = ver
			assets = r.Assets
			found = true
		}
	}
	if !found {
		return Update{}, fmt.Errorf("no %s package in the release", arch)
	}
	return releaseUpdate(arch, best, assets)
}

// fetchWeb reads a catalog. version is the release. assets, when they
// name this arch, win. Otherwise the files are loft-ndms-<version>-<arch>.ipk
// and its .sha256 sidecar under base. A next object in the JSON is ignored.
func fetchWeb(ctx context.Context, arch, raw string) (Update, error) {
	body, err := doGet(ctx, raw, 1<<20, "")
	if err != nil {
		return Update{}, err
	}
	var cat struct {
		Version string    `json:"version"`
		Base    string    `json:"base"`
		Assets  []ghAsset `json:"assets"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		return Update{}, err
	}
	latest := strings.TrimPrefix(strings.TrimSpace(cat.Version), "v")
	if parts(latest) == nil {
		return Update{}, fmt.Errorf("version")
	}
	assets := cat.Assets
	if _, _, err := PickAssets(assets, arch); err != nil {
		built, berr := assetsFromBase(latest, cat.Base, arch)
		if berr != nil {
			return Update{}, err
		}
		assets = built
	}
	return releaseUpdate(arch, latest, assets)
}

// assetsFromBase names the Dist ipk and its sidecar under a directory URL.
func assetsFromBase(latest, base, arch string) ([]ghAsset, error) {
	if !httpURL(base) {
		return nil, fmt.Errorf("package url")
	}
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return nil, fmt.Errorf("package url")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	name := "loft-ndms-" + latest + "-" + arch + ".ipk"
	ipk := *u
	sum := *u
	ipk.Path += name
	sum.Path += name + ".sha256"
	return []ghAsset{
		{Name: name, URL: ipk.String()},
		{Name: name + ".sha256", URL: sum.String()},
	}, nil
}

// indexURL is base itself when it already ends in .json, otherwise base/latest.json.
func indexURL(base string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(u.Path), ".json") {
		if !strings.HasSuffix(u.Path, "/") {
			u.Path += "/"
		}
		u.Path += "latest.json"
	}
	return u.String()
}

// releaseUpdate fills Update from a version and the assets PickAssets accepts.
func releaseUpdate(arch, latest string, assets []ghAsset) (Update, error) {
	ipk, sum, err := PickAssets(assets, arch)
	if err != nil {
		return Update{}, err
	}
	ipkURL, shaURL := ipk.link(), sum.link()
	if !httpURL(ipkURL) || !httpURL(shaURL) {
		return Update{}, fmt.Errorf("package url")
	}
	latest = strings.TrimPrefix(strings.TrimSpace(latest), "v")
	cur := currentVersion()
	return Update{
		Current: cur,
		Latest:  latest,
		Ready:   Newer(latest, cur),
		Arch:    arch,
		IPK:     ipkURL,
		SHA:     shaURL,
	}, nil
}

// httpURL accepts http and https with a host. Other schemes are refused.
func httpURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http"
}

// installPackage downloads the ipk, checks the sidecar, and reinstalls it.
// prerm stops this process; the new package's postinst starts it again.
func installPackage(ctx context.Context, up Update) error {
	const dst = "/opt/tmp/loft-ndms.ipk"
	if err := DownloadIPK(ctx, up, dst); err != nil {
		return err
	}
	return OpkgInstall(ctx, dst)
}

// currentVersion is version.Version without a leading v.
// An ldflags value of "dev", or an empty injection, stays non-numeric,
// and Newer will not report that build as Ready against a real tag.
func currentVersion() string {
	return strings.TrimPrefix(strings.TrimSpace(version.Version), "v")
}

// userAgent is loft-ndms/<version>. A dev or empty build omits the
// version so the request is not attributed to a release that does not exist.
func userAgent() string {
	v := currentVersion()
	if v == "" || v == "dev" {
		return "loft-ndms"
	}
	return "loft-ndms/" + v
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
	return doGet(ctx, raw, n, "")
}

// doGet is getLimited with an optional Accept header. GitHub wants
// application/vnd.github+json; a catalog does not.
func doGet(ctx context.Context, raw string, n int64, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := updateHTTP.Do(req)
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
