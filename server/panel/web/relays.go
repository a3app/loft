package web

// Relays: issue a relay's key and hand out what its host needs to install —
// a one-line command, the relay's node config, or a full offline bundle.
//
// A relay is a second loft-node in the relay role on another server. It
// enters this controller with a key issued for its id (relays.keys here,
// relay.key there) and trusts only slices signed by this controller's key
// (controller_pub). Everything below is built from node.conf, so the panel
// stores nothing of its own.

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/server/panel/admproto"
)

// relayInstall is what the Relays dialog shows for one relay.
type relayInstall struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	ControllerURL string `json:"controller_url"`
	ControllerPub string `json:"controller_pub"`
	Advertise     string `json:"advertise"`
	Version       string `json:"version"`
	Command       string `json:"command"`
	Config        string `json:"config"` // node.conf for the relay host
}

// relayReleases is where loft-node release archives are fetched from; tests
// point it at a local server.
var relayReleases = "https://github.com/a3app/loft/releases/download"

// relayKeyIssue issues a new key for a relay (the old one stops working)
// and returns its install.
func (s *Server) relayKeyIssue(w http.ResponseWriter, r *http.Request) {
	var in admproto.RelayKeyReq
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	auditTarget(r, in.ID)
	s.relayInstallReply(w, r, admproto.OpRelayKey, in.ID)
}

// relayKeyShow returns the install of a relay with the key it already has.
func (s *Server) relayKeyShow(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		fail(w, http.StatusBadRequest, "relay id required")
		return
	}
	auditTarget(r, id)
	s.relayInstallReply(w, r, admproto.OpRelayGet, id)
}

func (s *Server) relayInstallReply(w http.ResponseWriter, r *http.Request, op, id string) {
	inst, err := s.relayInstall(r.Context(), userOf(r).Login, op, id)
	if err != nil {
		admFail(w, err)
		return
	}
	ok(w, inst)
}

// relayInstall asks the helper for the key (op issues or reads it) and the
// node config, and builds the command and the relay's node.conf.
func (s *Server) relayInstall(ctx context.Context, by, op, id string) (relayInstall, error) {
	a := s.Adm(by)
	var nc admproto.NodeConfig
	if err := a.Call(ctx, admproto.OpNodeConfig, nil, &nc); err != nil {
		return relayInstall{}, err
	}
	i := slices.IndexFunc(nc.Relays, func(x admproto.Relay) bool { return x.ID == id })
	if i < 0 {
		return relayInstall{}, fmt.Errorf("relay %q is not in node.conf: save it first", id)
	}
	if len(nc.ControllerPub) != 64 {
		return relayInstall{}, errors.New("this node has no controller key yet")
	}
	ctl, err := relayControllerURL(nc)
	if err != nil {
		return relayInstall{}, err
	}
	var k admproto.IssuedKey
	if err := a.Call(ctx, op, admproto.RelayKeyReq{ID: id}, &k); err != nil {
		return relayInstall{}, err
	}
	in := relayInstall{
		ID: id, Key: k.Key, ControllerURL: ctl, ControllerPub: nc.ControllerPub,
		Advertise: nc.Relays[i].URL, Version: version.Version,
	}
	in.Command = in.command()
	in.Config = in.nodeConf(nc.Relays[i].Fronts)
	return in, nil
}

// relayControllerURL is where a relay's trunk dials this controller: over
// WebSocket when the controller serves ws, else over GET.
func relayControllerURL(nc admproto.NodeConfig) (string, error) {
	u, err := url.Parse(nc.Advertise)
	if err != nil || u.Host == "" {
		return "", errors.New("set this node's public address first (advertise)")
	}
	if slices.Contains(nc.Fronts, "ws") && nc.WSPath != "" {
		return "wss://" + u.Host + nc.WSPath, nil
	}
	return "https://" + u.Host, nil
}

// args are the installer flags for this relay.
func (in relayInstall) args() string {
	return fmt.Sprintf("--role relay --id %s --advertise %s --controller %s --controller-pub %s --relay-key %s",
		in.ID, in.Advertise, in.ControllerURL, in.ControllerPub, in.Key)
}

// command downloads the release archive for the host's architecture and
// runs its installer as a relay.
func (in relayInstall) command() string {
	return fmt.Sprintf("V=%s; A=$(dpkg --print-architecture); curl -fsSL %s/v$V/loft-node-$V-linux-$A.tar.gz | tar xz && sudo sh loft-node-$V-linux-$A/install.sh %s",
		in.Version, relayReleases, in.args())
}

// nodeConf is the relay host's /etc/loft/node.conf, for a manual install
// (the key goes to /var/lib/loft/relay.key, not into this file).
func (in relayInstall) nodeConf(fronts []string) string {
	if len(fronts) == 0 {
		fronts = []string{"get"}
	}
	return "# loft-node relay " + in.ID + "; its key is in /var/lib/loft/relay.key\n" +
		"role relay\n" +
		"id " + in.ID + "\n" +
		"listen :443\n" +
		"advertise " + in.Advertise + "\n" +
		"controller " + in.ControllerURL + "\n" +
		"controller_pub " + in.ControllerPub + "\n" +
		"cert /etc/loft/tls/fullchain.pem\n" +
		"key /etc/loft/tls/privkey.pem\n" +
		"mask disable\n" +
		"fronts " + strings.Join(fronts, " ") + "\n"
}

// relayBundle sends the loft-node release archive for one architecture with
// install-relay.sh added, for a relay host without access to GitHub. The
// archive is checked against the release's SHA256SUMS first. It holds the
// relay key, so it is only for users who may change the node.
func (s *Server) relayBundle(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	arch := r.URL.Query().Get("arch")
	if id == "" || (arch != "amd64" && arch != "arm64") {
		fail(w, http.StatusBadRequest, "relay id and arch (amd64 or arm64) required")
		return
	}
	auditTarget(r, id)
	inst, err := s.relayInstall(r.Context(), userOf(r).Login, admproto.OpRelayGet, id)
	if err != nil {
		admFail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	name := fmt.Sprintf("loft-node-%s-linux-%s", inst.Version, arch)
	src, err := fetchRelease(ctx, inst.Version, name+".tar.gz", fmt.Sprintf("loft-node-%s-SHA256SUMS.txt", inst.Version))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	script := "#!/bin/sh\n# Installs this loft-node as relay " + inst.ID + " of " + inst.ControllerURL + ".\n" +
		"set -eu\ncd \"$(dirname \"$0\")\"\nexec sh ./install.sh " + inst.args() + " \"$@\"\n"
	out, err := addToTarGz(src, name+"/install-relay.sh", []byte(script), 0o755)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("loft-relay-%s-%s.tar.gz", inst.ID, arch)))
	_, _ = w.Write(out)
}

// fetchRelease downloads one release asset and checks it against the
// release's SHA256SUMS file.
func fetchRelease(ctx context.Context, ver, asset, sums string) ([]byte, error) {
	get := func(name string, max int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, relayReleases+"/v"+ver+"/"+name, nil)
		if err != nil {
			return nil, err
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("release %s: %v", name, err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("release %s: %s", name, res.Status)
		}
		return io.ReadAll(io.LimitReader(res.Body, max))
	}
	list, err := get(sums, 64<<10)
	if err != nil {
		return nil, err
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(list))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			want = strings.ToLower(f[0])
		}
	}
	if len(want) != 64 {
		return nil, fmt.Errorf("release: %s is not in %s", asset, sums)
	}
	body, err := get(asset, 128<<20)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != want {
		return nil, fmt.Errorf("release: %s does not match %s", asset, sums)
	}
	return body, nil
}

// addToTarGz copies a .tar.gz and appends one file to it.
func addToTarGz(src []byte, name string, body []byte, mode int64) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return nil, err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(body); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
