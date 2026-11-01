package ifcfg

// nlmsg_linux.go builds and parses raw netlink messages. It does no I/O, so
// it can be unit-tested without root.
//
// A short primer, since netlink is not common knowledge. The kernel and a
// program talk over a special socket (AF_NETLINK). Every message is:
//
//	+----------------------+-------------------------+----------------------+
//	| header, 16 bytes     | fixed body (per type)   | attributes (TLVs)    |
//	| len,type,flags,seq,  | e.g. ifaddrmsg: family, | each: len u16,       |
//	| pid                  | prefix, ..., ifindex    | type u16, value, pad |
//	+----------------------+-------------------------+----------------------+
//
// Everything is padded to 4 bytes and written in the machine's own byte
// order (not network order). The kernel answers each request that asked for
// an acknowledgement with an NLMSG_ERROR message whose first 4 bytes are 0
// for success or a negative errno, e.g. -17 (EEXIST, "file exists").
// A "dump" request (list all addresses, all routes) is answered with a
// series of data messages that ends with NLMSG_DONE.

import (
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// nativeEndian: netlink uses the byte order of the running machine.
var nativeEndian = binary.NativeEndian

const nlHeaderLen = unix.SizeofNlMsghdr // 16

// align4 rounds n up to the next multiple of 4 (netlink's alignment).
func align4(n int) int { return (n + 3) &^ 3 }

// request is one netlink message being built.
type request struct {
	b []byte
}

// newRequest starts a message of the given type. NLM_F_REQUEST ("this is a
// request") and NLM_F_ACK ("answer me even on success") are always set:
// without the ACK a successful change would be answered with silence, and
// we could not tell "done" from "lost".
func newRequest(typ uint16, flags uint16) *request {
	r := &request{b: make([]byte, nlHeaderLen, 128)}
	nativeEndian.PutUint16(r.b[4:6], typ)
	nativeEndian.PutUint16(r.b[6:8], flags|unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	return r
}

// body appends the fixed part that follows the header (ifaddrmsg, rtmsg,
// ifinfomsg). All of them are a multiple of 4 bytes, so no padding.
func (r *request) body(p []byte) { r.b = append(r.b, p...) }

// attr appends one attribute: 2 bytes length (header + value, without the
// padding), 2 bytes type, the value, then zero padding up to 4 bytes.
func (r *request) attr(typ uint16, value []byte) {
	n := unix.SizeofRtAttr + len(value)
	var h [unix.SizeofRtAttr]byte
	nativeEndian.PutUint16(h[0:2], uint16(n))
	nativeEndian.PutUint16(h[2:4], typ)
	r.b = append(r.b, h[:]...)
	r.b = append(r.b, value...)
	for len(r.b)%4 != 0 {
		r.b = append(r.b, 0)
	}
}

// attrU32 appends an attribute holding a 32-bit number (an interface index,
// an MTU).
func (r *request) attrU32(typ uint16, v uint32) {
	var b [4]byte
	nativeEndian.PutUint32(b[:], v)
	r.attr(typ, b[:])
}

// finish fills in the total length, the sequence number and our port id
// and returns the bytes to send. The sequence number lets us match the
// answer to this request; the port id is our socket's address.
func (r *request) finish(seq, pid uint32) []byte {
	nativeEndian.PutUint32(r.b[0:4], uint32(len(r.b)))
	nativeEndian.PutUint32(r.b[8:12], seq)
	nativeEndian.PutUint32(r.b[12:16], pid)
	return r.b
}

// ifaddrmsg is the fixed body of RTM_NEWADDR / RTM_DELADDR
// (struct ifaddrmsg in <linux/if_addr.h>): which address on which interface.
func ifaddrmsg(family, prefixLen, scope uint8, index uint32) []byte {
	b := make([]byte, unix.SizeofIfAddrmsg) // 8
	b[0] = family
	b[1] = prefixLen
	b[2] = 0 // flags: none
	b[3] = scope
	nativeEndian.PutUint32(b[4:8], index)
	return b
}

// rtmsg is the fixed body of RTM_NEWROUTE / RTM_DELROUTE
// (struct rtmsg in <linux/rtnetlink.h>).
func rtmsg(family, dstLen, table, protocol, scope, typ uint8) []byte {
	b := make([]byte, unix.SizeofRtMsg) // 12
	b[0] = family
	b[1] = dstLen
	b[2] = 0 // source prefix length: we never route by source
	b[3] = 0 // TOS: any
	b[4] = table
	b[5] = protocol
	b[6] = scope
	b[7] = typ
	// b[8:12] flags: none
	return b
}

// ifinfomsg is the fixed body of RTM_NEWLINK (struct ifinfomsg in
// <linux/rtnetlink.h>). change says which bits of flags to apply; other
// interface flags stay as they are.
func ifinfomsg(index int32, flags, change uint32) []byte {
	b := make([]byte, unix.SizeofIfInfomsg) // 16
	b[0] = unix.AF_UNSPEC
	// b[1] padding, b[2:4] device type: not changed
	nativeEndian.PutUint32(b[4:8], uint32(index))
	nativeEndian.PutUint32(b[8:12], flags)
	nativeEndian.PutUint32(b[12:16], change)
	return b
}

// message is one parsed netlink message.
type message struct {
	typ   uint16
	flags uint16
	seq   uint32
	pid   uint32
	data  []byte // everything after the 16-byte header
}

var errShort = errors.New("netlink: truncated message")

// parseMessages splits one received datagram into messages. The kernel may
// pack several messages into one datagram (dumps do), so we walk them by
// their length fields. A length that does not fit is treated as corruption
// rather than guessed around.
func parseMessages(b []byte) ([]message, error) {
	var out []message
	for len(b) > 0 {
		if len(b) < nlHeaderLen {
			return nil, errShort
		}
		n := int(nativeEndian.Uint32(b[0:4]))
		if n < nlHeaderLen || n > len(b) {
			return nil, fmt.Errorf("netlink: bad message length %d (have %d)", n, len(b))
		}
		out = append(out, message{
			typ:   nativeEndian.Uint16(b[4:6]),
			flags: nativeEndian.Uint16(b[6:8]),
			seq:   nativeEndian.Uint32(b[8:12]),
			pid:   nativeEndian.Uint32(b[12:16]),
			data:  b[nlHeaderLen:n],
		})
		// The next message starts at the next 4-byte boundary. The last
		// one may lack its padding, so never step past the end.
		next := align4(n)
		if next > len(b) {
			next = len(b)
		}
		b = b[next:]
	}
	return out, nil
}

// errnoOf reads the status of an NLMSG_ERROR (or NLMSG_DONE) message: 0
// means success, otherwise the kernel's errno. The kernel stores it as a
// negative int32.
func errnoOf(m message) (unix.Errno, error) {
	if len(m.data) < 4 {
		return 0, errShort
	}
	code := int32(nativeEndian.Uint32(m.data[0:4]))
	if code > 0 {
		// Never sent by a real kernel; do not turn it into a bogus errno.
		return 0, fmt.Errorf("netlink: positive error code %d", code)
	}
	return unix.Errno(-code), nil
}

// attribute is one parsed TLV from the attribute area of a message.
type attribute struct {
	typ   uint16
	value []byte
}

// parseAttrs walks the attribute area (what follows the fixed body).
// Used to read dumps back, in tests and checks.
func parseAttrs(b []byte) ([]attribute, error) {
	var out []attribute
	for len(b) > 0 {
		if len(b) < unix.SizeofRtAttr {
			return nil, errShort
		}
		n := int(nativeEndian.Uint16(b[0:2]))
		if n < unix.SizeofRtAttr || n > len(b) {
			return nil, fmt.Errorf("netlink: bad attribute length %d (have %d)", n, len(b))
		}
		out = append(out, attribute{
			// The top two bits of the type are flags (nested, byte order);
			// mask them off to get the plain attribute number.
			typ:   nativeEndian.Uint16(b[2:4]) & 0x3fff,
			value: b[unix.SizeofRtAttr:n],
		})
		next := align4(n)
		if next > len(b) {
			next = len(b)
		}
		b = b[next:]
	}
	return out, nil
}
