package transport

// Resume ticket cache across Supervise redials (Wi-Fi / LTE path change).

import "sync"

var (
	ticketMu   sync.Mutex
	heldTicket []byte
)

// RememberTicket stores a Status resume ticket for the next Start.
func RememberTicket(t []byte) {
	ticketMu.Lock()
	defer ticketMu.Unlock()
	if len(t) == 0 {
		heldTicket = nil
		return
	}
	heldTicket = append([]byte(nil), t...)
}

// CachedTicket is the last Status ticket, or nil.
func CachedTicket() []byte {
	ticketMu.Lock()
	defer ticketMu.Unlock()
	if len(heldTicket) == 0 {
		return nil
	}
	return append([]byte(nil), heldTicket...)
}
