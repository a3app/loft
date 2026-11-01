package protect

import (
	"testing"
	"time"
)

func TestTimeoutWithoutProtector(t *testing.T) {
	Set(nil)
	_ = Bind("")
	d := NetDialer(50 * time.Millisecond)
	if d.Control != nil {
		t.Fatal("control")
	}
}

func TestSetClears(t *testing.T) {
	_ = Bind("")
	Set(func(fd int) bool { return true })
	if NetDialer(time.Second).Control == nil {
		t.Fatal("want control")
	}
	Set(nil)
	if NetDialer(time.Second).Control != nil {
		t.Fatal("cleared")
	}
}

func TestNetDialerResolverFollowsProtector(t *testing.T) {
	Set(nil)
	_ = Bind("")
	if NetDialer(time.Second).Resolver != nil {
		t.Fatal("resolver without protect")
	}
	Set(func(fd int) bool { return true })
	d := NetDialer(time.Second)
	if d.Control == nil || d.Resolver == nil {
		t.Fatal("want control and resolver")
	}
	Set(nil)
	if NetDialer(time.Second).Resolver != nil {
		t.Fatal("resolver after clear")
	}
}

func TestBypassPoisonDNS(t *testing.T) {
	if g := bypassDNS("udp4", "10.8.0.1:53"); g != "1.1.1.1:53" {
		t.Fatalf("overlay dns %s", g)
	}
	if g := bypassDNS("udp4", "127.0.0.53:53"); g != "1.1.1.1:53" {
		t.Fatalf("stub %s", g)
	}
	if g := bypassDNS("udp4", "8.8.8.8:53"); g != "8.8.8.8:53" {
		t.Fatalf("public %s", g)
	}
	if g := bypassDNS("udp6", "[fd00::1]:53"); g != "[2606:4700:4700::1111]:53" {
		t.Fatalf("ula %s", g)
	}
}

func TestLoopbackAddr(t *testing.T) {
	for _, a := range []string{"127.0.0.53:53", "127.0.0.1:1", "[::1]:53"} {
		if !loopbackAddr(a) {
			t.Fatalf("%s", a)
		}
	}
	if loopbackAddr("1.1.1.1:53") || loopbackAddr("hello.loft.cx:443") {
		t.Fatal("public")
	}
}

func TestNetDialerResolverWhenBound(t *testing.T) {
	Set(nil)
	t.Cleanup(func() { _ = Bind("") })
	if err := BindUplink(""); err != nil {
		t.Fatal(err)
	}
	if boundIndex() == 0 {
		t.Skip("no physical uplink")
	}
	d := NetDialer(time.Second)
	if d.Control == nil || d.Resolver == nil {
		t.Fatal("want control and resolver")
	}
}
