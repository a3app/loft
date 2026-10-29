package web

import (
	"testing"
	"time"
)

// TestTrackConns checks that track turns the node's per-device report into one
// memberConn per attached device, under the right member, with its fields kept.
func TestTrackConns(t *testing.T) {
	s := &Server{}
	now := time.Now()
	var st nodeStatus
	st.At = now
	st.Members = []struct {
		Member string    `json:"member"`
		IP     string    `json:"ip"`
		Inst   string    `json:"inst"`
		From   string    `json:"from"`
		Front  string    `json:"front,omitempty"`
		Since  time.Time `json:"since"`
		Rx     uint64    `json:"rx,omitempty"`
		Tx     uint64    `json:"tx,omitempty"`
		Key    string    `json:"key,omitempty"`
	}{
		{Member: "alice", Inst: "7f3ac21e", IP: "10.10.0.14", From: "203.0.113.24", Front: "get", Since: now.Add(-2 * time.Hour), Rx: 1200, Tx: 300, Key: "current"},
		{Member: "alice", Inst: "5e209ad1", IP: "10.10.0.15", From: "198.51.100.9", Front: "h2", Since: now.Add(-10 * time.Minute), Rx: 40, Tx: 20, Key: "current"},
		{Member: "bob", Inst: "b7c41f08", IP: "10.20.0.21", From: "198.51.100.31", Front: "ws", Since: now.Add(-1 * time.Minute), Rx: 5, Tx: 5, Key: "old"},
	}

	out := s.track(st)

	if n := len(out["alice"].Conns); n != 2 {
		t.Fatalf("alice: want 2 conns, got %d", n)
	}
	if out["alice"].Online != 2 || out["alice"].Devices != 2 {
		t.Fatalf("alice: want online=2 devices=2, got online=%d devices=%d", out["alice"].Online, out["alice"].Devices)
	}
	c0 := out["alice"].Conns[0]
	if c0.Inst != "7f3ac21e" || c0.IP != "10.10.0.14" || c0.From != "203.0.113.24" || c0.Front != "get" {
		t.Fatalf("alice conn[0] fields wrong: %+v", c0)
	}
	if c0.Rx != 1200 || c0.Tx != 300 || c0.Since.IsZero() {
		t.Fatalf("alice conn[0] traffic/since wrong: %+v", c0)
	}
	if n := len(out["bob"].Conns); n != 1 {
		t.Fatalf("bob: want 1 conn, got %d", n)
	}
	if out["bob"].Conns[0].Key != "old" {
		t.Fatalf("bob conn key: want old, got %q", out["bob"].Conns[0].Key)
	}
}

// TestTrackStaleNoConns checks that a status file older than a minute (the node
// is probably stopped) yields no live devices, so the Connections tab is empty.
func TestTrackStaleNoConns(t *testing.T) {
	s := &Server{}
	var st nodeStatus
	st.At = time.Now().Add(-2 * time.Minute)
	st.Members = []struct {
		Member string    `json:"member"`
		IP     string    `json:"ip"`
		Inst   string    `json:"inst"`
		From   string    `json:"from"`
		Front  string    `json:"front,omitempty"`
		Since  time.Time `json:"since"`
		Rx     uint64    `json:"rx,omitempty"`
		Tx     uint64    `json:"tx,omitempty"`
		Key    string    `json:"key,omitempty"`
	}{
		{Member: "alice", Inst: "7f3ac21e", IP: "10.10.0.14", Front: "get"},
	}
	out := s.track(st)
	if len(out["alice"].Conns) != 0 || out["alice"].Online != 0 {
		t.Fatalf("stale status should list no live devices, got %+v", out["alice"])
	}
}
