package transport

import (
	"net"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestApplyCC(t *testing.T) {
	t.Cleanup(func() { SetCCPolicy(nil, 0, 0) })
	ask := (wire.Hello{Version: 1, Min: wire.ProtoMin, Max: wire.Proto, CC: wire.CCBBR}).Encode()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			defer c.Close()
			buf := make([]byte, 1)
			c.Read(buf)
		}
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sock := &prefixConn{Conn: c}
	kernel := setTCPCongestion(c.(*net.TCPConn), "bbr") == nil

	var st wire.Status
	applyCC(&st, wire.FrontWS, ask, sock)
	if st.CC != wire.CCDefault {
		t.Fatal("node without cc said yes")
	}
	SetCCPolicy([]byte{wire.CCBBR}, 0, 0)
	applyCC(&st, wire.FrontGET, ask, sock)
	if st.CC != wire.CCDefault {
		t.Fatal("get said yes")
	}
	p1, p2 := net.Pipe()
	defer p1.Close()
	defer p2.Close()
	applyCC(&st, wire.FrontWS, ask, p1)
	if st.CC != wire.CCDefault {
		t.Fatal("no tcp socket, still yes")
	}
	applyCC(&st, wire.FrontWS, ask, sock)
	if (st.CC == wire.CCBBR) != kernel {
		t.Fatalf("cc %d, kernel bbr %v", st.CC, kernel)
	}
	off := (wire.Hello{Version: 1, Min: wire.ProtoMin, Max: wire.Proto}).Encode()
	applyCC(&st, wire.FrontWS, off, sock)
	if st.CC != wire.CCDefault {
		t.Fatal("client left it off, node said yes")
	}
}
