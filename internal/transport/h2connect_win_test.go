package transport

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// TestH2ClientTakesPeerWindow: the CONNECT client uses the stream window the
// node announces in SETTINGS, not the 64 KiB default; with the default every
// upload over h2 and l3 was capped at 64 KiB per round trip.
func TestH2ClientTakesPeerWindow(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()
	go func() {
		sfr := http2.NewFramer(srv, srv)
		_ = sfr.WriteWindowUpdate(0, 1<<20) // connection window first, as some servers do
		_ = sfr.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: 4 << 20})
		_, _ = sfr.ReadFrame() // the client's SETTINGS ack
	}()
	bw := bufio.NewWriter(cli)
	fr := http2.NewFramer(bw, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sw, cw, err := h2WaitSettingsWin(ctx, cli, fr, bw)
	if err != nil {
		t.Fatal(err)
	}
	if sw != 4<<20 || cw != 65535+1<<20 {
		t.Fatalf("stream window %d, connection window %d", sw, cw)
	}
}

// TestH2SettingsChangeShiftsWindow: a later SETTINGS with a new initial
// window shifts the open stream's window by the difference.
func TestH2SettingsChangeShiftsWindow(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()
	bw := bufio.NewWriter(cli)
	st := &h2stream{c: cli, fr: http2.NewFramer(bw, cli), bw: bw, sid: 1, win: 1000, cwin: 1 << 20, initWin: 65535}
	st.inR, st.inW = io.Pipe()
	got := make(chan h2hs, 1)
	go st.readLoop(got)
	sfr := http2.NewFramer(srv, srv)
	go func() { // drain what the client writes (its SETTINGS ack)
		for {
			if _, err := sfr.ReadFrame(); err != nil {
				return
			}
		}
	}()
	if err := sfr.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: 65535 + 5000}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st.mu.Lock()
		w := st.win
		st.mu.Unlock()
		if w == 6000 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("window %d, want 1000+5000", st.win)
}
