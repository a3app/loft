package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// MemberKeysFile is member-keys.json under the data directory: plaintext
// member secrets the operator asked the panel to keep, so a link, QR and
// .loft can be shown again. Mode 0600. Never returned from GET /api/settings.
const MemberKeysFile = "member-keys.json"

// StoredKey is one member's kept secret.
type StoredKey struct {
	Key    string `json:"key"`
	SHA256 string `json:"sha256"`
}

type keysDisk struct {
	Version int                  `json:"version"`
	Keys    map[string]StoredKey `json:"keys"`
}

// MemberKeys is the panel's copy of member secrets.
type MemberKeys struct {
	path string
	mu   sync.Mutex
}

// OpenMemberKeys returns dir/member-keys.json (it may not exist yet).
func OpenMemberKeys(dir string) *MemberKeys {
	return &MemberKeys{path: filepath.Join(dir, MemberKeysFile)}
}

func (k *MemberKeys) read() (map[string]StoredKey, error) {
	m := map[string]StoredKey{}
	b, err := os.ReadFile(k.path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var f keysDisk
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Keys != nil {
		m = f.Keys
	}
	return m, nil
}

func (k *MemberKeys) write(m map[string]StoredKey) error {
	if m == nil {
		m = map[string]StoredKey{}
	}
	b, err := json.MarshalIndent(keysDisk{Version: 1, Keys: m}, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(k.path, append(b, '\n'), 0o600)
}

// Get returns the kept key for id.
func (k *MemberKeys) Get(id string) (StoredKey, bool, error) {
	if k == nil || id == "" {
		return StoredKey{}, false, nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	m, err := k.read()
	if err != nil {
		return StoredKey{}, false, err
	}
	s, ok := m[id]
	if !ok || s.Key == "" {
		return StoredKey{}, false, nil
	}
	return s, true, nil
}

// Put stores the plaintext key for id.
func (k *MemberKeys) Put(id, key, sha string) error {
	if k == nil {
		return errors.New("member keys store is not open")
	}
	if id == "" || key == "" {
		return errors.New("empty member key")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	m, err := k.read()
	if err != nil {
		return err
	}
	m[id] = StoredKey{Key: key, SHA256: sha}
	return k.write(m)
}

// Delete drops the kept key for id.
func (k *MemberKeys) Delete(id string) error {
	if k == nil || id == "" {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	m, err := k.read()
	if err != nil {
		return err
	}
	if _, ok := m[id]; !ok {
		return nil
	}
	delete(m, id)
	return k.write(m)
}

// Wipe removes every kept key (turning the setting off).
func (k *MemberKeys) Wipe() error {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	err := os.Remove(k.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
