package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a3app/loft/internal/cdn"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

func TestFrontTuneCalmLowersInflight(t *testing.T) {
	ft := newFrontTune(cdn.Profile{HeaderBudget: 16 << 10, Inflight: 16})
	for i := 0; i < 2; i++ {
		ft.observe(wire.TuneCalm)
		if _, ok := ft.takeReply(); ok {
			t.Fatal("early")
		}
	}
	ft.observe(wire.TuneCalm)
	f, ok := ft.takeReply()
	if !ok {
		t.Fatal("reply")
	}
	got, ok := wire.ParseTune(f.Payload)
	if !ok || got.Event != wire.TuneSet || got.Inflight != 8 || got.Budget != 16<<10 {
		t.Fatalf("%+v", got)
	}
	b, in := ft.advertised()
	if b != 16<<10 || in != 8 {
		t.Fatalf("adv %d %d", b, in)
	}
}

func TestFrontTune404LowersInflight(t *testing.T) {
	ft := newFrontTune(cdn.Profile{HeaderBudget: 16 << 10, Inflight: 16})
	for i := 0; i < 2; i++ {
		ft.observe(wire.Tune404)
		if _, ok := ft.takeReply(); ok {
			t.Fatal("early")
		}
	}
	ft.observe(wire.Tune404)
	f, ok := ft.takeReply()
	if !ok {
		t.Fatal("reply")
	}
	got, ok := wire.ParseTune(f.Payload)
	if !ok || got.Event != wire.TuneSet || got.Inflight != 8 || got.Budget != 16<<10 {
		t.Fatalf("%+v", got)
	}
}

func TestFrontTune413HalvesBudget(t *testing.T) {
	ft := newFrontTune(cdn.Profile{HeaderBudget: 16 << 10, Inflight: 16})
	ft.observe(wire.Tune413)
	f, ok := ft.takeReply()
	if !ok {
		t.Fatal("reply")
	}
	got, _ := wire.ParseTune(f.Payload)
	if got.Budget != 8<<10 || got.Inflight != 16 {
		t.Fatalf("%+v", got)
	}
}

func TestFrontTuneNoReplyWithoutEvent(t *testing.T) {
	ft := newFrontTune(cdn.Profile{HeaderBudget: 16 << 10, Inflight: 16})
	ft.recover()
	if _, ok := ft.takeReply(); ok {
		t.Fatal("quiet")
	}
}

func TestFrontTuneRecoverAfterQuiet(t *testing.T) {
	ft := newFrontTune(cdn.Profile{HeaderBudget: 16 << 10, Inflight: 16})
	ft.observe(wire.TuneCalm)
	ft.observe(wire.TuneCalm)
	ft.observe(wire.TuneCalm)
	if _, ok := ft.takeReply(); !ok {
		t.Fatal("drop")
	}
	ft.mu.Lock()
	ft.quietAt = time.Now().Add(-3 * time.Minute)
	ft.mu.Unlock()
	ft.recover()
	f, ok := ft.takeReply()
	if !ok {
		t.Fatal("recover")
	}
	got, _ := wire.ParseTune(f.Payload)
	if got.Inflight != 16 {
		t.Fatalf("inflight %d", got.Inflight)
	}
}

func TestQueueNoteOnce(t *testing.T) {
	g := &GET{}
	g.budget.Store(12 << 10)
	g.liveInflight.Store(16)
	g.queueNote(wire.TuneCalm)
	g.queueNote(wire.Tune413)
	fs := g.takeNoteFrame()
	if len(fs) != 1 {
		t.Fatalf("n=%d", len(fs))
	}
	got, ok := wire.ParseTune(fs[0].Payload)
	if !ok || got.Event != wire.TuneCalm || got.Inflight != 16 || got.Budget != 12<<10 {
		t.Fatalf("%+v", got)
	}
	if fs := g.takeNoteFrame(); fs != nil {
		t.Fatal("second")
	}
}

func TestApplyTune(t *testing.T) {
	g := &GET{}
	g.budget.Store(16 << 10)
	g.liveInflight.Store(16)
	g.applyTune(wire.Tune{Event: wire.TuneSet, Inflight: 8, Budget: 12 << 10})
	if g.budget.Load() != 12<<10 || g.liveInflight.Load() != 8 {
		t.Fatalf("budget=%d inflight=%d", g.budget.Load(), g.liveInflight.Load())
	}
	g.applyTune(wire.Tune{Event: wire.TuneCalm, Inflight: 4, Budget: 1024})
	if g.budget.Load() != 12<<10 || g.liveInflight.Load() != 8 {
		t.Fatal("event ignored")
	}
}

func TestApplyTuneH2Conns(t *testing.T) {
	g := &GET{}
	g.liveH2.Store(1)
	g.applyTune(wire.Tune{Event: wire.TuneSet, H2Conns: 2})
	if g.liveH2.Load() != 2 {
		t.Fatalf("h2 %d", g.liveH2.Load())
	}
	g.applyTune(wire.Tune{Event: wire.TuneSet, H2Conns: 0})
	if g.liveH2.Load() != 2 {
		t.Fatal("zero leaves")
	}
}

// TestGETStatusAdvertisesYandexInflight: the node's Status ceiling is taken
// as min(client, node). A Yandex-profile server must advertise 16, so a
// client configured higher lands on 16, not the generic 8.
func TestGETStatusAdvertisesYandexInflight(t *testing.T) {
	const token = "test-token-please-change"
	gs := NewGETServerProfile(token, "/static/v3/build", cdn.Yandex())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		gs.ServeHTTP(rec, r)
		for k, vv := range rec.Header() {
			if k == "X-Session" {
				continue
			}
			w.Header()[k] = vv
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	defer ts.Close()
	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 48, Insecure: true,
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
	if v := g.liveInflight.Load(); v != 16 {
		t.Fatalf("liveInflight=%d want 16 (yandex ceiling)", v)
	}
}
