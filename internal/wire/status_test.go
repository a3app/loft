package wire

// Hello/Status codecs, optional tails, and status code names.

import "testing"

// TestHelloStatusRoundTrip encodes then decodes Hello and a full Status.
func TestHelloStatusRoundTrip(t *testing.T) {
	h := Hello{Version: 1, MemberID: "alice", InstanceID: "phone", Min: ProtoMin, Max: Proto, Caps: NodeCaps, Program: "loft", Software: "0.1.0"}
	got, err := DecodeHello(h.Encode())
	if err != nil || got != h {
		t.Fatalf("%+v %v", got, err)
	}
	s := Status{
		Code: AuthLimit, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "phone",
		MTU: 1280, VLAN: 10, LeaseSec: 45, KeepaliveMS: 15000, IdleMS: 120000,
		ReMinMS: 200, ReMaxMS: 8000, ReFastN: 5, ReFastForMS: 8000,
		Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		Program: "loft-node", Software: "0.1.0",
		Ticket:      []byte{1, 2, 3},
		Decoy:       []string{"www.example.com:443", "origin.example:443"},
		MaskPub:     []byte{9, 8, 7, 6},
		Replicas:    []string{"203.0.113.10:443"},
		CanaryFlags: 3,
		CanaryPad:   128,
		GapHoldMS:   8000,
		Roster:      []byte{9, 9, 9},
		ListsSerial: 7,
		ListsHash:   make([]byte, 32),
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != s.Code || out.IP != s.IP || out.Program != s.Program || out.Software != s.Software {
		t.Fatalf("%+v", out)
	}
	if string(out.Ticket) != string(s.Ticket) {
		t.Fatalf("ticket %+v", out.Ticket)
	}
	if len(out.Decoy) != 2 || out.Decoy[0] != s.Decoy[0] || out.Decoy[1] != s.Decoy[1] {
		t.Fatalf("decoy %+v", out.Decoy)
	}
	if string(out.MaskPub) != string(s.MaskPub) {
		t.Fatalf("mask %+v", out.MaskPub)
	}
	if len(out.Replicas) != 1 || out.Replicas[0] != s.Replicas[0] || out.CanaryFlags != 3 || out.CanaryPad != 128 || out.GapHoldMS != 8000 {
		t.Fatalf("replicas/canary/gap %+v", out)
	}
	if string(out.Roster) != string(s.Roster) {
		t.Fatalf("roster %+v", out.Roster)
	}
	if out.ListsSerial != 7 || len(out.ListsHash) != 32 {
		t.Fatalf("lists %+v", out)
	}
}

// TestStatusDNSRoundTrip preserves resolver IPs on Status.
func TestStatusDNSRoundTrip(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		DNS: []string{"10.8.0.1", "1.1.1.1"},
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.DNS) != 2 || out.DNS[0] != "10.8.0.1" || out.DNS[1] != "1.1.1.1" {
		t.Fatalf("%+v", out.DNS)
	}
}

// TestStatusIPv6OptionalTail omits ip6/gateway6 on IPv4-only blobs.
func TestStatusIPv6OptionalTail(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		IP6: "fd00::2", Gateway6: "fd00::1",
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.IP6 != "fd00::2" || out.Gateway6 != "fd00::1" {
		t.Fatalf("%q %q", out.IP6, out.Gateway6)
	}
	old := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps}
	got, err := DecodeStatus(old.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.IP6 != "" || got.Gateway6 != "" {
		t.Fatalf("old blob %q %q", got.IP6, got.Gateway6)
	}
}

// TestStatusOldBlobHasNoDNS ignores a truncated tail instead of failing.
func TestStatusOldBlobHasNoDNS(t *testing.T) {
	s := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto}
	b := s.Encode()
	if len(b) < 1 {
		t.Fatal("short")
	}
	old := b[:len(b)-1] // drop DNS count
	out, err := DecodeStatus(old)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.DNS) != 0 {
		t.Fatalf("%+v", out.DNS)
	}
}

// TestStatusOldBlobHasNoDecoy still decodes when later fields are missing.
func TestStatusOldBlobHasNoDecoy(t *testing.T) {
	s := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto}
	b := s.Encode()
	// drop trailing roster length (u16)
	if len(b) < 2 {
		t.Fatal("short")
	}
	old := b[:len(b)-2]
	out, err := DecodeStatus(old)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Replicas) != 0 || out.CanaryFlags != 0 {
		t.Fatalf("%+v", out)
	}
}

// TestHelloOldBlobStillDecodes accepts a Hello without proto/caps.
func TestHelloOldBlobStillDecodes(t *testing.T) {
	old := Hello{Version: 1, MemberID: "alice", InstanceID: "phone"}
	m, i := []byte(old.MemberID), []byte(old.InstanceID)
	b := make([]byte, 3+len(m)+len(i))
	b[0], b[1] = 1, byte(len(m))
	copy(b[2:], m)
	b[2+len(m)] = byte(len(i))
	copy(b[3+len(m):], i)
	got, err := DecodeHello(b)
	if err != nil || got.MemberID != "alice" || got.Max != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}

// TestCodeNames checks String and OK() for assigned codes.
func TestCodeNames(t *testing.T) {
	if AuthAlreadyOnline.String() != "auth_already_online" {
		t.Fatal(AuthAlreadyOnline)
	}
	if OK.OK() == false || AuthLimit.OK() {
		t.Fatal("ok")
	}
}
