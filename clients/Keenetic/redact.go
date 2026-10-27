package keenetic

import (
	"io"
	"regexp"
)

var (
	reToken  = regexp.MustCompile(`lt_[A-Za-z0-9_-]{4,}`)
	reUUID   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reUser   = regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`)
	reUserAt = regexp.MustCompile(`://[^/\s:@]+@`)
)

// Redact shortens member keys, UUIDs and URL userinfo before they reach
// the journal or the log file. Trojan puts the password in userinfo with
// no colon (`trojan://secret@host`); that form is reUserAt.
func Redact(s string) string {
	s = reUser.ReplaceAllString(s, "://••••@")
	s = reUserAt.ReplaceAllString(s, "://••••@")
	s = reToken.ReplaceAllString(s, "lt_…")
	s = reUUID.ReplaceAllString(s, "••••")
	return s
}

type redactWriter struct{ w io.Writer }

func (r redactWriter) Write(p []byte) (int, error) {
	s := Redact(string(p))
	_, err := io.WriteString(r.w, s)
	return len(p), err
}
