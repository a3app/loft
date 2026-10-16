package transport

// Client overlay instance id: persisted locally, stamped on Hello, stored from Status.

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// instID is the overlay instance; instFile is the 0600 path (empty = memory only).
var (
	instMu   sync.Mutex
	instID   string
	instFile string
)

// SetInstanceFile loads a persisted overlay instance id from path (0600).
// Empty path: in-memory only.
func SetInstanceFile(path string) {
	instMu.Lock()
	defer instMu.Unlock()
	instFile = path
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		instID = s
	}
}

// ClientInstance returns the stored overlay instance id, or empty if none.
func ClientInstance() string {
	instMu.Lock()
	defer instMu.Unlock()
	return instID
}

// RememberInstance stores the overlay instance from Status and writes the file.
func RememberInstance(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	instMu.Lock()
	defer instMu.Unlock()
	if instID == id {
		return
	}
	instID = id
	if instFile != "" {
		_ = os.WriteFile(instFile, []byte(id+"\n"), 0o600)
	}
}

// instanceDir is the folder of the instance file (GUI sessions/), or empty.
func instanceDir() string {
	instMu.Lock()
	defer instMu.Unlock()
	if instFile == "" {
		return ""
	}
	return filepath.Dir(instFile)
}

// EnsureInstance makes a stable id before the first Hello if none is stored.
func EnsureInstance() string {
	instMu.Lock()
	defer instMu.Unlock()
	if instID != "" {
		return instID
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	instID = hex.EncodeToString(b[:])
	if instFile != "" {
		_ = os.WriteFile(instFile, []byte(instID+"\n"), 0o600)
	}
	return instID
}
