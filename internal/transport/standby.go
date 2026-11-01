package transport

// Witnesses and automatic promotion of a standby controller.
//
// A standby may become the controller by itself only when the controller is
// really gone, not just when the standby's own link to it broke (then two
// controllers would run at once). Relays are the witnesses: each one tells
// every standby, every few seconds, whether it reaches the controller. A
// standby set to auto_promote takes over when it has not reached the
// controller for promote_after and more than half of the relays (at least
// two) say they do not reach it either.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/wire"
)

const (
	witnessEvery = 10 * time.Second
	witnessFresh = 30 * time.Second // reports older than this do not count
)

// PromoteHook is called once when a standby decides to take over; the node
// sets it (it records the decision and restarts in the controller role).
var PromoteHook func()

// witnessLoop sends this relay's report to every standby. Each standby is
// reached the way a client reaches a relay: TLS on its public address,
// marked with this relay's key, so the standby takes the connection instead
// of handing it to its decoy site. One connection stays open and carries a
// report every witnessEvery, give or take a fifth, so there is no new
// handshake on a fixed beat.
func witnessLoop(ctx context.Context, rt *relayRuntime, id string, key []byte, urls []string) {
	for _, u := range urls {
		go witnessTo(ctx, rt, id, key, u)
	}
}

// witnessTo keeps reporting to one standby until ctx ends.
func witnessTo(ctx context.Context, rt *relayRuntime, id string, key []byte, u string) {
	o := trunkOutbound("witness", u)
	var c net.Conn
	var br *bufio.Reader
	var host string
	defer func() {
		if c != nil {
			c.Close()
		}
	}()
	for {
		if c == nil {
			addr, sni, insecure, err := resolveTLS(o)
			if err == nil {
				dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				c, err = dialTLSALPNContext(dctx, addr, sni, "chrome", insecure, key, []string{"http/1.1"})
				cancel()
			}
			if err != nil {
				c = nil
			} else {
				br, host = bufio.NewReader(c), sni
			}
		}
		if c != nil {
			rep := wire.EncodeWitness(wire.Witness{ID: id, At: time.Now().Unix(), Sees: rt.ctlUp.Load()}, key)
			if err := postWitness(c, br, host, rep); err != nil {
				c.Close()
				c = nil
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(witnessEvery)):
		}
	}
}

// jitter is d plus or minus up to a fifth of it.
func jitter(d time.Duration) time.Duration {
	return d - d/5 + time.Duration(rand.Int64N(int64(2*d/5)+1))
}

// postWitness sends one report as an HTTP/1.1 POST and reads the answer.
func postWitness(c net.Conn, br *bufio.Reader, host string, rep []byte) error {
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	defer c.SetDeadline(time.Time{})
	req := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: %s\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n", wire.WitnessPath, host, chromeUA, len(rep))
	if _, err := c.Write(append([]byte(req), rep...)); err != nil {
		return err
	}
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("witness: %s", resp.Status)
	}
	return nil
}

// witnessBook holds the latest report of each relay.
type witnessBook struct {
	mu   sync.Mutex
	last map[string]wire.Witness
}

func (b *witnessBook) put(w wire.Witness) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last == nil {
		b.last = map[string]wire.Witness{}
	}
	if old, ok := b.last[w.ID]; !ok || w.At >= old.At {
		b.last[w.ID] = w
	}
}

// lost counts relays among voters whose fresh report says they do not
// reach the controller.
func (b *witnessBook) lost(voters []string, now time.Time) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, id := range voters {
		w, ok := b.last[id]
		if ok && !w.Sees && now.Sub(time.Unix(w.At, 0)) <= witnessFresh {
			n++
		}
	}
	return n
}

// witnessSink is a standby's side: where reports land and the copy of the
// controller's relay keys they are checked against.
type witnessSink struct {
	book    *witnessBook
	standby string
}

// witnessPrefix is how a witness request starts once TLS is off.
var witnessPrefix = []byte("POST " + wire.WitnessPath + " ")

// serve answers witness POSTs on one connection until it closes or idles
// for longer than three report periods.
func (w *witnessSink) serve(c net.Conn, br *bufio.Reader) {
	defer c.Close()
	for {
		_ = c.SetReadDeadline(time.Now().Add(3 * witnessEvery))
		req, err := http.ReadRequest(br)
		if err != nil || req.Method != http.MethodPost || req.URL.Path != wire.WitnessPath {
			return
		}
		b, err := io.ReadAll(io.LimitReader(req.Body, 512))
		req.Body.Close()
		if err != nil {
			return
		}
		if !w.take(b) {
			_, _ = io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
			return
		}
		if _, err := io.WriteString(c, "HTTP/1.1 204 No Content\r\n\r\n"); err != nil {
			return
		}
	}
}

// take checks one report against the relay keys in the standby copy and
// records it.
func (w *witnessSink) take(b []byte) bool {
	id, err := wire.WitnessID(b)
	if err != nil {
		return false
	}
	ring, err := identity.Load(filepath.Join(w.standby, "relays.keys"))
	if err != nil {
		return false
	}
	key := ring.TokenFor(id)
	if len(key) == 0 {
		return false
	}
	rep, err := wire.DecodeWitness(b, key, time.Now(), witnessFresh)
	if err != nil {
		return false
	}
	w.book.put(rep)
	return true
}

// loadRelayKeys puts the relay keys of the standby copy into RelayKeys, so
// this standby's listener recognises TLS marked with a relay key (the
// witnesses) as loft and not as a visitor for the decoy site.
func (w *witnessSink) loadRelayKeys() {
	ring, err := identity.Load(filepath.Join(w.standby, "relays.keys"))
	if err != nil || RelayKeys == nil {
		return
	}
	RelayKeys.Replace(ring)
}

// promoteDecision reports whether a standby should take over now. ctl is the
// controller's node.conf from the standby copy; self is this standby's id.
func promoteDecision(ctl *config.ServerConfig, self string, down, after time.Duration, book *witnessBook, now time.Time) bool {
	var auto bool
	var voters []string
	for _, e := range ctl.Relays {
		if e.ID == self {
			auto = e.AutoPromote
			continue
		}
		voters = append(voters, e.ID)
	}
	if !auto || down < after || len(voters) < 2 {
		return false
	}
	return book.lost(voters, now)*2 > len(voters)
}

// runStandby keeps the relay keys of the standby copy loaded for the
// witnesses and, every few seconds, decides whether to take over.
func runStandby(ctx context.Context, cfg *config.ServerConfig, rt *relayRuntime, wit *witnessSink) {
	standby, book := wit.standby, wit.book
	wit.loadRelayKeys()
	after := time.Duration(cfg.PromoteAfter) * time.Second
	if after <= 0 {
		after = 5 * time.Minute
	}
	t := time.NewTicker(witnessEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		wit.loadRelayKeys()
		b, err := os.ReadFile(filepath.Join(standby, "node.conf"))
		if err != nil {
			continue
		}
		ctl, err := config.ParseCommands(string(b))
		if err != nil {
			continue
		}
		if promoteDecision(ctl, cfg.ID, rt.downFor(), after, book, time.Now()) {
			log.Printf("loft-standby: the controller is gone for %s and most relays agree; taking over", rt.downFor().Round(time.Second))
			if PromoteHook != nil {
				PromoteHook()
			}
			return
		}
	}
}
