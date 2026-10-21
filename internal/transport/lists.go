package transport

// Named address lists: controller-signed sets mapped by the client to via/direct/drop.

import (
	"context"
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

// RememberLists stores a verified signed Lists document, keeping the current use map.
func RememberLists(l wire.Lists) {
	if err := l.Verify(); err != nil {
		return
	}
	sets := map[string][]wire.Addr{}
	for _, s := range l.Sets {
		sets[strings.ToLower(s.Name)] = s.Entries
	}
	nb := &book{sets: sets, ser: l.Serial, hash: l.Hash(), def: ActVia, use: map[string]Action{}}
	if cur := liveBook.Load(); cur != nil {
		nb.def = cur.def
		nb.use = cur.use
	}
	liveBook.Store(nb)
}

// LookupAddr applies drop, then direct, then via on matching mapped lists; else default.
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
	if err := l.Verify(); err != nil {
		return err
	}
	RememberLists(l)
	return nil
}

// BootstrapLists applies use: then fetches lists.json from ListsURL, bootstrap, or CDN URLs.
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
	for _, o := range cfg.Outbounds {
		if o.CDN != "" && o.URL != "" {
			urls = append(urls, strings.TrimRight(o.URL, "/")+path)
		}
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

var listSources atomic.Value // []string

// maybeRefreshLists refetches when Status lists serial/hash is newer than the live book.
func maybeRefreshLists(st wire.Status) {
	if st.ListsSerial == 0 {
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
