package transport

import (
	"testing"
	"time"
)

func TestStarlinkPauseWindows(t *testing.T) {
	t.Cleanup(func() { SetPauseSchedule("") })
	SetPauseSchedule("starlink")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if draftPausedAt(base.Add(11 * time.Second)) {
		t.Fatal("11s")
	}
	if !draftPausedAt(base.Add(12*time.Second + 100*time.Millisecond)) {
		t.Fatal("12.1s")
	}
	if draftPausedAt(base.Add(12*time.Second + 500*time.Millisecond)) {
		t.Fatal("12.5s")
	}
	if !draftPausedAt(base.Add(57 * time.Second)) {
		t.Fatal("57s")
	}
	SetPauseSchedule("")
	if draftPausedAt(base.Add(12 * time.Second)) {
		t.Fatal("off")
	}
}
