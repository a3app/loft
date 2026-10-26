package transport

// A node that rejects the member key answers like an ordinary site: GET
// gets 404, every other front closes right after 0x02 without a Status.
// The protocol requires this so that a prober without a key learns nothing;
// a sealed "bad key" reply would confirm a loft node. The client reads that
// clean close as auth_bad_key. A timeout or a reset stays a network error,
// so a middlebox that cuts the path does not look like a key problem.
// StartSelected trusts the inference only when every front agrees.

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

// proofRefused is auth_bad_key inferred from a close after 0x02, not sent
// by the node. It is a separate type so StartSelected can tell a guess from
// a real Status error.
type proofRefused struct{ err wire.Error }

func (e proofRefused) Error() string { return e.err.Error() }
func (e proofRefused) Unwrap() error { return e.err }

func refused() error {
	return proofRefused{wire.Error{Code: wire.AuthBadKey, Msg: "closed after proof"}}
}

// afterProof maps the Status read that failed right after 0x02. A clean
// close on any front (EOF, WebSocket policy close, QUIC close with code 0,
// HTTP/2 CANCEL or NO_ERROR reset) becomes proofRefused. Timeouts,
// cancellations and every other error pass through unchanged.
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
