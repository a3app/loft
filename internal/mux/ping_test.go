package mux

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/a3app/loft/internal/wire"
)

func TestPingTimeoutReturnsElapsed(t *testing.T) {
	s := NewClient()
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	dt, err := s.Ping(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if dt < 30*time.Millisecond {
		t.Fatalf("elapsed %v", dt)
	}
}

func TestSendCtxUnblocksFullPri(t *testing.T) {
	s := NewClient()
	defer s.Close()
	for i := 0; i < cap(s.pri); i++ {
		if err := s.send(wire.Frame{Cmd: wire.CmdPing, Payload: []byte{1}}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	err := s.sendCtx(ctx, wire.Frame{Cmd: wire.CmdPing, Payload: []byte{2}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if time.Since(t0) > time.Second {
		t.Fatal("blocked on pri")
	}
}

func TestPingSucceeds(t *testing.T) {
	s := NewClient()
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		for {
			frames := s.Pull(4096, 50*time.Millisecond)
			for _, f := range frames {
				if f.Cmd == wire.CmdPing {
					s.Push([]wire.Frame{{Cmd: wire.CmdPong, Payload: f.Payload}})
					return
				}
			}
			if s.Closed() {
				return
			}
		}
	}()
	dt, err := s.Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dt <= 0 {
		t.Fatalf("dt %v", dt)
	}
}
