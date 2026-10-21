package transport

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func TestH2ConnectClientSendsProtocol(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- "accept " + err.Error()
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		preface := make([]byte, len(h2Preface))
		if _, err := io.ReadFull(br, preface); err != nil {
			got <- "preface " + err.Error()
			return
		}
		fr := http2.NewFramer(c, br)
		fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
		if err := fr.WriteSettings(http2.Setting{ID: http2.SettingEnableConnectProtocol, Val: 1}); err != nil {
			got <- "settings " + err.Error()
			return
		}
		proto := ""
		for i := 0; i < 12; i++ {
			f, err := fr.ReadFrame()
			if err != nil {
				got <- "frame " + err.Error()
				return
			}
			switch f := f.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					_ = fr.WriteSettingsAck()
				}
			case *http2.MetaHeadersFrame:
				for _, hf := range f.Fields {
					if hf.Name == ":protocol" {
						proto = hf.Value
					}
				}
				var hdr []byte
				enc := hpack.NewEncoder(&hpackAppend{p: &hdr})
				_ = enc.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
				_ = fr.WriteHeaders(http2.HeadersFrameParam{
					StreamID: f.StreamID, BlockFragment: hdr, EndHeaders: true,
				})
				got <- proto
				return
			}
		}
		got <- "no headers"
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := h2ExtendedConnect(ctx, c, "sni.example", "/api/v1/tunnel")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case p := <-got:
		if p != "loft" {
			t.Fatalf("protocol %q", p)
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}

type hpackAppend struct{ p *[]byte }

func (w *hpackAppend) Write(b []byte) (int, error) {
	*w.p = append(*w.p, b...)
	return len(b), nil
}
