package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
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

// TestQUICAndH3SwitchCC: a client that asks gets what the node allows on
// both QUIC fronts, and nothing when it does not ask.
func TestQUICAndH3SwitchCC(t *testing.T) {
	t.Cleanup(func() { SetCCPolicy(nil, 0, 0); SetClientCC(wire.CCDefault, 0, 0) })
	token := "node-token"
	certFile, keyFile := quicCertFiles(t)
	info := NodeInfo{Token: []byte(token), Caps: wire.NodeCaps}
	probe, _ := net.ListenPacket("udp", "127.0.0.1:0")
	listen := probe.LocalAddr().String()
	probe.Close()
	go ListenUDP(listen, certFile, keyFile, info, []string{wire.FrontQUIC, wire.FrontH3})
	time.Sleep(100 * time.Millisecond)
	SetCCPolicy([]byte{wire.CCBBR, wire.CCFixed}, 0, 0)

	for _, c := range []struct {
		ask, want byte
		down      uint32
	}{
		{wire.CCBBR, wire.CCBBR, 0},
		{wire.CCFixed, wire.CCFixed, 50000},
		{wire.CCDefault, wire.CCDefault, 0},
	} {
		SetClientCC(c.ask, 10000, c.down)
		for _, kind := range []string{wire.FrontQUIC, wire.FrontH3} {
			ccInUse.Store(99)
			o := config.Outbound{Type: kind, URL: "https://" + listen, Insecure: true}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var err error
			if kind == wire.FrontQUIC {
				q := NewQUIC(token, o)
				if err = q.Start(ctx); err == nil {
					q.Close()
				}
			} else {
				h := NewH3(token, o, wire.MemberCaps)
				if err = h.Start(ctx); err == nil {
					h.Close()
				}
			}
			cancel()
			if err != nil {
				t.Fatalf("%s ask %d: %v", kind, c.ask, err)
			}
			if got := CCInUse(); got != c.want {
				t.Fatalf("%s ask %d: in use %d, want %d", kind, c.ask, got, c.want)
			}
		}
	}
}
