package transport

// ctl:1 holds a relay to the id of its key, and the relay trusts only the
// controller key it was installed with.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// muxPair connects a client and a server mux in memory until t ends.
func muxPair(t *testing.T) (cli, srv *mux.Session) {
	t.Helper()
	cli, srv = mux.NewClient(), mux.NewServer()
	done := make(chan struct{})
	t.Cleanup(func() { close(done); cli.Close(); srv.Close() })
	pump := func(from, to *mux.Session) {
		for {
			select {
			case <-done:
				return
			default:
			}
			if fs := from.Pull(64*1024, 10*time.Millisecond); len(fs) > 0 {
				to.Push(fs)
			}
		}
	}
	go pump(cli, srv)
	go pump(srv, cli)
	return cli, srv
}

func TestRelayEnrolBoundToKeyAndPin(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	old := FleetCtl
	FleetCtl = NewFleet(priv, &config.ServerConfig{Relays: []config.RelayEntry{{ID: "ru1"}, {ID: "de1"}}})
	t.Cleanup(func() { FleetCtl = old })

	run := func(asID string, pin []byte) (error, *relayRuntime) {
		cli, srv := muxPair(t)
		srv.SetRelay("ru1") // the trunk's key was issued for ru1
		go func() {
			for {
				st, err := srv.Accept()
				if err != nil {
					return
				}
				go FleetCtl.ServeCtl(st)
			}
		}()
		rt := &relayRuntime{started: time.Now()}
		beat := uint32(50)
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		return relayCtlSession(ctx, cli, wire.EnrolReq{ID: asID, URL: "https://r.example"}, rt, &beat, &relayPin{base: pin, cur: pin}, ""), rt
	}

	var ee enrolError
	if err, _ := run("de1", pub); !errors.As(err, &ee) {
		t.Fatalf("ru1 enrolled as de1: %v", err)
	}
	if err, _ := run("ru1", other); !errors.As(err, &ee) {
		t.Fatalf("slice accepted under a foreign controller key: %v", err)
	}
	if err, _ := run("ru1", pub); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ru1 with the right key: %v", err)
	}
}

// TestRelayFollowsKeyRoll: the controller signs with a new key and publishes
// a KeyRoll from the old one; a relay that pins the old key takes the roll
// and then the slice, and saves the new key for the next start.
func TestRelayFollowsKeyRoll(t *testing.T) {
	oldPub, oldPriv, _ := ed25519.GenerateKey(rand.Reader)
	newPub, newPriv, _ := ed25519.GenerateKey(rand.Reader)
	prev := FleetCtl
	FleetCtl = NewFleet(newPriv, &config.ServerConfig{Relays: []config.RelayEntry{{ID: "ru1"}}})
	t.Cleanup(func() { FleetCtl = prev })
	roll := ecrypto.SignKeyRoll(oldPriv, newPub, 1, time.Now().Add(time.Hour))
	FleetCtl.SetKeyRoll(roll.Encode())

	cli, srv := muxPair(t)
	srv.SetRelay("ru1")
	go func() {
		for {
			st, err := srv.Accept()
			if err != nil {
				return
			}
			go FleetCtl.ServeCtl(st)
		}
	}()
	pin := &relayPin{base: oldPub, cur: oldPub, path: filepath.Join(t.TempDir(), "controller.pub")}
	beat := uint32(50)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := relayCtlSession(ctx, cli, wire.EnrolReq{ID: "ru1", URL: "https://r.example"}, &relayRuntime{started: time.Now()}, &beat, pin, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("relay refused the rolled controller: %v", err)
	}
	if string(pin.key()) != string(newPub) {
		t.Fatal("pin did not move to the new key")
	}
	again := &relayPin{base: oldPub, cur: oldPub, path: pin.path}
	again.load()
	if string(again.key()) != string(newPub) {
		t.Fatal("rolled key not kept for the next start")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	fresh := &relayPin{base: other, cur: other, path: pin.path}
	fresh.load()
	if string(fresh.key()) != string(other) {
		t.Fatal("saved key used although node.conf names another controller")
	}
}

// TestStandbyGetsState: a relay listed as replica gets the controller's
// files under its standby dir; an ordinary relay gets nothing; a name with
// a path in it is not written.
func TestStandbyGetsState(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	prev, prevState := FleetCtl, StateFn
	FleetCtl = NewFleet(priv, &config.ServerConfig{Relays: []config.RelayEntry{{ID: "r2", Replica: true}, {ID: "ru1"}}})
	StateFn = func() map[string][]byte {
		return map[string][]byte{"members.keys": []byte("alice: lt_a\n"), "controller.seed": []byte("seed"), "../escape": []byte("x")}
	}
	t.Cleanup(func() { FleetCtl, StateFn = prev, prevState })

	// run keeps the relay session up for a moment and returns its standby
	// dir and, from while it was up, when the controller says the state went.
	run := func(id string) (string, time.Time) {
		cli, srv := muxPair(t)
		srv.SetRelay(id)
		go func() {
			for {
				st, err := srv.Accept()
				if err != nil {
					return
				}
				go FleetCtl.ServeCtl(st)
			}
		}()
		dir := filepath.Join(t.TempDir(), "standby")
		beat := uint32(50)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			_ = relayCtlSession(ctx, cli, wire.EnrolReq{ID: id, URL: "https://r.example"}, &relayRuntime{started: time.Now()}, &beat, &relayPin{base: pub, cur: pub}, dir)
			close(done)
		}()
		time.Sleep(300 * time.Millisecond)
		var at time.Time
		for _, r := range FleetCtl.Live() {
			if r.ID == id {
				at = r.StateAt
			}
		}
		cancel()
		<-done
		return dir, at
	}
	dir, at := run("r2")
	if b, err := os.ReadFile(filepath.Join(dir, "members.keys")); err != nil || string(b) != "alice: lt_a\n" {
		t.Fatalf("standby state: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
		t.Fatal("a name with a path escaped the standby dir")
	}
	if at.IsZero() {
		t.Fatal("controller does not record when the standby got the state")
	}
	if dir, _ := run("ru1"); dirExists(dir) {
		t.Fatal("an ordinary relay got the controller's state")
	}
}

func dirExists(p string) bool { _, err := os.Stat(p); return err == nil }

// TestDrainCutsTrunk: draining a relay closes its whole trunk, so clients
// through it are cut, not only its control stream.
func TestDrainCutsTrunk(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	prev := FleetCtl
	FleetCtl = NewFleet(priv, &config.ServerConfig{Relays: []config.RelayEntry{{ID: "ru1"}}})
	t.Cleanup(func() { FleetCtl = prev })
	cli, srv := muxPair(t)
	srv.SetRelay("ru1")
	go func() {
		for {
			st, err := srv.Accept()
			if err != nil {
				return
			}
			go FleetCtl.ServeCtl(st)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	beat := uint32(50)
	go relayCtlSession(ctx, cli, wire.EnrolReq{ID: "ru1", URL: "https://r.example"}, &relayRuntime{started: time.Now()}, &beat, &relayPin{base: pub, cur: pub}, "")
	for i := 0; i < 100 && len(FleetCtl.Live()) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if len(FleetCtl.Live()) == 0 {
		t.Fatal("relay did not enrol")
	}
	FleetCtl.SetRelays([]config.RelayEntry{{ID: "ru1", Drain: true}})
	for i := 0; i < 100 && !srv.Closed(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.Closed() {
		t.Fatal("drained relay's trunk is still open")
	}
}
