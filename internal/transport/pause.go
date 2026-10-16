package transport

// Client-local draft pause. Not on the wire. Resin (Open/DNS/Leave) still goes.

import "time"

// pauseSchedule is set from client.yaml `pause:` (starlink, or empty).
var pauseSchedule string

// SetPauseSchedule installs a draft-pause calendar. Unknown names are off.
func SetPauseSchedule(name string) { pauseSchedule = name }

// DraftPaused is true when the client must not send mux Data/ordinary UDP.
func DraftPaused() bool {
	return draftPausedAt(time.Now().UTC())
}

func draftPausedAt(now time.Time) bool {
	switch pauseSchedule {
	case "starlink":
		// Starlink GSL handover: :12 :27 :42 :57 of each UTC minute, ~400ms.
		ms := now.Second()*1000 + now.Nanosecond()/1e6
		for _, t := range []int{12_000, 27_000, 42_000, 57_000} {
			if ms >= t && ms < t+400 {
				return true
			}
		}
	}
	return false
}
