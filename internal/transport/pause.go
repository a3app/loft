package transport

// Client-local draft pause. Not on the wire. Resin (Open/DNS/Leave) still goes.
//
// Mux frames come in two kinds. Resin frames (Open, Ping, Leave, Close, DNS
// datagrams and other control) must arrive: on GET they are resent until
// the node reports it applied them. Draft frames (Data, Pad, other
// datagrams) are sent once; if they are lost, inner TCP or the app sends
// again. On a link with a known regular outage, sending draft into the
// outage only wastes it, so the client holds draft back for those moments
// and lets resin through.

import (
	"sync/atomic"
	"time"
)

// pauseSchedule is set from client.yaml `pause:` (starlink, or empty).
// The pump reads it while SuperviseOn may replace it.
var pauseSchedule atomic.Value // string

func init() { pauseSchedule.Store("") }

// SetPauseSchedule installs a draft-pause calendar. Unknown names are off.
func SetPauseSchedule(name string) { pauseSchedule.Store(name) }

// DraftPaused is true when the client must not send mux Data/ordinary UDP.
func DraftPaused() bool {
	return draftPausedAt(time.Now().UTC())
}

func draftPausedAt(now time.Time) bool {
	name, _ := pauseSchedule.Load().(string)
	switch name {
	case "starlink":
		// Starlink GSL handover: :12 :27 :42 :57 of each UTC minute, ~400ms.
		// GSL is the ground-to-satellite link; it switches satellites on
		// that fixed schedule and drops packets while it does.
		ms := now.Second()*1000 + now.Nanosecond()/1e6
		for _, t := range []int{12_000, 27_000, 42_000, 57_000} {
			if ms >= t && ms < t+400 {
				return true
			}
		}
	}
	return false
}
