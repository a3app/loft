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

// TestStatusRoutesOptionalTail keeps managed CIDRs after GapHoldMS.
func TestStatusRoutesOptionalTail(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		Routes: []Route{{Dst: "192.168.10.0/24", Via: "10.8.0.5"}},
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Routes) != 1 || out.Routes[0].Dst != "192.168.10.0/24" || out.Routes[0].Via != "10.8.0.5" {
		t.Fatalf("%+v", out.Routes)
	}
	old := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps, GapHoldMS: 8000}
	got, err := DecodeStatus(old.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Routes) != 0 || got.GapHoldMS != 8000 {
		t.Fatalf("old %+v gap %d", got.Routes, got.GapHoldMS)
	}
}

// TestStatusPolicyAndTTLOptionalTail keeps compact ACL and live-session max after routes.
func TestStatusPolicyAndTTLOptionalTail(t *testing.T) {
	vlan := uint16(10)
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		Routes: []Route{{Dst: "192.168.10.0/24", Via: "10.8.0.5"}},
		Policy: Policy{
			Present:    true,
			OwnTags:    []string{"ops"},
			ACLDefault: 1,
			Rules: []PolicyRule{{
				Action: 1, SrcTag: "ops", DstTag: "db", Proto: 1, Dport: []uint16{5432}, SrcVLAN: &vlan,
			}},
			Peers: []PeerTags{{IP: "10.8.0.5", Tags: []string{"db"}}},
		},
		SessionTTLMS: 86400000,
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if !out.Policy.Present || out.Policy.ACLDefault != 1 || len(out.Policy.OwnTags) != 1 || out.Policy.OwnTags[0] != "ops" {
		t.Fatalf("policy %+v", out.Policy)
	}
	if len(out.Policy.Rules) != 1 || out.Policy.Rules[0].SrcTag != "ops" || out.Policy.Rules[0].DstTag != "db" || out.Policy.Rules[0].Proto != 1 || len(out.Policy.Rules[0].Dport) != 1 || out.Policy.Rules[0].Dport[0] != 5432 {
		t.Fatalf("rules %+v", out.Policy.Rules)
	}
	if out.Policy.Rules[0].SrcVLAN == nil || *out.Policy.Rules[0].SrcVLAN != 10 {
		t.Fatalf("vlan %+v", out.Policy.Rules[0].SrcVLAN)
	}
	if len(out.Policy.Peers) != 1 || out.Policy.Peers[0].IP != "10.8.0.5" || out.Policy.Peers[0].Tags[0] != "db" {
		t.Fatalf("peers %+v", out.Policy.Peers)
	}
	if out.SessionTTLMS != 86400000 || len(out.Routes) != 1 {
		t.Fatalf("ttl %d routes %+v", out.SessionTTLMS, out.Routes)
	}
	ttlOnly := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps, SessionTTLMS: 5000}
	got, err := DecodeStatus(ttlOnly.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionTTLMS != 5000 || !got.Policy.Present {
		t.Fatalf("ttl-only %+v ttl %d", got.Policy, got.SessionTTLMS)
	}
	old := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps, Routes: []Route{{Dst: "192.168.10.0/24", Via: "10.8.0.5"}}}
	got, err = DecodeStatus(old.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy.Present || got.SessionTTLMS != 0 || len(got.Routes) != 1 {
		t.Fatalf("old extra %+v ttl %d", got.Policy, got.SessionTTLMS)
	}
}

// TestStatusRateBPSOptionalTail keeps the bulk cap after session_ttl_ms.
func TestStatusRateBPSOptionalTail(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		SessionTTLMS: 5000, RateBPS: 2_000_000,
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.RateBPS != 2_000_000 || out.SessionTTLMS != 5000 || !out.Policy.Present {
		t.Fatalf("rate %+v ttl %d policy %v", out.RateBPS, out.SessionTTLMS, out.Policy.Present)
	}
	rateOnly := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps, RateBPS: 800000}
	got, err := DecodeStatus(rateOnly.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.RateBPS != 800000 || got.SessionTTLMS != 0 {
		t.Fatalf("rate-only %d ttl %d", got.RateBPS, got.SessionTTLMS)
	}
	old := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps, SessionTTLMS: 5000}
	got, err = DecodeStatus(old.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.RateBPS != 0 || got.SessionTTLMS != 5000 {
		t.Fatalf("old rate %d ttl %d", got.RateBPS, got.SessionTTLMS)
	}
}

// TestStatusPeerHintPubRoundTrip keeps the 32-byte X25519 pub on a hint
// and does not steal the DNS / GapHold tail.
func TestStatusPeerHintPubRoundTrip(t *testing.T) {
	pub := make([]byte, 32)
	pub[0] = 7
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		Peers: 1, P2P: 1, DNS: []string{"10.8.0.1"}, GapHoldMS: 8000, IP6: "fd00::2",
		PeerHints: []PeerHint{{IP: "10.8.0.3", Host: "203.0.113.9", Port: 4242, VLAN: 0, Pub: pub}},
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.PeerHints) != 1 {
		t.Fatalf("%+v", out.PeerHints)
	}
	h := out.PeerHints[0]
	if h.IP != "10.8.0.3" || h.Host != "203.0.113.9" || h.Port != 4242 || len(h.Pub) != 32 || h.Pub[0] != 7 {
		t.Fatalf("%+v", h)
	}
	if len(out.DNS) != 1 || out.DNS[0] != "10.8.0.1" || out.GapHoldMS != 8000 || out.IP6 != "fd00::2" {
		t.Fatalf("tail %+v", out)
	}
}

// TestStatusPeerHintWithoutPubKeepsTail is the old hint layout (no pub byte)
// plus empty DNS: the decoder must not eat the DNS count 0 as pub_len.
func TestStatusPeerHintWithoutPubKeepsTail(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		Peers: 1, P2P: 1, GapHoldMS: 8000, IP6: "fd00::2",
		PeerHints: []PeerHint{{IP: "10.8.0.3", Host: "203.0.113.9", Port: 4242}},
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.PeerHints) != 1 || len(out.PeerHints[0].Pub) != 0 {
		t.Fatalf("%+v", out.PeerHints)
	}
	if len(out.DNS) != 0 || out.GapHoldMS != 8000 || out.IP6 != "fd00::2" {
		t.Fatalf("tail %+v", out)
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
