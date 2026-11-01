package transport

import "testing"

func TestL3UrgentACKOnly(t *testing.T) {
	syn := shapeSeg(tcpSYN, 0, false)
	if !l3Urgent(syn) {
		t.Fatal("syn")
	}
	ack := shapeSeg(tcpACK, 0, false)
	if !l3Urgent(ack) {
		t.Fatal("ack-only")
	}
	data := shapeSeg(tcpACK|tcpPSH, 200, false)
	if l3Urgent(data) {
		t.Fatal("bulk")
	}
	udp := append([]byte(nil), ack...)
	udp[9] = ipProtoUDP
	if !l3Urgent(udp) {
		t.Fatal("udp")
	}
}
