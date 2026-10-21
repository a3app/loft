package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one change made through the panel or its API.
type Entry struct {
	At     time.Time `json:"at"`
	User   string    `json:"user"`             // login, or "system"
	Op     string    `json:"op"`               // the route, e.g. "POST /api/network/apply"
	Target string    `json:"target,omitempty"` // what it was about (a member, a channel, a user)
	From   string    `json:"from,omitempty"`   // client address, or "token"
}

// Audit is audit.jsonl under the data directory, newest last.
type Audit struct {
	path string
	mu   sync.Mutex
}

// auditMax is the file size at which the older half is dropped.
const auditMax = 2 << 20

// OpenAudit returns dir/audit.jsonl.
func OpenAudit(dir string) *Audit { return &Audit{path: filepath.Join(dir, "audit.jsonl")} }

// Add appends e.
func (a *Audit) Add(e Entry) error {
	if a == nil {
		return nil
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	b, _ := json.Marshal(e)
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	st, _ := f.Stat()
	f.Close()
	if err == nil && st != nil && st.Size() > auditMax {
		if all, rerr := os.ReadFile(a.path); rerr == nil {
			half := all[len(all)/2:]
			if i := bytes.IndexByte(half, '\n'); i >= 0 {
				WriteAtomic(a.path, half[i+1:], 0o600)
			}
		}
	}
	return err
}

// List returns up to n entries, newest first, of user (all when empty).
func (a *Audit) List(user string, n int) []Entry {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	b, err := os.ReadFile(a.path)
	a.mu.Unlock()
	if err != nil {
		return []Entry{}
	}
	var all []Entry
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && (user == "" || e.User == user) {
			all = append(all, e)
		}
	}
	out := []Entry{}
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out
}
