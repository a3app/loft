package ifcfg

// Unit tests for message building and parsing. No sockets, no root: these
// run in plain CI on Linux.

import (
	"bytes"
	"testing"

	"golang.org/x/sys/unix"
)

// TestRequestLayout builds an address request and checks every byte that
// matters: header, body, both attributes, padding.
func TestRequestLayout(t *testing.T) {
	req := newRequest(unix.RTM_NEWADDR, unix.NLM_F_CREATE|unix.NLM_F_EXCL)
	req.body(ifaddrmsg(unix.AF_INET, 24, unix.RT_SCOPE_UNIVERSE, 7))
	req.attr(unix.IFA_LOCAL, []byte{10, 10, 0, 10})
	req.attr(unix.IFA_ADDRESS, []byte{10, 10, 0, 10})
	b := req.finish(42, 99)

	// 16 header + 8 ifaddrmsg + 2 * (4 attr header + 4 address) = 40.
	if len(b) != 40 {
		t.Fatalf("length %d, want 40", len(b))
	}
	if got := nativeEndian.Uint32(b[0:4]); got != 40 {
		t.Errorf("header length field %d, want 40", got)
	}
	if got := nativeEndian.Uint16(b[4:6]); got != unix.RTM_NEWADDR {
		t.Errorf("type %d", got)
	}
	wantFlags := uint16(unix.NLM_F_REQUEST | unix.NLM_F_ACK | unix.NLM_F_CREATE | unix.NLM_F_EXCL)
	if got := nativeEndian.Uint16(b[6:8]); got != wantFlags {
		t.Errorf("flags %#x, want %#x", got, wantFlags)
	}
	if nativeEndian.Uint32(b[8:12]) != 42 || nativeEndian.Uint32(b[12:16]) != 99 {
		t.Errorf("seq/pid not filled in")
	}
	if b[16] != unix.AF_INET || b[17] != 24 || nativeEndian.Uint32(b[20:24]) != 7 {
		t.Errorf("ifaddrmsg wrong: % x", b[16:24])
	}
	attrs, err := parseAttrs(b[24:])
	if err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 2 || attrs[0].typ != unix.IFA_LOCAL || attrs[1].typ != unix.IFA_ADDRESS {
		t.Fatalf("attrs %+v", attrs)
	}
	if !bytes.Equal(attrs[0].value, []byte{10, 10, 0, 10}) {
		t.Errorf("IFA_LOCAL % x", attrs[0].value)
	}
}

// TestAttrPadding: a value that is not a multiple of 4 bytes must be padded
// with zeros, and the length field must not count the padding.
func TestAttrPadding(t *testing.T) {
	req := newRequest(unix.RTM_NEWLINK, 0)
	req.attr(3, []byte("tun0x")) // 5 bytes: 4 header + 5 = 9, padded to 12
	b := req.finish(1, 1)
	if len(b) != 16+12 {
		t.Fatalf("length %d, want 28", len(b))
	}
	if got := nativeEndian.Uint16(b[16:18]); got != 9 {
		t.Errorf("attribute length %d, want 9 (padding not counted)", got)
	}
	if !bytes.Equal(b[25:28], []byte{0, 0, 0}) {
		t.Errorf("padding not zero: % x", b[25:28])
	}
}

// msgBytes builds a raw message the way the kernel would send it.
func msgBytes(typ uint16, seq, pid uint32, data []byte) []byte {
	b := make([]byte, nlHeaderLen, nlHeaderLen+len(data)+3)
	nativeEndian.PutUint32(b[0:4], uint32(nlHeaderLen+len(data)))
	nativeEndian.PutUint16(b[4:6], typ)
	nativeEndian.PutUint32(b[8:12], seq)
	nativeEndian.PutUint32(b[12:16], pid)
	b = append(b, data...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func errData(code int32) []byte {
	b := make([]byte, 4+nlHeaderLen) // status + echo of our header
	nativeEndian.PutUint32(b[0:4], uint32(code))
	return b
}

// TestParseSeveral: the kernel packs several messages into one datagram;
// all of them must come out, in order.
func TestParseSeveral(t *testing.T) {
	var dgram []byte
	dgram = append(dgram, msgBytes(unix.RTM_NEWROUTE, 5, 9, []byte{1, 2, 3})...) // padded
	dgram = append(dgram, msgBytes(unix.NLMSG_DONE, 5, 9, []byte{0, 0, 0, 0})...)
	msgs, err := parseMessages(dgram)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].typ != unix.RTM_NEWROUTE || msgs[1].typ != unix.NLMSG_DONE {
		t.Fatalf("got %+v", msgs)
	}
	if !bytes.Equal(msgs[0].data, []byte{1, 2, 3}) {
		t.Errorf("data % x (padding must not leak into data)", msgs[0].data)
	}
}

// TestParseRejectsBadLength: a length field that points past the datagram
// or below the header size is corruption and must be an error, not a panic.
func TestParseRejectsBadLength(t *testing.T) {
	good := msgBytes(unix.NLMSG_ERROR, 1, 1, errData(0))
	cases := map[string][]byte{
		"short header": good[:10],
		"length too big": func() []byte {
			b := append([]byte(nil), good...)
			nativeEndian.PutUint32(b[0:4], uint32(len(b)+4))
			return b
		}(),
		"length too small": func() []byte {
			b := append([]byte(nil), good...)
			nativeEndian.PutUint32(b[0:4], 8)
			return b
		}(),
	}
	for name, b := range cases {
		if _, err := parseMessages(b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestErrnoOf: 0 is success, -17 is EEXIST, a positive code or a short
// payload is an error of its own.
func TestErrnoOf(t *testing.T) {
	if e, err := errnoOf(message{data: errData(0)}); err != nil || e != 0 {
		t.Errorf("ack: %v %v", e, err)
	}
	if e, err := errnoOf(message{data: errData(-int32(unix.EEXIST))}); err != nil || e != unix.EEXIST {
		t.Errorf("EEXIST: %v %v", e, err)
	}
	if _, err := errnoOf(message{data: errData(5)}); err == nil {
		t.Error("positive code accepted")
	}
	if _, err := errnoOf(message{data: []byte{1}}); err == nil {
		t.Error("short payload accepted")
	}
}

// TestParseAttrsRejectsBadLength mirrors TestParseRejectsBadLength for TLVs.
func TestParseAttrsRejectsBadLength(t *testing.T) {
	b := []byte{0xff, 0x00, 0x01, 0x00, 1, 2, 3, 4} // claims 255 bytes
	if _, err := parseAttrs(b); err == nil {
		t.Error("oversized attribute accepted")
	}
	if _, err := parseAttrs([]byte{2, 0}); err == nil {
		t.Error("short attribute accepted")
	}
}
