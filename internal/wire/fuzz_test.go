package wire

// Fuzz targets for mux batches and Hello/Status decoders.

import "testing"

// FuzzDecodeBatch round-trips any blob that DecodeBatch accepts.
func FuzzDecodeBatch(f *testing.F) {
	f.Add([]byte{0x00, 0x02, CmdData, 0, 0, 0, 1, 'h', 'i'})
	f.Add([]byte{0, 0, CmdPad, 0, 0, 0, 0})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		frames, err := DecodeBatch(b)
		if err != nil {
			return
		}
		enc, err := EncodeBatch(frames)
		if err != nil {
			t.Fatal(err)
		}
		out, err := DecodeBatch(enc)
		if err != nil || len(out) != len(frames) {
			t.Fatalf("roundtrip %v n=%d/%d", err, len(out), len(frames))
		}
	})
}

// FuzzDecodeHelloStatus feeds arbitrary bytes to DecodeHello and DecodeStatus.
func FuzzDecodeHelloStatus(f *testing.F) {
	h := Hello{Version: 1, MemberID: "a", InstanceID: "b", Min: ProtoMin, Max: Proto, Caps: NodeCaps, Program: "loft", Software: "0.1.0"}
	f.Add(h.Encode())
	st := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto}
	f.Add(st.Encode())
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = DecodeHello(b)
		_, _ = DecodeStatus(b)
	})
}
