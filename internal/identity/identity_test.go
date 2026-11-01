package identity

// Member-key mint, SHA-256 match, and prefix.

import "testing"

// TestKeyRoundTrip mints lt_…, matches its SHA-256, and rejects a wrong key.
func TestKeyRoundTrip(t *testing.T) {
	plain, sha, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if plain[:3] != "lt_" {
		t.Fatalf("prefix: %s", plain)
	}
	if !Match(plain, sha) {
		t.Fatal("match")
	}
	if Match("lt_nope", sha) {
		t.Fatal("false positive")
	}
}
