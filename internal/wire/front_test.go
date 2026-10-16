package wire

// Front name parsing, intersection order, and cap masks.

import "testing"

// TestParseFrontsDefaultAll treats a nil list as FrontOrder.
func TestParseFrontsDefaultAll(t *testing.T) {
	got, err := ParseFronts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(FrontOrder) {
		t.Fatalf("%v", got)
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
	if c.Has(CapGET) || !c.Has(CapWS) || !c.Has(CapTicket) {
		t.Fatalf("%v", c.Names())
	}
	if got := c.Fronts(); len(got) != 1 || got[0] != "ws" {
		t.Fatalf("%v", got)
	}
}
