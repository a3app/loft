package wire

// Protocol Negotiate, cap names, and ProtoString.

import "testing"

// TestNegotiate picks the highest common version and rejects an empty intersection.
func TestNegotiate(t *testing.T) {
	sel, err := Negotiate(Offer{Min: 0x0100, Max: 0x0102, Caps: CapMulti}, NodeOffer())
	if err != nil || sel != Proto {
		t.Fatalf("sel=%#x err=%v", sel, err)
	}
	_, err = Negotiate(Offer{Min: 0x0200, Max: 0x0200}, NodeOffer())
	if err == nil {
		t.Fatal("expected protocol mismatch")
	}
	if e, ok := err.(Error); !ok || e.Code != Protocol {
		t.Fatalf("want protocol, got %v", err)
	}
	sel, err = Negotiate(Offer{}, NodeOffer())
	if err != nil || sel != ProtoMin {
		t.Fatalf("old client %#x %v", sel, err)
	}
}

// TestCapNames checks NodeCaps bits and Names order.
func TestCapNames(t *testing.T) {
	if !NodeCaps.Has(CapGET) || !NodeCaps.Has(CapP2P) || !NodeCaps.Has(CapDNS) || !NodeCaps.Has(CapConnect) || !NodeCaps.Has(CapResin) || !NodeCaps.Has(CapHold) || !NodeCaps.Has(CapL3) || NodeCaps.Has(1<<28) {
		t.Fatalf("%v", NodeCaps.Names())
	}
	// Status still advertises trunks; default Hello does not (audit CapRelay).
	if !NodeCaps.Has(CapRelay) || MemberCaps.Has(CapRelay) || !MemberCaps.Has(CapHold) {
		t.Fatalf("status=%v member=%v", NodeCaps.Names(), MemberCaps.Names())
	}
	names := (CapMulti | CapResume).Names()
	if len(names) != 2 || names[0] != "multi" {
		t.Fatalf("%v", names)
	}
}

// TestProtoString formats 0x0100 as "1.0".
func TestProtoString(t *testing.T) {
	if ProtoString(0x0100) != "1.0" || ProtoString(0x0203) != "2.3" {
		t.Fatal(ProtoString(Proto))
	}
}
