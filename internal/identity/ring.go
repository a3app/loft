package identity

// In-memory member-key ring: plaintext tokens keyed by member id.

import (
	"crypto/hmac"
	"fmt"
	"os"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// Ring is the in-memory set of member keys. HMAC and HKDF need the
// plaintext; network.conf keeps only SHA-256. One id may hold several
// tokens (the live key plus a reissue waiting for apply).
type Ring struct {
	mu   sync.RWMutex
	byID map[string][][]byte
}

// NewRing is an empty key ring.
func NewRing() *Ring {
	return &Ring{byID: map[string][][]byte{}}
}

// Load reads `{dir}/members.keys` (YAML map id → lt_… or a list of them).
// Missing file is empty.
func Load(path string) (*Ring, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewRing(), nil
		}
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("members.keys: %w", err)
	}
	r := NewRing()
	for id, v := range raw {
		if id == "" {
			continue
		}
		for _, tok := range yamlTokens(v) {
			r.putLocked(id, tok)
		}
	}
	return r, nil
}

func yamlTokens(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			s, ok := x.(string)
			if !ok || s == "" {
				continue
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}

// Save writes id: lt_… YAML with mode 0600, sorted by id. One token stays a
// scalar so older nodes still read the file; several become a list.
func (r *Ring) Save(path string) error {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	ids := make([]string, 0, len(r.byID))
	m := map[string][][]byte{}
	for id, toks := range r.byID {
		ids = append(ids, id)
		cp := make([][]byte, len(toks))
		for i, tok := range toks {
			cp[i] = append([]byte(nil), tok...)
		}
		m[id] = cp
	}
	r.mu.RUnlock()
	sort.Strings(ids)
	out := ""
	for _, id := range ids {
		toks := m[id]
		if len(toks) == 1 {
			out += id + ": " + string(toks[0]) + "\n"
			continue
		}
		out += id + ":\n"
		for _, tok := range toks {
			out += "  - " + string(tok) + "\n"
		}
	}
	return os.WriteFile(path, []byte(out), 0o600)
}

// Filter drops ids unknown to network.conf and tokens whose SHA-256 does
// not match. A reissue keeps the old token on disk until apply; Filter
// then keeps whichever token matches want.
func (r *Ring) Filter(want map[string]string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, toks := range r.byID {
		sha, ok := want[id]
		if !ok {
			delete(r.byID, id)
			continue
		}
		keep := toks[:0]
		for _, tok := range toks {
			if Match(string(tok), sha) {
				keep = append(keep, tok)
			}
		}
		if len(keep) == 0 {
			delete(r.byID, id)
		} else {
			r.byID[id] = keep
		}
	}
}

// Put stores plaintext token for id. A second token for the same id is
// kept (reissue before apply) so a reload still matches network.conf.
func (r *Ring) Put(id, token string) {
	if r == nil || id == "" || token == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.putLocked(id, token)
}

func (r *Ring) putLocked(id, token string) {
	if r.byID == nil {
		r.byID = map[string][][]byte{}
	}
	b := []byte(token)
	for _, have := range r.byID[id] {
		if hmac.Equal(have, b) {
			return
		}
	}
	r.byID[id] = append(r.byID[id], append([]byte(nil), b...))
}

// Delete removes id if present.
func (r *Ring) Delete(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
}

// Len is the number of members with at least one token.
func (r *Ring) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}

// Tokens copies every plaintext key for CheckAuthAny.
func (r *Ring) Tokens() [][]byte {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([][]byte, 0, len(r.byID))
	for _, toks := range r.byID {
		for _, tok := range toks {
			out = append(out, append([]byte(nil), tok...))
		}
	}
	return out
}

// IDFor returns the member id whose token equals the given bytes.
func (r *Ring) IDFor(token []byte) string {
	if r == nil || len(token) == 0 {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id, toks := range r.byID {
		for _, tok := range toks {
			if hmac.Equal(tok, token) {
				return id
			}
		}
	}
	return ""
}

// TokenFor copies the latest plaintext token for id (the reissued key if
// several are stored).
func (r *Ring) TokenFor(id string) []byte {
	if r == nil || id == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	toks := r.byID[id]
	if len(toks) == 0 {
		return nil
	}
	tok := toks[len(toks)-1]
	return append([]byte(nil), tok...)
}

// Replace takes every key of src (a reload of members.keys).
func (r *Ring) Replace(src *Ring) {
	if r == nil || src == nil {
		return
	}
	src.mu.RLock()
	m := make(map[string][][]byte, len(src.byID))
	for id, toks := range src.byID {
		cp := make([][]byte, len(toks))
		for i, tok := range toks {
			cp[i] = append([]byte(nil), tok...)
		}
		m[id] = cp
	}
	src.mu.RUnlock()
	r.mu.Lock()
	r.byID = m
	r.mu.Unlock()
}
