package transport

// A node that rejects the member key answers like a site (§3.4): GET 404,
// every other front closes right after 0x02 without a Status. The client
// reads that clean close as auth_bad_key. A timeout or a reset stays a
// network error, so a middlebox that cuts the path does not look like a key
// problem. StartSelected trusts the inference only when every front agrees.

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/coder/websocket"
	"github.com/quic-go/quic-go"
	"golang.org/x/net/http2"

	"github.com/a3app/loft/internal/wire"
)

// proofRefused is auth_bad_key inferred from a close after 0x02.
type proofRefused struct{ err wire.Error }

func (e proofRefused) Error() string { return e.err.Error() }
func (e proofRefused) Unwrap() error { return e.err }

func refused() error {
	return proofRefused{wire.Error{Code: wire.AuthBadKey, Msg: "closed after proof"}}
}

// afterProof maps the Status read that failed right after 0x02.
func afterProof(err error) error {
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return err
	}
	var ae *quic.ApplicationError
	var rst h2Reset
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		websocket.CloseStatus(err) == websocket.StatusPolicyViolation ||
		(errors.As(err, &ae) && ae.Remote && ae.ErrorCode == 0) ||
		(errors.As(err, &rst) && (rst.code == http2.ErrCodeCancel || rst.code == http2.ErrCodeNo)) {
		return refused()
	}
	return err
}

// inferredRefusal reports whether err is only the close-after-proof guess.
func inferredRefusal(err error) bool {
	var pr proofRefused
	return errors.As(err, &pr)
}
