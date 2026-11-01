package wire

import (
	"testing"
	"time"
)

func TestWitnessSignedAndDated(t *testing.T) {
	now := time.Now()
	b := EncodeWitness(Witness{ID: "ru1", At: now.Unix(), Sees: false}, []byte("lt_ru1"))
	if id, err := WitnessID(b); err != nil || id != "ru1" {
		t.Fatalf("id %q %v", id, err)
	}
	w, err := DecodeWitness(b, []byte("lt_ru1"), now, 30*time.Second)
	if err != nil || w.ID != "ru1" || w.Sees {
		t.Fatalf("%+v %v", w, err)
	}
	if _, err := DecodeWitness(b, []byte("lt_other"), now, 30*time.Second); err == nil {
		t.Fatal("report under a foreign key accepted")
	}
	if _, err := DecodeWitness(b, []byte("lt_ru1"), now.Add(time.Minute), 30*time.Second); err == nil {
		t.Fatal("old report accepted")
	}
	b[len(b)-17] ^= 1 // flip "sees"
	if _, err := DecodeWitness(b, []byte("lt_ru1"), now, 30*time.Second); err == nil {
		t.Fatal("tampered report accepted")
	}
}
