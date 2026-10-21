package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

func TestShortSID(t *testing.T) {
	if shortSID("") != "-" {
		t.Fatal(shortSID(""))
	}
	if shortSID("new") != "new" {
		t.Fatal(shortSID("new"))
	}
	id := "c0ffee12deadbeef"
	if shortSID(id) != "c0ffee12" {
		t.Fatal(shortSID(id))
	}
}

func TestTraceCmds(t *testing.T) {
	got := traceCmds([]wire.Frame{{Cmd: wire.CmdPad}, {Cmd: wire.CmdPing}, {Cmd: wire.CmdPad}})
	if got != "pad,ping" {
		t.Fatal(got)
	}
}

func TestSessTraceEventLine(t *testing.T) {
	var buf bytes.Buffer
	tr := NewSessTrace(&buf)
	tr.Event("GET", "sid=ab12cd34", "seq=9", "via=ingest")
	s := buf.String()
	if !strings.Contains(s, " GET sid=ab12cd34 seq=9 via=ingest\n") {
		t.Fatal(s)
	}
}

func TestOpenSessTraceOff(t *testing.T) {
	tr, err := OpenSessTrace("")
	if err != nil || tr != nil {
		t.Fatalf("%v %v", tr, err)
	}
	tr, err = OpenSessTrace("off")
	if err != nil || tr != nil {
		t.Fatalf("%v %v", tr, err)
	}
}

func TestOpenSessTraceFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sess.log")
	tr, err := OpenSessTrace(p)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.Event("HS", "sid=ab12cd34", "seq=1")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), " HS sid=ab12cd34 seq=1\n") {
		t.Fatal(string(b))
	}
}

func TestSessTraceHandshakeAndGET(t *testing.T) {
	var buf bytes.Buffer
	SetSessTrace(NewSessTrace(&buf))
	t.Cleanup(func() { SetSessTrace(nil) })

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()

	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping-get")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 8)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}

	log := buf.String()
	if !strings.Contains(log, " HS ") {
		t.Fatalf("no HS: %s", log)
	}
	if !strings.Contains(log, " GET ") || !strings.Contains(log, "seq=") {
		t.Fatalf("no GET seq: %s", log)
	}
	if strings.Contains(log, token) {
		t.Fatal("token leaked")
	}
	if strings.Contains(log, "session=") {
		t.Fatal("cookie leaked")
	}
	if !strings.Contains(log, "sid=") {
		t.Fatal(log)
	}
	for _, line := range strings.Split(log, "\n") {
		if !strings.Contains(line, "sid=") {
			continue
		}
		i := strings.Index(line, "sid=")
		rest := line[i+4:]
		if sp := strings.IndexByte(rest, ' '); sp >= 0 {
			rest = rest[:sp]
		}
		if rest == "-" || rest == "new" {
			continue
		}
		if len(rest) != 8 {
			t.Fatalf("sid %q want 8 hex in %s", rest, line)
		}
	}
}

func TestSessTraceMissNosid(t *testing.T) {
	var buf bytes.Buffer
	SetSessTrace(NewSessTrace(&buf))
	t.Cleanup(func() { SetSessTrace(nil) })

	gs := NewGETServer("test-token-please-change", "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/static/v3/build/deadbeefdeadbeefdeadbeef/9.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
	s := buf.String()
	if !strings.Contains(s, " MISS ") || !strings.Contains(s, "reason=nosid") || !strings.Contains(s, "seq=9") {
		t.Fatal(s)
	}
	if !strings.Contains(s, "sid=deadbeef") {
		t.Fatal(s)
	}
}
