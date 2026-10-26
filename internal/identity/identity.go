// Package identity is the member key: lt_ plus 48 hex characters (24 bytes).
// The node stores SHA-256(key); HMAC/HKDF need the plaintext in members.keys.
//
// A member is one device or person allowed to use the node. Its key is
// both its login and the secret behind the 0x02 handshake proof. Two files
// hold it on the node: network.conf keeps only the hash (safe to show in
// the panel and in backups), and members.keys keeps the plaintext, because
// the node must compute the same HMAC as the client to check the proof.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// NewKey mints a member key: lt_ plus 48 hex characters (24 random bytes).
// Shown once; the node stores SHA-256. This is a device credential, not a user password.
func NewKey() (plain, sha string, err error) {
	var b [24]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", "", err
	}
	plain = "lt_" + hex.EncodeToString(b[:])
	return plain, Sum(plain), nil
}

// Sum is hex(SHA-256(plain)), the form stored in network.conf.
func Sum(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

// Match reports whether plain hashes to sha. Empty either side is false.
func Match(plain, sha string) bool {
	if sha == "" || plain == "" {
		return false
	}
	return Sum(plain) == sha
}

// Short is a log-safe truncation of a hex SHA-256.
func Short(sha string) string {
	if len(sha) < 12 {
		return sha
	}
	return fmt.Sprintf("%s…%s", sha[:6], sha[len(sha)-4:])
}
