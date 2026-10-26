package wire

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func signedRoll(t *testing.T, serial uint64, notAfter time.Time) (KeyRoll, ed25519.PrivateKey) {
	t.Helper()
	_, oldPriv, _ := ed25519.GenerateKey(rand.Reader)
	newPub, _, _ := ed25519.GenerateKey(rand.Reader)
	k := KeyRoll{Serial: serial, NotAfter: notAfter.Unix(), Old: oldPriv.Public().(ed25519.PublicKey), New: newPub}
	k.Sig = ed25519.Sign(oldPriv, k.Body())
	return k, oldPriv
}

func TestKeyRollRoundTripAndVerify(t *testing.T) {
	k, _ := signedRoll(t, 3, time.Now().Add(time.Hour))
	got, err := DecodeKeyRoll(k.Encode())
	if err != nil || got.Serial != 3 || string(got.New) != string(k.New) {
		t.Fatalf("%+v %v", got, err)
	}
	if err := got.Verify(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got.Verify(time.Now().Add(2*time.Hour)) == nil {
		t.Fatal("expired roll accepted")
	}
	bad := got
	bad.New = append([]byte(nil), got.New...)
	bad.New[0] ^= 1
	if bad.Verify(time.Now()) == nil {
		t.Fatal("tampered roll accepted")
	}
	if _, err := DecodeKeyRoll(k.Encode()[1:]); err == nil {
		t.Fatal("short roll decoded")
	}
}

// TestStatusCarriesKeyRoll: the roll rides after inner, and a Status that
// negotiated no cipher still says ChaCha20-Poly1305 there.
func TestStatusCarriesKeyRoll(t *testing.T) {
	k, _ := signedRoll(t, 1, time.Now().Add(time.Hour))
	st := Status{Code: OK, IP: "10.8.0.2", KeyRoll: k.Encode()}
	got, err := DecodeStatus(st.Encode())
	if err != nil || string(got.KeyRoll) != string(k.Encode()) {
		t.Fatalf("roll lost: %v", err)
	}
	if !got.InnerSet || got.Inner != InnerChaCha {
		t.Fatalf("inner %v %d", got.InnerSet, got.Inner)
	}
	plain, err := DecodeStatus(Status{Code: OK, IP: "10.8.0.2"}.Encode())
	if err != nil || len(plain.KeyRoll) != 0 || plain.InnerSet {
		t.Fatalf("status without roll changed: %+v %v", plain, err)
	}
}

func TestManifestCarriesKeyRoll(t *testing.T) {
	k, _ := signedRoll(t, 1, time.Now().Add(time.Hour))
	b, err := Roster{Serial: 1}.ManifestJSONRoll(k.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if string(ManifestRoll(b)) != string(k.Encode()) {
		t.Fatal("manifest roll lost")
	}
	if _, err := ParseManifest(b); err != nil {
		t.Fatal(err)
	}
}
