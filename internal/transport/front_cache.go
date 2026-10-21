package transport

// Disk cache of decoy SNI and mask_pub, keyed by node addr. Survives a new
// loft-front process so the first ClientHello on a filtered path is mask.

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type frontDisk struct {
	Decoy   []string `json:"decoy"`
	MaskPub string   `json:"mask_pub"`
}

var (
	frontDiskMu   sync.Mutex
	frontDiskOnce sync.Map // addr -> loaded
)

func frontCachePath() string {
	dir := instanceDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "fronts.cache")
}

func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// maskOnDecoySNI reports a direct ClientHello whose SNI is the dest, not the
// node hostname. The node answers that hello with mask records.
func maskOnDecoySNI(addr, sni string) bool {
	sni = decoyHost(sni)
	if sni == "" {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if strings.EqualFold(sni, host) {
		return false
	}
	if origins, known := CachedDecoy(addr); known {
		if len(origins) == 0 {
			return false
		}
		return strings.EqualFold(sni, decoyHost(origins[0]))
	}
	return true
}

func loadFrontCache(addr string) {
	if addr == "" {
		return
	}
	if _, loaded := frontDiskOnce.LoadOrStore(addr, true); loaded {
		return
	}
	path := frontCachePath()
	if path == "" {
		return
	}
	frontDiskMu.Lock()
	b, err := os.ReadFile(path)
	frontDiskMu.Unlock()
	if err != nil || len(b) == 0 {
		return
	}
	var all map[string]frontDisk
	if json.Unmarshal(b, &all) != nil {
		return
	}
	ent, ok := all[addr]
	if !ok {
		return
	}
	if _, known := CachedDecoy(addr); !known && ent.Decoy != nil {
		RememberDecoy(addr, ent.Decoy)
	}
	if len(CachedMaskPub(addr)) != 32 {
		if pub := ParseMaskPub(ent.MaskPub); len(pub) == 32 {
			RememberMaskPub(addr, pub)
		}
	}
}

func saveFrontCache(addr string) {
	path := frontCachePath()
	if path == "" || addr == "" {
		return
	}
	ent := frontDisk{}
	if origins, known := CachedDecoy(addr); known {
		ent.Decoy = append([]string(nil), origins...)
	}
	if pub := CachedMaskPub(addr); len(pub) == 32 {
		ent.MaskPub = base64.RawURLEncoding.EncodeToString(pub)
	}
	if ent.Decoy == nil && ent.MaskPub == "" {
		return
	}
	frontDiskMu.Lock()
	defer frontDiskMu.Unlock()
	all := map[string]frontDisk{}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &all)
	}
	all[addr] = ent
	raw, err := json.Marshal(all)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(raw, '\n'), 0o600)
}
