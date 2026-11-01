package web

// update.go keeps loft itself up to date. The panel asks GitHub for the
// project's releases, records the newest one that ships a panel archive for
// this machine, and shows it on the Packages screen. Installing is done by
// panel-adm, the root helper, as a job that updates both the node and the
// panel. In "auto" mode FollowReleases starts that job on its own inside the
// chosen time window; in "notify" mode it only checks.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/store"
)

// ReleasesAPI lists the project's releases.
const ReleasesAPI = "https://api.github.com/repos/a3app/loft/releases?per_page=30"

// tagRe is the release tag form the panel accepts: v1.2.3 with an optional
// pre-release suffix. Anything else on GitHub is ignored.
var tagRe = regexp.MustCompile(`^v\d{1,4}\.\d{1,4}\.\d{1,6}(-[0-9A-Za-z.]{1,32})?$`)

// checkEvery is how often the panel asks GitHub (60 requests an hour is the
// anonymous limit; this is one in six hours).
const checkEvery = 6 * time.Hour

// ghRelease is the part of a GitHub release the panel reads.
type ghRelease struct {
	Tag        string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

// latestRelease picks the newest release that carries the panel for arch
// (both the archive and its checksum file). Drafts are skipped, and
// pre-releases unless pre is set.
func latestRelease(rels []ghRelease, pre bool, arch string) (ghRelease, bool) {
	var best ghRelease
	found := false
	for _, r := range rels {
		if r.Draft || (r.Prerelease && !pre) || !tagRe.MatchString(r.Tag) {
			continue
		}
		ver := strings.TrimPrefix(r.Tag, "v")
		want := map[string]bool{
			fmt.Sprintf("loft-panel-%s-linux-%s.tar.gz", ver, arch): false,
			fmt.Sprintf("loft-panel-%s-SHA256SUMS.txt", ver):        false,
		}
		for _, a := range r.Assets {
			if _, ok := want[a.Name]; ok {
				want[a.Name] = true
			}
		}
		all := true
		for _, v := range want {
			all = all && v
		}
		if all && (!found || cmpVersion(ver, strings.TrimPrefix(best.Tag, "v")) > 0) {
			best, found = r, true
		}
	}
	return best, found
}

// cmpVersion compares 1.2.3[-pre]: numbers first, a pre-release below its release.
func cmpVersion(a, b string) int {
	an, ap, _ := strings.Cut(a, "-")
	bn, bp, _ := strings.Cut(b, "-")
	as, bs := strings.Split(an, "."), strings.Split(bn, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	case ap < bp:
		return -1
	}
	return 1
}

// CheckReleases asks GitHub for the newest release and records it in the
// settings, together with the check time or the error.
func (s *Server) CheckReleases(ctx context.Context) (store.Settings, error) {
	cur, err := s.Settings.Get()
	if err != nil {
		return cur, err
	}
	rel, cerr := s.fetchLatest(ctx, cur.Update.Channel == "pre")
	return s.Settings.Update(func(st *store.Settings) {
		st.Update.Checked = time.Now().UTC()
		st.Update.CheckError = ""
		if cerr != nil {
			st.Update.CheckError = cerr.Error()
			return
		}
		st.Update.Latest, st.Update.LatestDate = rel.Tag, rel.Published
	})
}

// fetchLatest downloads the release list (from ReleasesAPI, or the test
// override in s.ReleasesAPI) and picks the newest usable release.
func (s *Server) fetchLatest(ctx context.Context, pre bool) (ghRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	url := s.ReleasesAPI
	if url == "" {
		url = ReleasesAPI
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "loft-panel/"+version.Version)
	c := s.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return ghRelease{}, fmt.Errorf("GitHub is not reachable: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ghRelease{}, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	var rels []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rels); err != nil {
		return ghRelease{}, fmt.Errorf("GitHub releases: %v", err)
	}
	r, ok := latestRelease(rels, pre, runtime.GOARCH)
	if !ok {
		return ghRelease{}, errors.New("no release carries the panel yet")
	}
	return r, nil
}

// newer reports a recorded release above the running version.
func newer(u store.Update) bool {
	return u.Latest != "" && cmpVersion(strings.TrimPrefix(u.Latest, "v"), version.Version) > 0
}

// serverZone is the zone set now (Settings can change it while the panel
// runs; time.Local is read once at start).
func serverZone() *time.Location {
	if l, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(l, "zoneinfo/"); ok {
			if loc, err := time.LoadLocation(name); err == nil {
				return loc
			}
		}
	}
	return time.Local
}

// inWindow reports whether t falls in the update window: "now" is always,
// "sunday" is 4 to 5 a.m. on Sundays, and anything else ("daily") is 4 to 5
// a.m. every day, in the server's time zone.
func inWindow(window string, t time.Time) bool {
	switch window {
	case "now":
		return true
	case "sunday":
		return t.Weekday() == time.Sunday && t.Hour() == 4
	}
	return t.Hour() == 4
}

// FollowReleases checks every six hours and, in auto mode, starts the
// update inside the window. A release that failed is not retried for a day.
// It wakes every 10 minutes and runs until ctx ends.
func (s *Server) FollowReleases(ctx context.Context) {
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		st, err := s.Settings.Get()
		if err == nil && st.Update.Mode != "off" {
			if time.Since(st.Update.Checked) > checkEvery {
				st, _ = s.CheckReleases(ctx)
			}
			u := st.Update
			if u.Mode == "auto" && newer(u) && inWindow(u.Window, time.Now().In(serverZone())) && (u.Tried != u.Latest || time.Since(u.TriedAt) > 24*time.Hour) {
				s.Settings.Update(func(x *store.Settings) { x.Update.Tried, x.Update.TriedAt = u.Latest, time.Now().UTC() })
				var j admproto.Job
				if err := s.Adm("auto-update").Call(ctx, admproto.OpUpdateApply, admproto.UpdateApply{Tag: u.Latest}, &j); err != nil {
					s.logf("auto update to %s: %v", u.Latest, err)
				} else {
					s.logf("auto update to %s started (job %s)", u.Latest, j.ID)
					s.sysEvent("update", u.Latest)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// updateJSON is the update block on the Packages screen: running version,
// settings, the last check and whether a newer release is available.
type updateJSON struct {
	Version    string    `json:"version"`
	Mode       string    `json:"mode"`
	Channel    string    `json:"channel"`
	Window     string    `json:"window"`
	Checked    time.Time `json:"checked,omitzero"`
	Latest     string    `json:"latest,omitempty"`
	LatestDate time.Time `json:"latest_date,omitzero"`
	Error      string    `json:"error,omitempty"`
	Available  bool      `json:"available"`
}

// updateView builds updateJSON from the settings, filling in defaults
// (notify, stable channel, daily window).
func updateView(st store.Settings) updateJSON {
	u := st.Update
	return updateJSON{Version: version.Version, Mode: firstOf(u.Mode, "notify"), Channel: firstOf(u.Channel, "stable"),
		Window: firstOf(u.Window, "daily"), Checked: u.Checked, Latest: u.Latest, LatestDate: u.LatestDate,
		Error: u.CheckError, Available: newer(u)}
}

// firstOf returns a, or b when a is empty.
func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// updateGet returns the update block.
func (s *Server) updateGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.Settings.Get()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, updateView(st))
}

// updateSettings saves the mode, channel and window. Changing the channel
// clears the last result, because the other channel has its own newest
// release.
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct{ Mode, Channel, Window string }
	if !readJSON(w, r, 4<<10, &req) {
		return
	}
	if !oneOf(req.Mode, "auto", "notify", "off") || !oneOf(req.Channel, "stable", "pre") || !oneOf(req.Window, "daily", "sunday", "now") {
		fail(w, http.StatusBadRequest, "bad update settings")
		return
	}
	st, err := s.Settings.Update(func(st *store.Settings) {
		if st.Update.Channel != req.Channel {
			st.Update.Checked, st.Update.Latest = time.Time{}, "" // the other channel has its own newest
		}
		st.Update.Mode, st.Update.Channel, st.Update.Window = req.Mode, req.Channel, req.Window
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, updateView(st))
}

// oneOf reports whether v is one of l.
func oneOf(v string, l ...string) bool {
	for _, x := range l {
		if v == x {
			return true
		}
	}
	return false
}

// updateCheck asks GitHub now instead of waiting for the next check.
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	st, err := s.CheckReleases(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, updateView(st))
}

// updateApply installs the release the last check found; nothing else, so
// the page cannot ask for an arbitrary (older) tag.
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	var req admproto.UpdateApply
	if !readJSON(w, r, 4<<10, &req) {
		return
	}
	st, err := s.Settings.Get()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Tag == "" || req.Tag != st.Update.Latest || !newer(st.Update) {
		fail(w, http.StatusConflict, "stale page: reload")
		return
	}
	var j admproto.Job
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpUpdateApply, req, &j); err != nil {
		admFail(w, err)
		return
	}
	s.logf("update to %s by %s (job %s)", req.Tag, userOf(r).Login, j.ID)
	ok(w, j)
}
