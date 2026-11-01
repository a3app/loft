package web

// presence.go shows who is online. A member is a person with a key to the
// overlay network; each of their devices is an instance. The node writes
// node-status.json with who is attached right now and its relays (other
// nodes that carry traffic for this one); panel-adm hands that file over.
// The panel adds memory: it remembers when each device of each member was
// last seen, in seen.json, so the UI can show "last seen" for offline
// members too.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/store"
)

// nodeStatus is the part of node-status.json the panel reads: when it was
// written, each attached device, and each relay with its load and last
// heartbeat.
type nodeStatus struct {
	At      time.Time `json:"at"`
	Members []struct {
		Member string    `json:"member"`
		IP     string    `json:"ip"`
		Inst   string    `json:"inst"`
		From   string    `json:"from"`
		Front  string    `json:"front,omitempty"`
		Since  time.Time `json:"since"`
		Rx     uint64    `json:"rx,omitempty"`
		Tx     uint64    `json:"tx,omitempty"`
		Key    string    `json:"key,omitempty"` // "current" or "old" controller key; empty when unknown
	} `json:"members"`
	Relays []struct {
		ID    string    `json:"id"`
		URL   string    `json:"url"`
		Load  int       `json:"load"`
		Conns int       `json:"conns"`
		Beat  time.Time `json:"beat"`
		// StateAt is when a standby controller last got this node's state.
		StateAt time.Time `json:"state_at,omitzero"`
	} `json:"relays"`
}

// presence is the last-seen memory, loaded from path on first use and
// written back after every update.
type presence struct {
	mu   sync.Mutex
	seen map[string]map[string]time.Time // member → instance → last seen
	path string
}

// memberConn is one live device of a member: the node reports it right now.
// The member window's Connections tab draws a card per entry.
type memberConn struct {
	Inst  string    `json:"inst"`            // device id the client sent (or the node minted)
	IP    string    `json:"ip,omitempty"`    // the device's address inside the overlay
	From  string    `json:"from,omitempty"`  // where the device connects from
	Front string    `json:"front,omitempty"` // how it carries loft over HTTPS
	Since time.Time `json:"since,omitzero"`  // when this device attached
	Rx    uint64    `json:"rx,omitempty"`
	Tx    uint64    `json:"tx,omitempty"`
	Key   string    `json:"key,omitempty"` // "current" or "old" controller key
}

// memberPresence is one member's line in the UI: how many devices are online
// now, how many were ever seen, when one was last seen, and the address,
// front and traffic of the live connection. Conns lists the devices attached
// right now, one per card on the Connections tab.
type memberPresence struct {
	Online  int          `json:"online"`
	Devices int          `json:"devices"`
	Last    time.Time    `json:"last,omitzero"`
	IP      string       `json:"ip,omitempty"`
	From    string       `json:"from,omitempty"`
	Front   string       `json:"front,omitempty"` // live bulk mux front from the node
	Rx      uint64       `json:"rx,omitempty"`
	Tx      uint64       `json:"tx,omitempty"`
	Key     string       `json:"key,omitempty"` // "current" or "old"; "old" wins if any device still pins the previous key
	Conns   []memberConn `json:"conns,omitempty"`
}

// readPresence fetches node-status.json through the helper.
func (s *Server) readPresence(ctx context.Context) (nodeStatus, error) {
	var st nodeStatus
	var raw json.RawMessage
	if s.Adm == nil {
		return st, admproto.ErrUnavailable
	}
	if err := s.Adm("presence").Call(ctx, admproto.OpPresence, nil, &raw); err != nil {
		return st, err
	}
	err := json.Unmarshal(raw, &st)
	return st, err
}

// track records what the node reports and returns the members' view.
// A status file older than a minute is treated as stale (the node is
// probably stopped), so nobody counts as online, but saved last-seen times
// are still reported.
func (s *Server) track(st nodeStatus) map[string]memberPresence {
	p := &s.pres
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = map[string]map[string]time.Time{}
		if p.path != "" {
			if b, err := os.ReadFile(p.path); err == nil {
				json.Unmarshal(b, &p.seen)
			}
		}
	}
	fresh := time.Since(st.At) < time.Minute // a stopped node leaves an old file
	out := map[string]memberPresence{}
	if fresh {
		for _, m := range st.Members {
			if m.Member == "" {
				continue
			}
			if p.seen[m.Member] == nil {
				p.seen[m.Member] = map[string]time.Time{}
			}
			p.seen[m.Member][m.Inst] = st.At
			x := out[m.Member]
			x.Online++
			x.IP, x.From, x.Front = m.IP, m.From, m.Front
			x.Rx += m.Rx
			x.Tx += m.Tx
			if m.Key == "old" {
				x.Key = "old"
			} else if x.Key == "" {
				x.Key = m.Key
			}
			x.Conns = append(x.Conns, memberConn{
				Inst: m.Inst, IP: m.IP, From: m.From, Front: m.Front,
				Since: m.Since, Rx: m.Rx, Tx: m.Tx, Key: m.Key,
			})
			out[m.Member] = x
		}
	}
	for id, insts := range p.seen {
		x := out[id]
		x.Devices = len(insts)
		for _, t := range insts {
			if t.After(x.Last) {
				x.Last = t
			}
		}
		out[id] = x
	}
	if p.path != "" {
		if b, err := json.Marshal(p.seen); err == nil {
			store.WriteAtomic(p.path, b, 0o600)
		}
	}
	return out
}

// presenceGet returns the members' presence, the relays, and how many
// connected devices trust the current or the old controller key.
func (s *Server) presenceGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.readPresence(r.Context())
	if err != nil {
		admFail(w, err)
		return
	}
	// keys counts connected devices by the controller key their client
	// trusts, to show how far a key roll has got.
	keys := map[string]int{"current": 0, "old": 0}
	if time.Since(st.At) < time.Minute {
		for _, m := range st.Members {
			if m.Key != "" {
				keys[m.Key]++
			}
		}
	}
	ok(w, map[string]any{"at": st.At, "members": s.track(st), "relays": st.Relays, "keys": keys})
}

// SetSeenFile is where the last-seen times are kept (data dir).
func (s *Server) SetSeenFile(dir string) { s.pres.path = filepath.Join(dir, "seen.json") }
