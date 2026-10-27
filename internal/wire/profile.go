package wire

import (
	"encoding/json"
	"strings"
)

// RouteProfile is the GUI view of a signed lists blob: header (empty set
// name) plus named sets with kind 4 direction. Sets without kind 4 are
// omitted; the client maps those itself (§10.3).
type RouteProfile struct {
	Name   string
	DNS    string
	Serial uint64
	Sets   []RouteSet
}

// RouteSet is one named list as a zone service: dir via/direct/drop.
type RouteSet struct {
	ID      string
	Label   string
	Dir     string
	CIDRs   []string
	Domains []string
}

// Split zone ids the GUI uses for kind 4.
const (
	ZoneLocal  = "local"  // direct
	ZoneGlobal = "global" // via
	ZoneBlock  = "block"  // drop
)

// SplitZone maps kind 4 onto builtin SplitStore zones.
func SplitZone(dir string) string {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case DirDirect:
		return ZoneLocal
	case DirDrop:
		return ZoneBlock
	default:
		return ZoneGlobal
	}
}

// RouteProfile reads header metadata and sets that carry kind 4.
func (l Lists) RouteProfile() RouteProfile {
	p := RouteProfile{Serial: l.Serial}
	for _, s := range l.Sets {
		dir, label, dns := setMeta(s)
		if s.Name == "" {
			if label != "" {
				p.Name = label
			}
			if dns != "" {
				p.DNS = dns
			}
			continue
		}
		if dir == "" {
			continue
		}
		rs := RouteSet{ID: s.Name, Label: label, Dir: dir}
		if rs.Label == "" {
			rs.Label = s.Name
		}
		for _, e := range s.Entries {
			switch e.Kind {
			case KindCIDR:
				if e.Val != "" {
					rs.CIDRs = append(rs.CIDRs, e.Val)
				}
			case KindExact, KindSuffix:
				if e.Val != "" {
					rs.Domains = append(rs.Domains, e.Val)
				}
			}
		}
		p.Sets = append(p.Sets, rs)
	}
	return p
}

func setMeta(s AddrList) (dir, label, dns string) {
	for _, e := range s.Entries {
		switch e.Kind {
		case KindDir:
			d := strings.ToLower(strings.TrimSpace(e.Val))
			if d == DirVia || d == DirDirect || d == DirDrop {
				dir = d
			}
		case KindLabel:
			if e.Val != "" && label == "" {
				label = e.Val
			}
		case KindDNS:
			if e.Val != "" && dns == "" {
				dns = e.Val
			}
		}
	}
	return
}

// SetDir is kind 4 on a named set, or "".
func (s AddrList) SetDir() string {
	dir, _, _ := setMeta(s)
	return dir
}

// SplitJSON is the operator profile as a SplitStore document: builtin
// zones plus services with src=op. Empty CIDR/domain slices encode as [].
func (p RouteProfile) SplitJSON() ([]byte, error) {
	type zone struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Builtin bool   `json:"builtin"`
		Through bool   `json:"through"`
		Block   bool   `json:"block"`
	}
	type svc struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Zone    string   `json:"zone"`
		Src     string   `json:"src"`
		CIDRs   []string `json:"cidrs"`
		Domains []string `json:"domains"`
	}
	type doc struct {
		Name     string `json:"name"`
		DNS      string `json:"dns"`
		Serial   uint64 `json:"serial"`
		Src      string `json:"src"`
		Zones    []zone `json:"zones"`
		Services []svc  `json:"services"`
	}
	d := doc{
		Name:   p.Name,
		DNS:    p.DNS,
		Serial: p.Serial,
		Src:    "op",
		Zones: []zone{
			{ID: ZoneLocal, Name: "Local", Builtin: true},
			{ID: ZoneGlobal, Name: "Global", Builtin: true, Through: true},
			{ID: ZoneBlock, Name: "Block", Builtin: true, Block: true},
		},
		Services: []svc{},
	}
	for _, s := range p.Sets {
		cidrs, domains := s.CIDRs, s.Domains
		if cidrs == nil {
			cidrs = []string{}
		}
		if domains == nil {
			domains = []string{}
		}
		d.Services = append(d.Services, svc{
			ID: s.ID, Name: s.Label, Zone: SplitZone(s.Dir), Src: "op",
			CIDRs: cidrs, Domains: domains,
		})
	}
	return json.Marshal(d)
}
