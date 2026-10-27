package transport

import (
	"net"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestApplyCC(t *testing.T) {
	t.Cleanup(func() { SetCCPolicy(nil, 0, 0) })
	ask := (wire.Hello{Version: 1, Min: wire.ProtoMin, Max: wire.Proto, CC: wire.CCBBR}).Encode()
	p1, p2 := net.Pipe()
	defer p1.Close()
	defer p2.Close()

	var st wire.Status
	SetCCPolicy([]byte{wire.CCBBR}, 0, 0)
	applyCC(&st, wire.FrontGET, ask, nil)
	if st.CC != wire.CCDefault {
		t.Fatal("get said yes")
	}
	applyCC(&st, wire.FrontWS, ask, p1)
	if st.CC != wire.CCDefault {
		t.Fatal("tcp front said yes")
	}
}
