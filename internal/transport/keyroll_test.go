package transport

// A client that pins the old controller key follows a KeyRoll to the new
// one, keeps it across a restart, and ignores rolls it should not trust.

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

func TestClientFollowsKeyRoll(t *testing.T) {
	oldPub, oldPriv, _ := ed25519.GenerateKey(rand.Reader)
	newPub, newPriv, _ := ed25519.GenerateKey(rand.Reader)
	strangerPub, strangerPriv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	SetInstanceFile(filepath.Join(dir, "client.instance"))
	t.Cleanup(func() { SetInstanceFile(""); resetRosterForTest() })
	resetRosterForTest()
	PinControllerPub(oldPub)

	underNew := ecrypto.SignRoster(newPriv, wire.Roster{Serial: 5})
	if RememberRoster(underNew.Encode()) {
		t.Fatal("roster under the new key taken before the roll")
	}
	// A roll signed by a key this client does not trust changes nothing.
	if RememberKeyRoll(ecrypto.SignKeyRoll(strangerPriv, newPub, 9, time.Now().Add(time.Hour)).Encode()) {
		t.Fatal("roll from an untrusted key applied")
	}
	// An expired roll changes nothing.
	if RememberKeyRoll(ecrypto.SignKeyRoll(oldPriv, newPub, 1, time.Now().Add(-time.Minute)).Encode()) {
		t.Fatal("expired roll applied")
	}
	roll := ecrypto.SignKeyRoll(oldPriv, newPub, 1, time.Now().Add(time.Hour))
	if !RememberKeyRoll(roll.Encode()) {
		t.Fatal("valid roll refused")
	}
	if string(PinnedControllerPub()) != string(newPub) {
		t.Fatal("pin did not move")
	}
	if !RememberRoster(underNew.Encode()) {
		t.Fatal("roster under the new key refused after the roll")
	}
	underOld := ecrypto.SignRoster(oldPriv, wire.Roster{Serial: 6})
	if RememberRoster(underOld.Encode()) {
		t.Fatal("roster under the old key still taken")
	}
	// The same roll again (or a replayed older one) cannot move the pin back.
	if RememberKeyRoll(roll.Encode()) {
		t.Fatal("roll applied twice")
	}
	_ = strangerPub

	// After a restart with the old controller_pub in the config, the stored
	// roll brings the client to the new key again.
	resetRosterForTest()
	PinControllerPub(oldPub)
	replayStoredRolls()
	if string(PinnedControllerPub()) != string(newPub) {
		t.Fatal("stored roll not replayed at start")
	}
}
