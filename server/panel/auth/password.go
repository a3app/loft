// Package auth is panel sign-in: argon2id password hashes, sessions, and a
// per-address limit on failed logins.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// MinPassword is the shortest password the panel accepts.
const MinPassword = 9

// Argon2id parameters (RFC 9106 second recommendation, 64 MiB).
const (
	argTime    = 3
	argMemory  = 64 * 1024
	argThreads = 2
	argKeyLen  = 32
	argSaltLen = 16
)

// ErrShort is a password below MinPassword runes.
var ErrShort = fmt.Errorf("password: at least %d characters", MinPassword)

// Hash returns a PHC string: $argon2id$v=19$m=…,t=…,p=…$salt$key.
func Hash(pw string) (string, error) {
	if len([]rune(pw)) < MinPassword {
		return "", ErrShort
	}
	salt := make([]byte, argSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argTime, argMemory, argThreads, argKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argMemory, argTime, argThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether pw matches a Hash string. Parameters come from the
// string, so hashes made with older settings keep working.
func Verify(phc, pw string) bool {
	m, t, p, salt, key, err := parsePHC(phc)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1
}

// parsePHC splits $argon2id$v=19$m=,t=,p=$salt$key.
func parsePHC(s string) (m, t uint32, p uint8, salt, key []byte, err error) {
	f := strings.Split(s, "$")
	if len(f) != 6 || f[1] != "argon2id" {
		return 0, 0, 0, nil, nil, errors.New("hash: not argon2id")
	}
	var v int
	if _, err = fmt.Sscanf(f[2], "v=%d", &v); err != nil || v != argon2.Version {
		return 0, 0, 0, nil, nil, errors.New("hash: bad version")
	}
	var pp uint32
	if _, err = fmt.Sscanf(f[3], "m=%d,t=%d,p=%d", &m, &t, &pp); err != nil || m == 0 || t == 0 || pp == 0 || pp > 255 {
		return 0, 0, 0, nil, nil, errors.New("hash: bad parameters")
	}
	b64 := base64.RawStdEncoding
	if salt, err = b64.DecodeString(f[4]); err != nil {
		return 0, 0, 0, nil, nil, err
	}
	if key, err = b64.DecodeString(f[5]); err != nil || len(key) == 0 {
		return 0, 0, 0, nil, nil, errors.New("hash: bad key")
	}
	return m, t, uint8(pp), salt, key, nil
}
