// Package origin serves the node's public HTTPS origin.
// Cover HTML, GET/WS/H2 fronts, splice, and signed roster/lists share one mux.
package origin

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/wire"
	"github.com/a3app/loft/server/origin/cover"
)

// New builds the origin HTTP handler: roster, lists, H2, splice, GET, WebSocket, cover.
func New(cfg *config.ServerConfig, n *netconf.Network) (http.Handler, error) {
	info := nodeInfo(cfg)
	if n != nil {
		transport.NetSwitch = transport.NewOverlay(n)
		if !n.P2PEnabled() {
			info.Caps &^= wire.CapP2P
		}
	}
	if !cfg.IsRelay() {
		priv, err := ecrypto.LoadControllerKey(cfg.ControllerKey, cfg.Token)
		if err == nil {
			fl := transport.NewFleet(priv, cfg)
			transport.FleetCtl = fl
			info.RosterFn = fl.RosterBytes
			info.Caps |= wire.CapRoster
			listsKey = priv
			if blob, ser, hash := signLists(cfg, priv); len(blob) > 0 {
				lists.Store(&signedLists{blob, ser, hash})
				info.ListsFn = currentLists
				info.Caps |= wire.CapLists
			}
		}
	}
	getFront := transport.NewGETServer(cfg.Token, cfg.GetPath)
	getFront.Decoy = append([]string(nil), cfg.Decoy...)
	getFront.Info = info
	if cfg.GetH2 > 1 {
		getFront.H2Conns = byte(cfg.GetH2)
	}
	wss := &transport.WSSServer{Token: []byte(cfg.Token), Decoy: append([]string(nil), cfg.Decoy...), Info: info}
	h2s := &transport.H2Server{Info: info, Path: cfg.H2Path}
	if getFront.Seen == nil {
		getFront.Seen = ecrypto.NewNonces()
	}
	splice := &transport.SpliceServer{Token: []byte(cfg.Token), Seen: getFront.Seen}
	site := coverHandler(cfg.CoverDir)

	// Plain HandlerFunc: Go 1.22+ ServeMux 404s HTTP/2 CONNECT (empty path,
	// :authority is the target). Catch-all CONNECT was an open proxy; splice
	// is only fronts: connect, and every request carries 0x02.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == cfg.RosterPath:
			serveRoster(w, r)
		case r.Method == http.MethodGet && r.URL.Path == cfg.ListsPath:
			serveLists(w, r)
		case r.Method == http.MethodConnect && h2s.Match(r) && cfg.HasFront(wire.FrontH2):
			h2s.ServeHTTP(w, r)
		case r.Method == http.MethodConnect && cfg.HasFront(wire.FrontConnect):
			splice.ServeHTTP(w, r)
		case cfg.HasFront(wire.FrontGET) && getFront.Match(r.URL.Path):
			getFront.ServeHTTP(w, r)
		case cfg.HasFront(wire.FrontWS) && (r.URL.Path == cfg.WSPath || strings.HasPrefix(r.URL.Path, cfg.WSPath+"/")):
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				wss.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		default:
			site.ServeHTTP(w, r)
		}
	})
	transport.Hub = h
	return h, nil
}

// nodeInfo fills Status fields shared by every front on this node.
func nodeInfo(cfg *config.ServerConfig) transport.NodeInfo {
	var mask *transport.Mask
	if cfg.Mask && len(cfg.Decoy) > 0 {
		mask = transport.NewMask([]byte(cfg.Token), cfg.Decoy[0])
	}
	var pub []byte
	if mask != nil {
		pub = mask.Public()
	}
	return transport.NodeInfo{
		Token:       []byte(cfg.Token),
		Decoy:       append([]string(nil), cfg.Decoy...),
		Caps:        transport.ServerCaps(cfg, mask != nil),
		MaskPub:     pub,
		Replicas:    append([]string(nil), cfg.Replicas...),
		CanaryFlags: cfg.Canary.Flags(),
		CanaryPad:   cfg.Canary.Pad16(),
		QuicALPN:    cfg.QuicALPN,
		Roster:      nil,
	}
}

// Listen serves HTTPS on cfg.Listen. Without cert/key it is plaintext HTTP.
func Listen(cfg *config.ServerConfig, h http.Handler) error {
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return srv.ListenAndServe()
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return err
	}
	srv.TLSConfig = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2", "http/1.1"},
		Certificates: []tls.Certificate{cert},
	}
	transport.EnableHTTP2ExtendedConnect()
	_ = http2.ConfigureServer(srv, transport.H2HTTPServer())
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	ln = transport.TuneListener(ln)
	var mask *transport.Mask
	if cfg.Mask && len(cfg.Decoy) > 0 {
		mask = transport.NewMask([]byte(cfg.Token), cfg.Decoy[0])
	}
	ln = transport.WrapDecoyMask(ln, []byte(cfg.Token), cfg.Decoy, mask, h,
		transport.PublicSNIs(cfg.Advertise, cfg.CertFile)...)
	transport.ApplyCDNPeers(ln, cfg.CDNPeers)
	transport.ApplyDecoyProxy(ln, cfg.DecoyProxy)
	return srv.Serve(tls.NewListener(ln, srv.TLSConfig))
}

// ListenAll serves TCP HTTPS and UDP QUIC/H3 on the same port.
func ListenAll(cfg *config.ServerConfig, h http.Handler) error {
	if cfg.IsRelay() {
		return transport.RunRelay(context.Background(), cfg)
	}
	errc := make(chan error, 2)
	info := nodeInfo(cfg)
	if transport.FleetCtl != nil {
		info.RosterFn = transport.FleetCtl.RosterBytes
		info.Caps |= wire.CapRoster
	}
	if l := lists.Load(); l != nil && len(l.blob) > 0 {
		info.ListsFn = currentLists
		info.Caps |= wire.CapLists
	}
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		go func() {
			if len(cfg.Decoy) > 0 {
				errc <- transport.ListenUDPDecoy(cfg.Listen, cfg.CertFile, cfg.KeyFile, info, cfg.EnabledFronts(), cfg.Decoy[0])
				return
			}
			errc <- transport.ListenUDP(cfg.Listen, cfg.CertFile, cfg.KeyFile, info, cfg.EnabledFronts())
		}()
	}
	go func() { errc <- Listen(cfg, h) }()
	return <-errc
}

// lists is the signed lists payload, swapped by ReloadLists.
type signedLists struct {
	blob   []byte
	serial uint64
	hash   []byte
}

var (
	lists    atomic.Pointer[signedLists]
	listsKey ed25519.PrivateKey
)

func currentLists() (uint64, []byte) {
	if l := lists.Load(); l != nil {
		return l.serial, l.hash
	}
	return 0, nil
}

// ReloadLists re-signs cfg.Lists (on SIGHUP). Lists that appear on a node
// that started without any are served, but clients learn of them only
// after a restart (the capability is set at start).
func ReloadLists(cfg *config.ServerConfig) {
	if listsKey == nil {
		return
	}
	blob, ser, hash := signLists(cfg, listsKey)
	lists.Store(&signedLists{blob, ser, hash})
}

// signLists encodes and Ed25519-signs the controller address lists.
func signLists(cfg *config.ServerConfig, priv ed25519.PrivateKey) ([]byte, uint64, []byte) {
	if len(cfg.Lists) == 0 {
		return nil, 0, nil
	}
	names := make([]string, 0, len(cfg.Lists))
	for n := range cfg.Lists {
		names = append(names, n)
	}
	sort.Strings(names)
	l := wire.Lists{Serial: uint64(time.Now().Unix())}
	if l.Serial == 0 {
		l.Serial = 1
	}
	for _, n := range names {
		al := wire.AddrList{Name: n}
		for _, s := range cfg.Lists[n] {
			a := wire.ParseAddr(s)
			if a.Val != "" {
				al.Entries = append(al.Entries, a)
			}
		}
		l.Sets = append(l.Sets, al)
	}
	l = ecrypto.SignLists(priv, l)
	h := l.Hash()
	return l.Encode(), l.Serial, h[:]
}

// serveLists writes the signed lists as a JSON manifest.
func serveLists(w http.ResponseWriter, r *http.Request) {
	cur := lists.Load()
	if cur == nil || len(cur.blob) == 0 {
		http.NotFound(w, r)
		return
	}
	l, err := wire.DecodeLists(cur.blob)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := l.ManifestJSON()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// serveRoster writes the signed fleet roster as a JSON manifest.
func serveRoster(w http.ResponseWriter, r *http.Request) {
	var raw []byte
	if transport.FleetCtl != nil {
		raw = transport.FleetCtl.RosterBytes()
	}
	if len(raw) == 0 {
		http.NotFound(w, r)
		return
	}
	ros, err := wire.DecodeRoster(raw)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := ros.ManifestJSON()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// coverHandler serves CoverDir if it is a directory, else the embedded cover site.
func coverHandler(dir string) http.Handler {
	if dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return http.FileServer(http.Dir(dir))
		}
	}
	return cover.Handler()
}
