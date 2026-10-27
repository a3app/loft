package keenetic

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// Ring keeps the last journal lines, oldest first.
type Ring struct {
	mu    sync.Mutex
	lines []string
	max   int
}

// NewRing keeps up to n lines (80 when n < 1).
func NewRing(n int) *Ring {
	if n < 1 {
		n = 80
	}
	return &Ring{max: n}
}

// Add appends one line.
func (r *Ring) Add(s string) {
	if r == nil || s == "" {
		return
	}
	r.mu.Lock()
	r.lines = append(r.lines, s)
	if len(r.lines) > r.max {
		r.lines = r.lines[len(r.lines)-r.max:]
	}
	r.mu.Unlock()
}

// Lines is a copy, oldest first.
func (r *Ring) Lines() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

type kid struct {
	cmd    *exec.Cmd
	pw     io.Closer
	done   chan struct{}
	hash   string
	opkg   int
	born   time.Time
	delay  time.Duration // wait after exit before the next start
	exited time.Time
	noted  bool
}

func (k *kid) stop() {
	if k == nil {
		return
	}
	if k.cmd != nil && k.cmd.Process != nil {
		_ = k.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-k.done:
	case <-time.After(4 * time.Second):
		if k.cmd != nil && k.cmd.Process != nil {
			_ = k.cmd.Process.Kill()
		}
		<-k.done
	}
}

// Sup starts and stops session children and keeps the journal.
type Sup struct {
	Store *Store
	State string
	Exe   string
	Ring  *Ring

	mu     sync.Mutex
	kids   map[string]*kid
	update Update
}

// Reconcile matches children to the YAML: enabled connections run, the
// others are stopped (OpkgTun down, the NDM object kept). A connection
// that disappeared is removed with ndmc.
func (s *Sup) Reconcile() {
	if s.Store == nil {
		return
	}
	f, err := s.Store.Load()
	if err != nil {
		log.Printf("config: %v", err)
		return
	}
	want := map[string]Conn{}
	for _, c := range f.Connections {
		want[c.ID] = c
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kids == nil {
		s.kids = map[string]*kid{}
	}
	for id, k := range s.kids {
		c, ok := want[id]
		if !ok {
			s.removeLocked(id, k)
			continue
		}
		h := connHash(c)
		if !c.Enabled || h != k.hash {
			k.stop()
			delete(s.kids, id)
			continue
		}
		if !k.dead() {
			continue
		}
		wait := k.delay
		if wait < 10*time.Second {
			wait = 10 * time.Second
		}
		since := time.Since(k.exited)
		if k.exited.IsZero() {
			since = 0
		}
		if since < wait {
			if !k.noted {
				k.noted = true
				log.Printf("%s: stopped, retry in %s", c.Name, wait.Truncate(time.Second))
			}
			continue
		}
		next := 10 * time.Second
		if time.Since(k.born) < 30*time.Second {
			next = wait * 2
			if next > 60*time.Second {
				next = 60 * time.Second
			}
		}
		delete(s.kids, id)
		s.startLocked(c, next)
	}
	for _, c := range f.Connections {
		if !c.Enabled {
			continue
		}
		if _, ok := s.kids[c.ID]; ok {
			continue
		}
		s.startLocked(c, 0)
	}
}

func (k *kid) dead() bool {
	if k == nil {
		return true
	}
	select {
	case <-k.done:
		return true
	default:
		return false
	}
}

func (s *Sup) removeLocked(id string, k *kid) {
	k.stop()
	delete(s.kids, id)
	snap := readSnap(snapPath(s.stateDir(), id))
	lines, err := DeleteLines(Up{Index: k.opkg, ISP: snap.ISP, Hosts: snap.Hosts})
	if err != nil {
		log.Printf("ndmc %s: %v", id, err)
		return
	}
	if err := Apply(context.Background(), Bin{}, lines); err != nil {
		log.Printf("ndmc %s: %v", id, err)
	}
}

func (s *Sup) startLocked(c Conn, delay time.Duration) {
	exe := s.Exe
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			log.Printf("exe: %v", err)
			return
		}
	}
	cmd := exec.Command(exe, "-c", s.Store.Path(), "-session", c.ID, "-state", s.stateDir())
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		log.Printf("%s: %v", c.Name, err)
		_ = pw.Close()
		return
	}
	k := &kid{
		cmd: cmd, pw: pw, done: make(chan struct{}), hash: connHash(c), opkg: c.Opkg,
		born: time.Now(), delay: delay,
	}
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 256*1024)
		for sc.Scan() {
			line := Redact(sc.Text())
			fmt.Fprintln(os.Stderr, line)
			if s.Ring != nil {
				s.Ring.Add(line)
			}
		}
	}()
	go func() {
		_ = cmd.Wait()
		_ = pw.Close()
		// Write the stamp before close. A waiter that observes done
		// also observes exited (the close happens after the write).
		k.exited = time.Now()
		close(k.done)
	}()
	s.kids[c.ID] = k
	log.Printf("%s session", c.Name)
}

// Loop reconciles every few seconds and, when the YAML asks, checks
// GitHub about once every six hours. The check only stores the latest
// version. Install stays on the package button.
func (s *Sup) Loop(ctx context.Context) {
	s.Reconcile()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	nextCheck := time.Now().Add(time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			s.Reconcile()
			if now.Before(nextCheck) || !s.wantsCheck() {
				continue
			}
			nextCheck = now.Add(6 * time.Hour)
			go s.checkOnce(ctx)
		}
	}
}

func (s *Sup) wantsCheck() bool {
	if s.Store == nil {
		return false
	}
	f, err := s.Store.Load()
	if err != nil {
		return false
	}
	return f.Keenetic.CheckUpdates
}

func (s *Sup) checkOnce(ctx context.Context) {
	up, err := LatestRelease(ctx, AssetArch())
	if err != nil {
		log.Printf("update: %v", err)
		return
	}
	s.SetUpdate(up)
	if up.Ready {
		log.Printf("update %s available", up.Latest)
	}
}

// StopAll signals every child. Each child brings its OpkgTun down.
func (s *Sup) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, k := range s.kids {
		k.stop()
		delete(s.kids, id)
	}
}

// Snap reads the child's status file.
func (s *Sup) Snap(id string) Snap {
	return readSnap(snapPath(s.stateDir(), id))
}

func (s *Sup) stateDir() string {
	if s.State != "" {
		return s.State
	}
	return DefaultStateDir()
}

// SetUpdate remembers the last GitHub check.
func (s *Sup) SetUpdate(u Update) {
	s.mu.Lock()
	s.update = u
	s.mu.Unlock()
}

// LastUpdate is the last GitHub check.
func (s *Sup) LastUpdate() Update {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update
}

func connHash(c Conn) string {
	b, err := yaml.Marshal(c)
	if err != nil {
		return c.ID
	}
	return string(b)
}
