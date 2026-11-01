package egress

import "testing"

// TestDirectFor: a ping may leave only when the member's route is direct;
// a block rule or a channel stops it.
func TestDirectFor(t *testing.T) {
	c, err := Parse("channel px socks5 127.0.0.1:1 fail block\nrule member bob via block\nrule member carol via px\ndefault direct\n")
	if err != nil {
		t.Fatal(err)
	}
	r := New(c, Options{SysNet: t.TempDir()})
	defer r.Close()
	for who, want := range map[string]bool{"alice": true, "bob": false, "carol": false} {
		if got := r.DirectFor(Who{Member: who}, "1.1.1.1"); got != want {
			t.Fatalf("%s: DirectFor %v, want %v", who, got, want)
		}
	}
	c2, _ := Parse("default block\n")
	r2 := New(c2, Options{SysNet: t.TempDir()})
	defer r2.Close()
	if r2.DirectFor(Who{Member: "alice"}, "1.1.1.1") {
		t.Fatal("default block allowed a ping")
	}
}
