package transport

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
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

// TestH2OwnSNIIgnoresCachedMask keeps HTTP/2 on uTLS when Status has
// already stored mask_pub and SNI is the node name. DialMask would reject
// the node leaf (serial bind).
func TestH2OwnSNIIgnoresCachedMask(t *testing.T) {
	raw := startH2L3(t, &H2Server{Path: "/api/v1/tunnel"})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pub := make([]byte, 32)
	for i := range pub {
		pub[i] = byte(i + 1)
	}
	RememberMaskPub(u.Host, pub)
	t.Cleanup(func() {
		RememberMaskPub(u.Host, nil)
		noteKEX("")
	})
	noteKEX("X25519MLKEM768")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	parent, stream, _, sni, err := dialH2CONNECT(ctx, config.Outbound{
		Type: "h2", URL: raw, Path: "/api/v1/tunnel",
		Fingerprint: "go", Insecure: true,
	}, []byte("test-token-please-change"))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	defer stream.Close()
	if sni != "127.0.0.1" {
		t.Fatalf("sni %s", sni)
	}
	if got := LastKEX(); got != "tls" {
		t.Fatalf("kex %s", got)
	}
}

// TestH2DecoySNIUsesMask still opens the mask stack when SNI is the dest.
func TestH2DecoySNIUsesMask(t *testing.T) {
	raw := startH2L3(t, &H2Server{Path: "/api/v1/tunnel"})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, _, _, _, err := dialH2CONNECT(ctx, config.Outbound{
		Type: "h2", URL: raw, Path: "/api/v1/tunnel",
		Fingerprint: "go", Insecure: true, Decoy: "origin.example",
	}, []byte("test-token-please-change"))
	if err == nil || !strings.Contains(err.Error(), "mask:") {
		t.Fatalf("got %v", err)
	}
}

type hpackAppend struct{ p *[]byte }

func (w *hpackAppend) Write(b []byte) (int, error) {
	*w.p = append(*w.p, b...)
	return len(b), nil
}
