package wire

// Front name parsing, intersection order, and cap masks.

import "testing"

// TestParseFrontsDefaultGET treats a nil list as GET only.
func TestParseFrontsDefaultGET(t *testing.T) {
	got, err := ParseFronts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != FrontGET {
		t.Fatalf("%v", got)
	}
	all, err := ParseFronts(FrontOrder)
	if err != nil || len(all) != len(FrontOrder) {
		t.Fatalf("explicit all %v %v", all, err)
	}
}

// TestParseFrontsRejectsVendor refuses CDN names; they are not protocol fronts.
func TestParseFrontsRejectsVendor(t *testing.T) {
	if _, err := ParseFronts([]string{"yandex"}); err == nil {
		t.Fatal("expected error")
	}
}

// TestIntersectFronts keeps client order among server-enabled fronts.
func TestIntersectFronts(t *testing.T) {
	got := IntersectFronts([]string{"get", "ws", "h3"}, []string{"ws", "h2", "h3"})
	if len(got) != 2 || got[0] != "ws" || got[1] != "h3" {
		t.Fatalf("%v", got)
	}
}

// TestCapsWithFronts replaces FrontBits and leaves other caps set.
func TestCapsWithFronts(t *testing.T) {
	c := CapsWithFronts(NodeCaps, []string{"ws"})
	if c.Has(CapGET) || !c.Has(CapWS) || !c.Has(CapTicket) || c.Has(CapConnect) {
		t.Fatalf("%v", c.Names())
	}
	if got := c.Fronts(); len(got) != 1 || got[0] != "ws" {
		t.Fatalf("%v", got)
	}
}

// TestParseFrontConnect is the mux-off splice name; it is not in FrontOrder.
func TestParseFrontConnect(t *testing.T) {
	got, err := ParseFronts([]string{"get", "connect"})
	if err != nil || len(got) != 2 || got[1] != FrontConnect {
		t.Fatalf("%v %v", got, err)
	}
	c := CapsWithFronts(NodeCaps, got)
	if !c.Has(CapGET) || !c.Has(CapConnect) || c.Has(CapWS) {
		t.Fatalf("%v", c.Names())
	}
}
