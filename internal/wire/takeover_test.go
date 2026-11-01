package wire

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestTakeoverRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	tk := Takeover{Epoch: 2, At: 1700000000, By: "r2"}
	tk.Sign(priv)
	got, err := DecodeTakeover(tk.Encode())
	if err != nil || got.Epoch != 2 || got.At != 1700000000 || got.By != "r2" {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Verify(pub) != nil || got.Verify(other) == nil {
		t.Fatal("verify")
	}
	got.Epoch = 3
	if got.Verify(pub) == nil {
		t.Fatal("a changed epoch still verifies")
	}
	if _, err := DecodeTakeover(tk.Encode()[1:]); err == nil {
		t.Fatal("bad form decoded")
	}
}
