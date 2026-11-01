package wire

// Fleet slice signature and enrol/beat codecs.

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/a3app/loft/internal/testcfg"
)

// TestSliceSignRoundtrip signs, encodes, decodes, and verifies a Slice.
func TestSliceSignRoundtrip(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := Slice{
		Serial:  4,
		ID:      "ru1",
		URL:     "https://203.0.113.10",
		Decoy:   testcfg.DecoyAddr(),
		Fronts:  []string{"get", "h2"},
		Weight:  80,
		BeatMS:  10000,
		Expires: time.Now().Add(time.Hour).Unix(),
	}
	s.Pub = priv.Public().(ed25519.PublicKey)
	s.Sig = ed25519.Sign(priv, s.Body())
	got, err := DecodeSlice(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Verify(); err != nil {
		t.Fatal(err)
	}
	if got.ID != "ru1" || got.Weight != 80 || got.BeatMS != 10000 {
		t.Fatalf("%+v", got)
	}
}

// TestEnrolBeatCodec round-trips EnrolReq, Beat, and EncodeFlt/DecodeFlt.
func TestEnrolBeatCodec(t *testing.T) {
	e := EnrolReq{ID: "ru1", URL: "https://a.example", Decoy: testcfg.DecoyAddr(), Fronts: []string{"ws"}}
	got, err := DecodeEnrolReq(e.Encode())
	if err != nil || got.ID != e.ID || got.URL != e.URL || got.Fronts[0] != "ws" {
		t.Fatalf("%+v %v", got, err)
	}
	b := Beat{Load: 12, Conns: 7, Prog: "loft-node", Uptime: 99}
	g, err := DecodeBeat(b.Encode())
	if err != nil || g.Conns != 7 || g.Uptime != 99 || g.Prog != "loft-node" {
		t.Fatalf("%+v %v", g, err)
	}
	kind, body, err := DecodeFlt(EncodeFlt(FltBeat, b.Encode()))
	if err != nil || kind != FltBeat || len(body) == 0 {
		t.Fatal(kind, err)
	}
}
