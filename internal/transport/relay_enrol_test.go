package transport

// ctl:1 holds a relay to the id of its key, and the relay trusts only the
// controller key it was installed with.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
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
		return relayCtlSession(ctx, cli, wire.EnrolReq{ID: asID, URL: "https://r.example"}, rt, &beat, &relayPin{base: pin, cur: pin}), rt
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
	err := relayCtlSession(ctx, cli, wire.EnrolReq{ID: "ru1", URL: "https://r.example"}, &relayRuntime{started: time.Now()}, &beat, pin)
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
