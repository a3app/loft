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
// plaintext; network.conf keeps only SHA-256.
type Ring struct {
	mu   sync.RWMutex
	byID map[string][]byte
}

// NewRing is an empty key ring.
func NewRing() *Ring {
	return &Ring{byID: map[string][]byte{}}
}

// Load reads `{dir}/members.keys` (YAML map id → lt_…). Missing file is empty.
func Load(path string) (*Ring, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewRing(), nil
		}
		return nil, err
	}
	var m map[string]string
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("members.keys: %w", err)
	}
	r := NewRing()
	for id, tok := range m {
		if id == "" || tok == "" {
			continue
		}
		r.byID[id] = []byte(tok)
	}
	return r, nil
}

// Save writes id: lt_… YAML with mode 0600, sorted by id.
func (r *Ring) Save(path string) error {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	ids := make([]string, 0, len(r.byID))
	m := map[string]string{}
	for id, tok := range r.byID {
		ids = append(ids, id)
		m[id] = string(tok)
	}
	r.mu.RUnlock()
	sort.Strings(ids)
	out := ""
	for _, id := range ids {
		out += id + ": " + m[id] + "\n"
	}
	return os.WriteFile(path, []byte(out), 0o600)
}

// Filter drops keys whose id is unknown or whose SHA-256 does not match
// network.conf. want is member id → key_sha256.
func (r *Ring) Filter(want map[string]string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, tok := range r.byID {
		sha, ok := want[id]
		if !ok || !Match(string(tok), sha) {
			delete(r.byID, id)
		}
	}
}

// Put stores plaintext token for id.
func (r *Ring) Put(id, token string) {
	if r == nil || id == "" || token == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byID == nil {
		r.byID = map[string][]byte{}
	}
	r.byID[id] = []byte(token)
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

// Len is the number of stored keys.
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
	for _, tok := range r.byID {
		cp := append([]byte(nil), tok...)
		out = append(out, cp)
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
	for id, tok := range r.byID {
		if hmac.Equal(tok, token) {
			return id
		}
	}
	return ""
}

// TokenFor copies the plaintext token for id.
func (r *Ring) TokenFor(id string) []byte {
	if r == nil || id == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tok := r.byID[id]
	if len(tok) == 0 {
		return nil
	}
	return append([]byte(nil), tok...)
}
