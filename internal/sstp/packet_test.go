package sstp

import "testing"

func TestDuplexPathMSSpec(t *testing.T) {
	want := "/sra_{BA195980-CD49-458b-9E23-C84EE0ADCD75}/"
	if duplexPath != want {
		t.Fatalf("duplexPath=%q want %q", duplexPath, want)
	}
}

func TestConnectRequestLen(t *testing.T) {
	b := connectRequest().encode()
	if len(b) != 14 {
		t.Fatalf("CALL_CONNECT_REQUEST len=%d want 14", len(b))
	}
	if b[0] != ver || b[1] != 1 {
		t.Fatalf("hdr %x %x", b[0], b[1])
	}
}

func TestLCPAuthDefaultMSCHAPv2(t *testing.T) {
	p := newPPP("u", "p", false)
	_, nak, _ := p.lcpSplit([]byte{3, 4, 0xc0, 0x23})
	if len(nak) < 5 || nak[4] != 0x81 {
		t.Fatalf("nak pap %x", nak)
	}
	ack, nak2, _ := p.lcpSplit([]byte{3, 5, 0xc2, 0x23, 0x81})
	if len(ack) == 0 || len(nak2) != 0 {
		t.Fatalf("ack mschapv2 ack=%x nak=%x", ack, nak2)
	}
	_, nak3, _ := p.lcpSplit([]byte{3, 5, 0xc2, 0x23, 0x05})
	if len(nak3) < 5 || nak3[4] != 0x81 {
		t.Fatalf("nak md5 %x", nak3)
	}
	q := newPPP("u", "p", true)
	ackP, nakP, _ := q.lcpSplit([]byte{3, 4, 0xc0, 0x23})
	if len(ackP) == 0 || len(nakP) != 0 {
		t.Fatalf("pap allowed ack=%x nak=%x", ackP, nakP)
	}
}
