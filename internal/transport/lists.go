package transport

// Named address lists: controller-signed sets mapped by the client to via/direct/drop.
//
// Lists are named sets of domains and subnets, signed by the controller.
// The node only publishes them; the client decides what each
// name means in client.yaml use: (via = through loft, direct = bypass loft,
// drop = refuse), with default: for anything unmatched. This way a list can
// be updated centrally without touching each client's policy. Status carries
// the lists serial and hash, so a client refetches only when they change.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

// Action is the client routing decision for a named-list match.
type Action byte

// ActVia sends through the overlay, ActDirect dials the host, ActDrop refuses it.
const (
	ActVia Action = iota
	ActDirect
	ActDrop
)

// ParseAction maps "direct" / "drop" / anything else (via) from client.yaml use:.
func ParseAction(s string) Action {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "direct":
		return ActDirect
	case "drop":
		return ActDrop
	default:
		return ActVia
	}
}

// book is the live named-list snapshot: signed sets plus client use map.
// It is replaced as a whole (copy on write), so LookupAddr needs no lock.
type book struct {
	sets map[string][]wire.Addr
	use  map[string]Action
	def  Action
	ser  uint64
	hash [32]byte
}

var liveBook atomic.Pointer[book]

// SetUse installs client.yaml use: and default: without dropping fetched sets.
func SetUse(cfg *config.ClientConfig) {
	nb := &book{def: ParseAction(cfg.Default), use: map[string]Action{}, sets: map[string][]wire.Addr{}}
	if cur := liveBook.Load(); cur != nil {
		nb.sets = cur.sets
		nb.ser = cur.ser
		nb.hash = cur.hash
	}
	for k, v := range cfg.Use {
		nb.use[strings.ToLower(k)] = ParseAction(v)
	}
	liveBook.Store(nb)
}

// RememberLists stores a signed Lists document, keeping the current use map.
// The signature must be valid and made by the controller key (the same key
// that signs the roster): lists decide which traffic leaves the tunnel, so a
// document signed by anyone else, for example served by a CDN or a box on the
// path, is refused. An older serial than the one in use is refused too, so an
// old document cannot be replayed over a newer one.
func RememberLists(l wire.Lists) error {
	if err := l.Verify(); err != nil {
		return err
	}
	rosterMu.Lock()
	trusted := trustSignerLocked(l.Pub)
	rosterMu.Unlock()
	if !trusted {
		return errListsSigner
	}
	cur := liveBook.Load()
	if cur != nil && l.Serial < cur.ser {
		return errListsOld
	}
	sets := map[string][]wire.Addr{}
	kindUse := map[string]Action{}
	for _, s := range l.Sets {
		name := strings.ToLower(s.Name)
		sets[name] = s.Entries
		if name == "" {
			continue
		}
		if dir := s.SetDir(); dir != "" {
			kindUse[name] = ParseAction(dir)
		}
	}
	nb := &book{sets: sets, ser: l.Serial, hash: l.Hash(), def: ActVia, use: map[string]Action{}}
	if cur != nil {
		nb.def = cur.def
		for k, v := range cur.use {
			nb.use[k] = v
		}
	}
	for k, v := range kindUse {
		if _, exists := nb.use[k]; !exists {
			nb.use[k] = v
		}
	}
	liveBook.Store(nb)
	if raw, err := l.RouteProfile().SplitJSON(); err == nil {
		liveProfile.Store(string(raw))
	}
	return nil
}

var liveProfile atomic.Value // string SplitJSON

// ListsProfileJSON is the operator SplitStore document of the last verified
// lists, or "". gomobile Snapshot carries it for the Android GUI.
func ListsProfileJSON() string {
	s, _ := liveProfile.Load().(string)
	return s
}

var (
	errListsSigner = errors.New("lists: not signed by the controller key (set controller_pub or tofu: true)")
	errListsOld    = errors.New("lists: older serial than the lists in use")
)

// LookupAddr applies drop, then direct, then via on matching mapped lists; else default.
// Drop wins over everything so a blocking list cannot be bypassed by a host
// that also sits in a wider via or direct list. Sets with no use: entry are
// ignored. Without any lists the answer is via.
func LookupAddr(host string, ip net.IP) Action {
	b := liveBook.Load()
	if b == nil {
		return ActVia
	}
	var drop, direct, via bool
	for name, entries := range b.sets {
		act, ok := b.use[name]
		if !ok {
			continue
		}
		for _, e := range entries {
			if e.MatchHost(host) || e.MatchIP(ip) {
				switch act {
				case ActDrop:
					drop = true
				case ActDirect:
					direct = true
				default:
					via = true
				}
				break
			}
		}
	}
	if drop {
		return ActDrop
	}
	if direct {
		return ActDirect
	}
	if via {
		return ActVia
	}
	return b.def
}

// ListsSerial is the serial of the last verified lists document, or 0.
func ListsSerial() uint64 {
	b := liveBook.Load()
	if b == nil {
		return 0
	}
	return b.ser
}

// FetchLists GETs a public lists JSON manifest, verifies the signature, and stores it.
func FetchLists(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", chromeUA)
	cl := &http.Client{Timeout: 12 * time.Second}
	res, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("lists fetch: %s", res.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 512<<10))
	if err != nil {
		return err
	}
	l, err := wire.ParseListsManifest(raw)
	if err != nil {
		return err
	}
	return RememberLists(l)
}

// BootstrapLists applies use: then fetches lists.json from ListsURL, bootstrap, or CDN URLs.
// The URLs it tried are kept, so maybeRefreshLists can refetch from them
// later when Status announces a newer serial.
// Direct node origins are skipped the same way as BootstrapRoster.
func BootstrapLists(ctx context.Context, cfg *config.ClientConfig) {
	SetUse(cfg)
	decoy := decoyNodeHosts(cfg)
	urls := []string{}
	if cfg.ListsURL != "" && !skipPublicFetch(cfg.ListsURL, decoy) {
		urls = append(urls, cfg.ListsURL)
	}
	path := "/static/v3/lists.json"
	if cfg.Bootstrap != "" {
		u := cfg.Bootstrap
		if i := strings.LastIndex(u, "/"); i >= 0 {
			lu := u[:i] + path
			if !skipPublicFetch(lu, decoy) {
				urls = append(urls, lu)
			}
		}
	}
	seen := map[string]struct{}{}
	for _, u := range urls {
		seen[u] = struct{}{}
	}
	for _, o := range cfg.Outbounds {
		if o.CDN == "" || o.URL == "" {
			continue
		}
		urls = appendPublicURL(urls, seen, strings.TrimRight(o.URL, "/")+path, decoy)
	}
	listSources.Store(urls)
	for _, u := range urls {
		cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		err := FetchLists(cctx, u)
		cancel()
		if err == nil {
			return
		}
	}
}

// listSources holds the URLs BootstrapLists built, for later refreshes.
var listSources atomic.Value // []string

// maybeRefreshLists refetches when Status lists serial/hash is newer than the live book.
// lists_inline: GET is skipped; AskLists on the live mux fetches the body.
func maybeRefreshLists(st wire.Status) {
	if st.ListsSerial == 0 {
		return
	}
	if st.Caps.Has(wire.CapListsInline) {
		return
	}
	if b := liveBook.Load(); b != nil {
		if st.ListsSerial < b.ser {
			return
		}
		if st.ListsSerial == b.ser && bytesEqual32(b.hash, st.ListsHash) {
			return
		}
	}
	urls, _ := listSources.Load().([]string)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	for _, u := range urls {
		if FetchLists(ctx, u) == nil {
			return
		}
	}
}

// bytesEqual32 compares a SHA-256 array to a Status hash field.
func bytesEqual32(h [32]byte, b []byte) bool {
	if len(b) != 32 {
		return false
	}
	for i := 0; i < 32; i++ {
		if h[i] != b[i] {
			return false
		}
	}
	return true
}
