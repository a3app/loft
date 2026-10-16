package mux

// Stream, datagram, Leave, deadline, and Pull tests.

import (
	"io"
	"testing"
	"time"

	"github.com/a3app/loft/internal/wire"
)

// TestDgramRoundTrip sends a UDP datagram from client to server.
func TestDgramRoundTrip(t *testing.T) {
	c, s := NewClient(), NewServer()
	go func() {
		for {
			fs := c.Pull(64*1024, 50*time.Millisecond)
			if len(fs) > 0 {
				s.Push(fs)
			}
		}
	}()
	go func() {
		for {
			fs := s.Pull(64*1024, 50*time.Millisecond)
			if len(fs) > 0 {
				c.Push(fs)
			}
		}
	}()
	if err := c.WriteDgram("1.2.3.4:53", []byte("dns")); err != nil {
		t.Fatal(err)
	}
	addr, data, err := s.ReadDgram()
	if err != nil || addr != "1.2.3.4:53" || string(data) != "dns" {
		t.Fatalf("%s %q %v", addr, data, err)
	}
}

// TestLargeDgramDoesNotJumpData keeps TUN QUIC (~1200 B) from starving TCP
// ACKs: Pull drains pri first, so a pri CmdDgram would ride every GET
// ahead of Data already queued in out. DNS still jumps.
func TestLargeDgramDoesNotJumpData(t *testing.T) {
	c := NewClient()
	st, err := c.Open("1.2.3.4:80")
	if err != nil {
		t.Fatal(err)
	}
	open := c.Pull(64*1024, 0)
	if len(open) == 0 || open[0].Cmd != wire.CmdOpen {
		t.Fatal("open")
	}
	if _, err := st.Write(make([]byte, 8000)); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteDgram("17.248.214.68:443", make([]byte, 1200)); err != nil {
		t.Fatal(err)
	}
	fs := c.Pull(64*1024, 0)
	if len(fs) == 0 {
		t.Fatal("empty")
	}
	if fs[0].Cmd == wire.CmdDgram {
		t.Fatal("quic dgram jumped TCP Data")
	}
	var sawData, sawDgram bool
	for _, f := range fs {
		switch f.Cmd {
		case wire.CmdData:
			sawData = true
		case wire.CmdDgram:
			sawDgram = true
		}
	}
	if !sawData || !sawDgram {
		t.Fatalf("data=%v dgram=%v n=%d", sawData, sawDgram, len(fs))
	}

	if _, err := st.Write(make([]byte, 8000)); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteDgram("1.2.3.4:53", []byte("dns")); err != nil {
		t.Fatal(err)
	}
	fs = c.Pull(64*1024, 0)
	if len(fs) == 0 || fs[0].Cmd != wire.CmdDgram {
		t.Fatal("dns dgram must still jump Data")
	}
}

// TestFilterDgram steals matching datagrams and leaves others for ReadDgram.
func TestFilterDgram(t *testing.T) {
	c, s := NewClient(), NewServer()
	go pump(c, s)
	go pump(s, c)
	got := make(chan []byte, 1)
	s.FilterDgram(func(addr string, data []byte) bool {
		if addr == "10.8.0.1:1" {
			got <- data
			return true
		}
		return false
	})
	if err := c.WriteDgram("10.8.0.1:1", []byte("icmp")); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteDgram("1.2.3.4:53", []byte("dns")); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-got:
		if string(d) != "icmp" {
			t.Fatalf("%q", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("filter")
	}
	addr, data, err := s.ReadDgram()
	if err != nil || addr != "1.2.3.4:53" || string(data) != "dns" {
		t.Fatalf("%s %q %v", addr, data, err)
	}
}

// TestBytes counts Dgram payload on send and receive.
func TestBytes(t *testing.T) {
	c := NewClient()
	if err := c.WriteDgram("1.2.3.4:53", []byte("dns")); err != nil {
		t.Fatal(err)
	}
	up, down := c.Bytes()
	if up == 0 || down != 0 {
		t.Fatalf("client up=%d down=%d", up, down)
	}
	s := NewServer()
	fs := c.Pull(64*1024, 50*time.Millisecond)
	if len(fs) == 0 {
		t.Fatal("no frame")
	}
	s.Push(fs)
	_, d2 := s.Bytes()
	if d2 != up {
		t.Fatalf("server down=%d want %d", d2, up)
	}
}

// TestWriteChunksOverMaxPayload splits a 40 KiB write under MaxPayload.
func TestWriteChunksOverMaxPayload(t *testing.T) {
	cli, srv := NewClient(), NewServer()
	go pump(cli, srv)
	go pump(srv, cli)
	go func() {
		st, err := srv.Accept()
		if err != nil {
			return
		}
		defer st.Close()
		_, _ = io.Copy(st, st)
	}()
	st, err := cli.Open("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	want := make([]byte, 40<<10)
	for i := range want {
		want[i] = byte(i)
	}
	_ = st.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := st.Write(want); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(st, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("large write mismatch")
	}
}

func TestTuneIsPriority(t *testing.T) {
	s := NewClient()
	if err := s.send(wire.TuneFrame(wire.TuneCalm, 16, 12<<10)); err != nil {
		t.Fatal(err)
	}
	got := s.Pull(64*1024, 0)
	if len(got) != 1 || got[0].Cmd != wire.CmdTune {
		t.Fatalf("%+v", got)
	}
}

// TestWriteChunksFitGETCookie keeps CmdData ≤ streamChunk so a Yandex
// Cookie (16 KiB encoded, concurrent) is one GET without ENHANCE_YOUR_CALM.
func TestWriteChunksFitGETCookie(t *testing.T) {
	c := NewClient()
	st, err := c.Open("1.2.3.4:80")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Pull(64*1024, 0) // drain Open
	if _, err := st.Write(make([]byte, streamChunk+1)); err != nil {
		t.Fatal(err)
	}
	fs := c.Pull(64*1024, 0)
	if len(fs) < 2 {
		t.Fatalf("frames %d", len(fs))
	}
	for _, f := range fs {
		if f.Cmd == wire.CmdData && len(f.Payload) > streamChunk {
			t.Fatalf("chunk %d > %d", len(f.Payload), streamChunk)
		}
	}
}

// TestChunkFitsYandexCookie is one CmdData in a 16 KiB encoded Cookie.
func TestChunkFitsYandexCookie(t *testing.T) {
	plain := wire.HeaderSize + streamChunk
	sealed := plain + 12 + 16
	cookie := sealed*4/3 + len("session=")
	if cookie > 18<<10 {
		t.Fatalf("cookie %d > 18KiB (plain %d)", cookie, plain)
	}
	if streamChunk <= smallPri {
		t.Fatalf("streamChunk %d <= smallPri %d", streamChunk, smallPri)
	}
}

// TestOpenEcho opens a stream and echoes Data.
func TestOpenEcho(t *testing.T) {
	cli, srv := NewClient(), NewServer()
	go pump(cli, srv)
	go pump(srv, cli)
	go func() {
		st, err := srv.Accept()
		if err != nil {
			return
		}
		defer st.Close()
		_, _ = io.Copy(st, st)
	}()
	st, err := cli.Open("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	_ = st.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(st, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatal(string(buf))
	}
}

// TestDataBeforeOpen holds Data until Open (GET/HTTP2 reordering).
func TestDataBeforeOpen(t *testing.T) {
	cli, srv := NewClient(), NewServer()
	go pump(cli, srv)
	go pump(srv, cli)
	done := make(chan string, 1)
	go func() {
		st, err := srv.Accept()
		if err != nil {
			return
		}
		defer st.Close()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(st, buf); err != nil {
			return
		}
		done <- string(buf)
	}()
	id := uint32(1)
	srv.Push([]wire.Frame{{Cmd: wire.CmdData, StreamID: id, Payload: []byte("hello")}})
	srv.Push([]wire.Frame{{Cmd: wire.CmdOpen, StreamID: id, Payload: EncodeTarget("example.com", 443)}})
	select {
	case got := <-done:
		if got != "hello" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("data dropped before open")
	}
}

// TestPullPriorityBeforeData drains Ping before queued bulk Data (bufferbloat).
func TestPullPriorityBeforeData(t *testing.T) {
	s := NewClient()
	bulk := make([]byte, smallPri+1)
	if err := s.send(wire.Frame{Cmd: wire.CmdData, Payload: bulk}); err != nil {
		t.Fatal(err)
	}
	if err := s.send(wire.Frame{Cmd: wire.CmdPing, Payload: []byte("p")}); err != nil {
		t.Fatal(err)
	}
	got := s.Pull(64*1024, 0)
	if len(got) < 2 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].Cmd != wire.CmdPing {
		t.Fatalf("want ping first, got %d", got[0].Cmd)
	}
	if got[1].Cmd != wire.CmdData {
		t.Fatalf("want data second, got %d", got[1].Cmd)
	}
}

// TestOtherStreamSmallJumpsBulk lets a new stream's tiny Data jump bulk
// (Speedtest loaded ping) without reordering the download stream.
func TestOtherStreamSmallJumpsBulk(t *testing.T) {
	s := NewClient()
	a, err := s.Open("a.example:443")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Open("b.example:443")
	if err != nil {
		t.Fatal(err)
	}
	bulk := make([]byte, streamChunk)
	if _, err := a.Write(bulk); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := s.Pull(64*1024, 0)
	var data []wire.Frame
	for _, f := range got {
		if f.Cmd == wire.CmdData {
			data = append(data, f)
		}
	}
	if len(data) < 2 {
		t.Fatalf("data %d", len(data))
	}
	if string(data[0].Payload) != "ping" {
		t.Fatalf("want ping first n0=%d", len(data[0].Payload))
	}
	if len(data[1].Payload) != len(bulk) {
		t.Fatalf("bulk second n=%d", len(data[1].Payload))
	}
}

// TestSmallDataDoesNotJumpBulk keeps TLS record order on one stream.
func TestSmallDataDoesNotJumpBulk(t *testing.T) {
	s := NewClient()
	bulk := make([]byte, 12<<10)
	if err := s.send(wire.Frame{Cmd: wire.CmdData, Payload: bulk}); err != nil {
		t.Fatal(err)
	}
	if err := s.send(wire.Frame{Cmd: wire.CmdData, Payload: []byte("ping")}); err != nil {
		t.Fatal(err)
	}
	got := s.Pull(64*1024, 0)
	if len(got) < 2 {
		t.Fatalf("got %d", len(got))
	}
	if len(got[0].Payload) != len(bulk) || string(got[1].Payload) != "ping" {
		t.Fatalf("reordered n0=%d n1=%d", len(got[0].Payload), len(got[1].Payload))
	}
}

// TestSameStreamSmallStaysInOrder keeps a 4-byte write behind earlier bulk
// on the same stream (inner TLS record order).
func TestSameStreamSmallStaysInOrder(t *testing.T) {
	s := NewClient()
	st, err := s.Open("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	bulk := make([]byte, streamChunk)
	if _, err := st.Write(bulk); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write([]byte("ack")); err != nil {
		t.Fatal(err)
	}
	got := s.Pull(64*1024, 0)
	var data []wire.Frame
	for _, f := range got {
		if f.Cmd == wire.CmdData {
			data = append(data, f)
		}
	}
	if len(data) < 2 {
		t.Fatalf("data frames %d", len(data))
	}
	if len(data[0].Payload) != len(bulk) || string(data[1].Payload) != "ack" {
		t.Fatalf("reordered n0=%d n1=%d", len(data[0].Payload), len(data[1].Payload))
	}
}

// TestPushDoesNotStarveOtherStream delivers Data to stream 2 while stream 1 is full.
func TestPushDoesNotStarveOtherStream(t *testing.T) {
	s := NewServer()
	defer s.Close()
	s.Push([]wire.Frame{
		{Cmd: wire.CmdOpen, StreamID: 2, Payload: EncodeTarget("a.example", 443)},
		{Cmd: wire.CmdOpen, StreamID: 4, Payload: EncodeTarget("b.example", 443)},
	})
	st1, err := s.Accept()
	if err != nil {
		t.Fatal(err)
	}
	st2, err := s.Accept()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(st1.in); i++ {
		s.handle(wire.Frame{Cmd: wire.CmdData, StreamID: 2, Payload: []byte{byte(i)}})
	}
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 4)
		if _, err := io.ReadFull(st2, buf); err != nil {
			return
		}
		done <- string(buf)
	}()
	go s.Push([]wire.Frame{
		{Cmd: wire.CmdData, StreamID: 2, Payload: []byte("more")},
		{Cmd: wire.CmdData, StreamID: 4, Payload: []byte("ping")},
	})
	select {
	case got := <-done:
		if got != "ping" {
			t.Fatalf("%q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream 2 blocked behind full stream 1")
	}
}

// TestPullCloseDoesNotJumpData keeps graceful Close behind queued Data.
func TestPullCloseDoesNotJumpData(t *testing.T) {
	s := NewClient()
	if err := s.send(wire.Frame{Cmd: wire.CmdData, Payload: []byte("bulk")}); err != nil {
		t.Fatal(err)
	}
	if err := s.send(wire.Frame{Cmd: wire.CmdClose, StreamID: 1}); err != nil {
		t.Fatal(err)
	}
	got := s.Pull(64*1024, 0)
	if len(got) < 2 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].Cmd != wire.CmdData || got[1].Cmd != wire.CmdClose {
		t.Fatalf("order %d %d", got[0].Cmd, got[1].Cmd)
	}
}

// TestPriSendUnblocksOnClose unblocks a full priority queue when Close runs.
func TestPriSendUnblocksOnClose(t *testing.T) {
	s := NewClient()
	for i := 0; i < cap(s.pri); i++ {
		s.pri <- wire.Frame{Cmd: wire.CmdPing}
	}
	done := make(chan error, 1)
	go func() {
		done <- s.send(wire.Frame{Cmd: wire.CmdPing})
	}()
	time.Sleep(20 * time.Millisecond)
	s.Close()
	select {
	case err := <-done:
		if err != ErrClosed {
			t.Fatalf("send: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pri send still blocked after Close")
	}
}

// TestSendUnblocksOnClose unblocks a full out queue when Close runs.
func TestSendUnblocksOnClose(t *testing.T) {
	s := NewClient()
	// Fill the out buffer so the next send blocks.
	for i := 0; i < cap(s.out); i++ {
		s.out <- wire.Frame{Cmd: wire.CmdPad}
	}
	done := make(chan error, 1)
	go func() {
		done <- s.send(wire.Frame{Cmd: wire.CmdPad})
	}()
	time.Sleep(20 * time.Millisecond)
	s.Close()
	select {
	case err := <-done:
		if err != ErrClosed {
			t.Fatalf("send: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("send still blocked after Close")
	}
}

// TestLeaveClosesSession ends Accept with ErrClosed on CmdLeave.
func TestLeaveClosesSession(t *testing.T) {
	s := NewServer()
	done := make(chan error, 1)
	go func() {
		_, err := s.Accept()
		done <- err
	}()
	s.Push([]wire.Frame{{Cmd: wire.CmdLeave}})
	select {
	case err := <-done:
		if err != ErrClosed {
			t.Fatalf("accept: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("leave did not close accept")
	}
	if !s.Closed() {
		t.Fatal("session still open")
	}
}

// TestUnknownCmdIgnored does not reset the session on an unknown command.
func TestUnknownCmdIgnored(t *testing.T) {
	s := NewServer()
	defer s.Close()
	s.Push([]wire.Frame{{Cmd: 99, StreamID: 0}, {Cmd: wire.CmdReport, Payload: wire.EncodeReport(3)}})
	if s.Closed() {
		t.Fatal("unknown cmd closed session")
	}
}

// TestOpenRetransmitKeepsOneStream ignores a second Open for the same id.
func TestOpenRetransmitKeepsOneStream(t *testing.T) {
	s := NewServer()
	defer s.Close()
	id := uint32(2)
	pl := EncodeTarget("example.com", 443)
	s.Push([]wire.Frame{{Cmd: wire.CmdOpen, StreamID: id, Payload: pl}})
	st, err := s.Accept()
	if err != nil {
		t.Fatal(err)
	}
	s.Push([]wire.Frame{{Cmd: wire.CmdOpen, StreamID: id, Payload: pl}})
	select {
	case <-s.accept:
		t.Fatal("second open accepted")
	default:
	}
	if s.StreamCount() != 1 || st.Target() != "example.com:443" {
		t.Fatalf("count=%d target=%q", s.StreamCount(), st.Target())
	}
}

// TestLeaveWithOpenStream closes the session even with a live stream.
func TestLeaveWithOpenStream(t *testing.T) {
	cli, srv := NewClient(), NewServer()
	go pump(cli, srv)
	go pump(srv, cli)
	opened := make(chan *Stream, 1)
	go func() {
		st, err := srv.Accept()
		if err != nil {
			return
		}
		opened <- st
	}()
	st, err := cli.Open("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	select {
	case <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("open")
	}
	done := make(chan error, 1)
	go func() {
		_, err := srv.Accept()
		done <- err
	}()
	srv.Push([]wire.Frame{{Cmd: wire.CmdLeave}})
	select {
	case err := <-done:
		if err != ErrClosed {
			t.Fatalf("leave: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("leave with open stream")
	}
}

// TestPullPriSkipsDraft leaves bulk Data in out.
func TestPullPriSkipsDraft(t *testing.T) {
	s := NewClient()
	bulk := make([]byte, 8<<10)
	if err := s.send(wire.Frame{Cmd: wire.CmdData, StreamID: 1, Payload: bulk}); err != nil {
		t.Fatal(err)
	}
	if err := s.send(wire.Frame{Cmd: wire.CmdPing, Payload: []byte{1, 2, 3, 4, 5, 6, 7, 8}}); err != nil {
		t.Fatal(err)
	}
	got := s.PullPri(64*1024, 0)
	if len(got) != 1 || got[0].Cmd != wire.CmdPing {
		t.Fatalf("pri %+v", got)
	}
	rest := s.Pull(64*1024, 0)
	if len(rest) != 1 || rest[0].Cmd != wire.CmdData {
		t.Fatalf("draft %+v", rest)
	}
}

// TestPullZeroWaitDoesNotMiss drains a queued frame without NewTimer(0).
func TestPullZeroWaitDoesNotMiss(t *testing.T) {
	s := NewClient()
	for i := 0; i < 200; i++ {
		if err := s.send(wire.Frame{Cmd: wire.CmdPad, Payload: []byte{byte(i)}}); err != nil {
			t.Fatal(err)
		}
		got := s.Pull(64, 0)
		if len(got) != 1 {
			t.Fatalf("iter %d: Pull(0) got %d frames (NewTimer(0) race)", i, len(got))
		}
	}
}

// TestReadDeadline returns a timeout error from Read.
func TestReadDeadline(t *testing.T) {
	st := newStream(NewClient(), 1)
	_ = st.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
	buf := make([]byte, 8)
	t0 := time.Now()
	_, err := st.Read(buf)
	if err == nil {
		t.Fatal("expected timeout")
	}
	ne, ok := err.(interface{ Timeout() bool })
	if !ok || !ne.Timeout() {
		t.Fatalf("want timeout error, got %v", err)
	}
	if time.Since(t0) > 300*time.Millisecond {
		t.Fatalf("deadline too late: %s", time.Since(t0))
	}
}

// TestKillPreservesBuffered: Close/kill with a read deadline must still
// deliver Data already in st.in (select on done used to EOF first).
func TestKillPreservesBuffered(t *testing.T) {
	st := newStream(NewClient(), 1)
	st.push([]byte("hello"))
	st.kill()
	_ = st.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(st, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q", buf)
	}
}

// pump copies Pull frames from a into b.Push until a closes.
func pump(a, b *Session) {
	for {
		fs := a.Pull(64*1024, 20*time.Millisecond)
		if len(fs) > 0 {
			b.Push(fs)
		}
		if a.closed.Load() {
			return
		}
	}
}
