package transport

// RFC 8441 extended CONNECT on an already-dialed TLS conn.
// Go 1.27 net/http ClientConn rejects Header ":protocol"; write the
// HEADERS frame ourselves.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

const h2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

type h2hs struct {
	status int
	err    error
}

// h2ExtendedConnect issues CONNECT :protocol=loft on c and returns the stream.
func h2ExtendedConnect(ctx context.Context, c net.Conn, sni, path string) (net.Conn, error) {
	if path == "" {
		path = "/api/v1/tunnel"
	}
	if sni == "" {
		sni = "localhost"
	}
	br := bufio.NewReaderSize(c, 32<<10)
	bw := bufio.NewWriterSize(c, 32<<10)
	fr := http2.NewFramer(bw, br)
	fr.SetReuseFrames()
	fr.MaxHeaderListSize = 16 << 10
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if _, err := io.WriteString(c, h2Preface); err != nil {
		return nil, err
	}
	if err := fr.WriteSettings(
		http2.Setting{ID: http2.SettingInitialWindowSize, Val: h2StreamWindow},
		http2.Setting{ID: http2.SettingMaxFrameSize, Val: 1 << 20},
	); err != nil {
		return nil, err
	}
	if extra := uint32(h2ConnWindow - 65535); extra > 0 {
		if err := fr.WriteWindowUpdate(0, extra); err != nil {
			return nil, err
		}
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	streamWin, connWin := int32(65535), int32(65535)
	if standH2Win() {
		var err error
		streamWin, connWin, err = h2WaitSettingsWin(ctx, c, fr, bw)
		if err != nil {
			return nil, err
		}
	} else if err := h2WaitSettings(ctx, c, fr, bw); err != nil {
		return nil, err
	}

	st := &h2stream{c: c, fr: fr, bw: bw, br: br, sid: 1, win: streamWin, cwin: connWin}
	pr, pw := io.Pipe()
	st.inR, st.inW = pr, pw
	got := make(chan h2hs, 1)
	go st.readLoop(got)

	var hdr bytes.Buffer
	enc := hpack.NewEncoder(&hdr)
	for _, f := range []hpack.HeaderField{
		{Name: ":method", Value: "CONNECT"},
		{Name: ":protocol", Value: "loft"},
		{Name: ":scheme", Value: "https"},
		{Name: ":path", Value: path},
		{Name: ":authority", Value: sni},
	} {
		if err := enc.WriteField(f); err != nil {
			_ = st.Close()
			return nil, err
		}
	}
	st.mu.Lock()
	err := fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      1,
		BlockFragment: hdr.Bytes(),
		EndHeaders:    true,
	})
	if err == nil {
		err = bw.Flush()
	}
	st.mu.Unlock()
	if err != nil {
		_ = st.Close()
		return nil, err
	}

	select {
	case <-ctx.Done():
		_ = st.Close()
		return nil, ctx.Err()
	case r := <-got:
		if r.err != nil {
			_ = st.Close()
			return nil, r.err
		}
		if r.status != 200 {
			_ = st.Close()
			return nil, fmt.Errorf("status %d", r.status)
		}
	}
	if extra := uint32(h2StreamWindow - 65535); extra > 0 {
		st.mu.Lock()
		_ = fr.WriteWindowUpdate(1, extra)
		_ = bw.Flush()
		st.mu.Unlock()
	}
	return st, nil
}

// h2WaitSettingsWin is h2WaitSettings that also applies the peer's initial
// stream window and any connection WINDOW_UPDATE already in the preface.
func h2WaitSettingsWin(ctx context.Context, c net.Conn, fr *http2.Framer, bw *bufio.Writer) (int32, int32, error) {
	streamWin, connWin := int32(65535), int32(65535)
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetReadDeadline(dl)
		defer c.SetReadDeadline(time.Time{})
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		f, err := fr.ReadFrame()
		if err != nil {
			return 0, 0, err
		}
		switch f := f.(type) {
		case *http2.WindowUpdateFrame:
			if f.StreamID == 0 {
				connWin += int32(f.Increment)
			}
		case *http2.SettingsFrame:
			if f.IsAck() {
				continue
			}
			f.ForeachSetting(func(s http2.Setting) error {
				if s.ID == http2.SettingInitialWindowSize && s.Val >= 65535 && s.Val < 1<<31 {
					streamWin = int32(s.Val)
				}
				return nil
			})
			if err := fr.WriteSettingsAck(); err != nil {
				return 0, 0, err
			}
			if err := bw.Flush(); err != nil {
				return 0, 0, err
			}
			return streamWin, connWin, nil
		}
	}
}

func h2WaitSettings(ctx context.Context, c net.Conn, fr *http2.Framer, bw *bufio.Writer) error {
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetReadDeadline(dl)
		defer c.SetReadDeadline(time.Time{})
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := fr.ReadFrame()
		if err != nil {
			return err
		}
		sf, ok := f.(*http2.SettingsFrame)
		if !ok {
			continue
		}
		if sf.IsAck() {
			continue
		}
		if err := fr.WriteSettingsAck(); err != nil {
			return err
		}
		return bw.Flush()
	}
}

// h2stream is stream 1 DATA as a net.Conn.
type h2stream struct {
	c    net.Conn
	fr   *http2.Framer
	bw   *bufio.Writer
	br   *bufio.Reader
	sid  uint32
	inR  *io.PipeReader
	inW  *io.PipeWriter
	mu   sync.Mutex
	win  int32
	cwin int32
	dead atomic.Bool
	once sync.Once
	hs   chan h2hs
}

func (s *h2stream) readLoop(got chan h2hs) {
	s.hs = got
	send := func(r h2hs) {
		s.once.Do(func() { got <- r })
	}
	for {
		f, err := s.fr.ReadFrame()
		if err != nil {
			send(h2hs{err: err})
			_ = s.inW.CloseWithError(err)
			return
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			s.mu.Lock()
			if !f.IsAck() {
				_ = s.fr.WriteSettingsAck()
				_ = s.bw.Flush()
			}
			s.mu.Unlock()
		case *http2.PingFrame:
			if !f.IsAck() {
				s.mu.Lock()
				_ = s.fr.WritePing(true, f.Data)
				_ = s.bw.Flush()
				s.mu.Unlock()
			}
		case *http2.WindowUpdateFrame:
			s.mu.Lock()
			if f.StreamID == 0 {
				s.cwin += int32(f.Increment)
			} else if f.StreamID == s.sid {
				s.win += int32(f.Increment)
			}
			s.mu.Unlock()
		case *http2.MetaHeadersFrame:
			if f.StreamID != s.sid {
				continue
			}
			status := 0
			for _, hf := range f.Fields {
				if hf.Name == ":status" {
					_, _ = fmt.Sscanf(hf.Value, "%d", &status)
				}
			}
			send(h2hs{status: status})
			if f.StreamEnded() {
				_ = s.inW.Close()
				return
			}
		case *http2.DataFrame:
			if f.StreamID != s.sid {
				continue
			}
			if n := len(f.Data()); n > 0 {
				if _, err := s.inW.Write(f.Data()); err != nil {
					send(h2hs{err: err})
					return
				}
				s.mu.Lock()
				_ = s.fr.WriteWindowUpdate(0, uint32(n))
				_ = s.fr.WriteWindowUpdate(s.sid, uint32(n))
				_ = s.bw.Flush()
				s.mu.Unlock()
			}
			if f.StreamEnded() {
				_ = s.inW.Close()
				return
			}
		case *http2.RSTStreamFrame:
			err := h2Reset{f.ErrCode}
			send(h2hs{err: err})
			_ = s.inW.CloseWithError(err)
			return
		case *http2.GoAwayFrame:
			err := fmt.Errorf("goaway %v", f.ErrCode)
			send(h2hs{err: err})
			_ = s.inW.CloseWithError(err)
			return
		}
	}
}

// h2Reset is RST_STREAM from the peer. Inside TLS only the node or its
// terminator can send it.
type h2Reset struct{ code http2.ErrCode }

func (e h2Reset) Error() string { return fmt.Sprintf("rst %v", e.code) }

func (s *h2stream) Read(p []byte) (int, error) { return s.inR.Read(p) }

func (s *h2stream) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > 16<<10 {
			chunk = chunk[:16<<10]
		}
		if err := s.writeData(chunk); err != nil {
			return n, err
		}
		n += len(chunk)
		p = p[len(chunk):]
	}
	return n, nil
}

func (s *h2stream) writeData(p []byte) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		if s.dead.Load() {
			return net.ErrClosed
		}
		s.mu.Lock()
		if s.win >= int32(len(p)) && s.cwin >= int32(len(p)) {
			s.win -= int32(len(p))
			s.cwin -= int32(len(p))
			err := s.fr.WriteData(s.sid, false, p)
			if err == nil {
				err = s.bw.Flush()
			}
			s.mu.Unlock()
			return err
		}
		s.mu.Unlock()
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *h2stream) Close() error {
	if s.dead.Swap(true) {
		return nil
	}
	s.mu.Lock()
	_ = s.fr.WriteRSTStream(s.sid, http2.ErrCodeCancel)
	_ = s.bw.Flush()
	s.mu.Unlock()
	if s.inW != nil {
		_ = s.inW.Close()
	}
	if s.inR != nil {
		_ = s.inR.Close()
	}
	return s.c.Close()
}

func (s *h2stream) CloseWrite() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.fr.WriteData(s.sid, true, nil)
	if err == nil {
		err = s.bw.Flush()
	}
	return err
}

func (s *h2stream) LocalAddr() net.Addr              { return dummyAddr("h2") }
func (s *h2stream) RemoteAddr() net.Addr             { return dummyAddr("h2") }
func (s *h2stream) SetDeadline(time.Time) error      { return nil }
func (s *h2stream) SetReadDeadline(time.Time) error  { return nil }
func (s *h2stream) SetWriteDeadline(time.Time) error { return nil }
