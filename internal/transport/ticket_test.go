package transport

// Resume ticket cache across Supervise Start.

import "testing"

// resetTicket clears the process-wide ticket so tests do not leak.
func resetTicket(t *testing.T) {
	t.Helper()
	ticketMu.Lock()
	heldTicket = nil
	ticketMu.Unlock()
	t.Cleanup(func() {
		ticketMu.Lock()
		heldTicket = nil
		ticketMu.Unlock()
	})
}

// TestTicketCacheRoundTrip stores a Status ticket and returns a copy.
func TestTicketCacheRoundTrip(t *testing.T) {
	resetTicket(t)
	if CachedTicket() != nil {
		t.Fatal("empty")
	}
	RememberTicket([]byte{1, 2, 3})
	got := CachedTicket()
	if string(got) != "\x01\x02\x03" {
		t.Fatalf("%q", got)
	}
	got[0] = 9
	if CachedTicket()[0] != 1 {
		t.Fatal("alias")
	}
	RememberTicket(nil)
	if CachedTicket() != nil {
		t.Fatal("clear")
	}
}
