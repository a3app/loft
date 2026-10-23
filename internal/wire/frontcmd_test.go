package wire

import "testing"

func TestSplitFront(t *testing.T) {
	fs := []Frame{
		{Cmd: CmdData, StreamID: 1, Payload: []byte("x")},
		FrontFrame(FrontH2),
		{Cmd: CmdPing, Payload: []byte{1, 2, 3, 4, 5, 6, 7, 8}},
	}
	rest, last := SplitFront(fs)
	if last != FrontH2 || len(rest) != 2 {
		t.Fatalf("%q %d", last, len(rest))
	}
	if ParseFrontCmd([]byte("l3")) != "" || ParseFrontCmd([]byte("nope")) != "" {
		t.Fatal("l3")
	}
}
