package wire

// Handshake and stream status codes. Numeric values are stable; names may grow.

import "fmt"

// Code is a protocol status. Values are stable; names may grow, numbers do not move.
type Code uint16

// Assigned Status and stream codes. Numbers do not move.
const (
	OK        Code = 0x0000
	OKResumed Code = 0x0001 // same instance, same IP, fast path

	AuthBadKey        Code = 0x0101
	AuthRevoked       Code = 0x0102
	AuthExpired       Code = 0x0103
	AuthNotAuthorized Code = 0x0104
	AuthLimit         Code = 0x0105 // multi_limit reached
	AuthAlreadyOnline Code = 0x0106 // multi off, another instance holds the key
	AuthInstance      Code = 0x0107
	AuthClock         Code = 0x0108

	NetDisabled Code = 0x0201
	NetUnknown  Code = 0x0202
	Protocol    Code = 0x0203
	RateLimit   Code = 0x0204
	Timeout     Code = 0x0205
	Internal    Code = 0x0206

	IPPool     Code = 0x0301
	IPConflict Code = 0x0302
	VLANDenied Code = 0x0303

	PolicyDrop Code = 0x0401

	StreamRefused Code = 0x0501 // Open: dial failed
	StreamReset   Code = 0x0502 // peer closed with error
)

// OK reports a successful Status (new lease or same-instance resume).
func (c Code) OK() bool { return c == OK || c == OKResumed }

// String is the stable protocol name, or "loft 0xNNNN" for unknown codes.
func (c Code) String() string {
	if s, ok := codeName[c]; ok {
		return s
	}
	return fmt.Sprintf("loft 0x%04x", uint16(c))
}

// codeName maps each assigned Code to its protocol name.
var codeName = map[Code]string{
	OK:                "ok",
	OKResumed:         "ok_resumed",
	AuthBadKey:        "auth_bad_key",
	AuthRevoked:       "auth_revoked",
	AuthExpired:       "auth_expired",
	AuthNotAuthorized: "auth_not_authorized",
	AuthLimit:         "auth_limit",
	AuthAlreadyOnline: "auth_already_online",
	AuthInstance:      "auth_instance",
	AuthClock:         "auth_clock",
	NetDisabled:       "net_disabled",
	NetUnknown:        "net_unknown",
	Protocol:          "protocol",
	RateLimit:         "rate_limit",
	Timeout:           "timeout",
	Internal:          "internal",
	IPPool:            "ip_pool",
	IPConflict:        "ip_conflict",
	VLANDenied:        "vlan_denied",
	PolicyDrop:        "policy_drop",
	StreamRefused:     "stream_refused",
	StreamReset:       "stream_reset",
}

// Error is a Status or stream failure with a stable Code.
type Error struct {
	Code Code
	Msg  string
}

// Error is "name" or "name: msg".
func (e Error) Error() string {
	if e.Msg == "" {
		return e.Code.String()
	}
	return e.Code.String() + ": " + e.Msg
}

// Fail wraps c and msg as an Error.
func Fail(c Code, msg string) error { return Error{Code: c, Msg: msg} }
