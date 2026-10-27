package auth

import (
	"net"
	"sync"
	"time"
)

// limit.go is the brute-force guard of the panel sign-in: it counts failed
// logins per client address and blocks an address that fails too often.
// The panel's "guard" settings page changes its limits through Set.

// Limiter blocks an address after Fails failed logins within Window, for Block.
// It also slows every failure a little so guessing is expensive even below
// the limit (the caller sleeps Delay).
type Limiter struct {
	Fails  int
	Window time.Duration
	Block  time.Duration
	Delay  time.Duration
	now    func() time.Time
	mu     sync.Mutex
	m      map[string]*tries
	allow  []*net.IPNet
}

// Set changes the limits (the guard's settings); a negative block is for
// good. Addresses in allow are never blocked.
func (l *Limiter) Set(fails int, window, block time.Duration, allow []*net.IPNet) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if block < 0 {
		block = 100 * 365 * 24 * time.Hour
	}
	l.Fails, l.Window, l.Block, l.allow = fails, window, block, allow
}

// allowed reports whether ip is inside one of the allow networks, which are
// never counted or blocked. The caller holds l.mu.
func (l *Limiter) allowed(ip string) bool {
	a := net.ParseIP(ip)
	for _, n := range l.allow {
		if a != nil && n.Contains(a) {
			return true
		}
	}
	return false
}

// tries is one address's record: the times of recent failures (at) and,
// when blocked, the moment the block ends (until).
type tries struct {
	at    []time.Time
	until time.Time
}

// NewLimiter is 5 failures in 10 minutes → 24 hours, 1 s per failure.
func NewLimiter() *Limiter {
	return &Limiter{Fails: 5, Window: 10 * time.Minute, Block: 24 * time.Hour, Delay: time.Second, now: time.Now, m: map[string]*tries{}}
}

// Blocked reports whether ip may not try now, and until when.
func (l *Limiter) Blocked(ip string) (bool, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.m[ip]
	if t == nil || !l.now().Before(t.until) || l.allowed(ip) {
		return false, time.Time{}
	}
	return true, t.until
}

// Fail records a failed login; true when this one triggers a block.
func (l *Limiter) Fail(ip string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.allowed(ip) {
		return false
	}
	t := l.m[ip]
	if t == nil {
		if len(l.m) >= maxKeys {
			l.pruneLocked(now)
		}
		t = &tries{}
		l.m[ip] = t
	}
	keep := t.at[:0]
	for _, x := range t.at {
		if now.Sub(x) < l.Window {
			keep = append(keep, x)
		}
	}
	t.at = append(keep, now)
	if len(t.at) >= l.Fails {
		t.until = now.Add(l.Block)
		t.at = nil
		return true
	}
	return false
}

// maxKeys bounds the tables so random logins or addresses cannot grow memory.
const maxKeys = 10000

// pruneLocked drops idle entries; if all are live, it drops all but blocks.
func (l *Limiter) pruneLocked(now time.Time) {
	for k, t := range l.m {
		if now.After(t.until) && (len(t.at) == 0 || now.Sub(t.at[len(t.at)-1]) >= l.Window) {
			delete(l.m, k)
		}
	}
	if len(l.m) >= maxKeys {
		for k, t := range l.m {
			if !now.Before(t.until) {
				delete(l.m, k)
			}
		}
	}
}

// OK clears failures after a successful login.
func (l *Limiter) OK(ip string) {
	l.mu.Lock()
	delete(l.m, ip)
	l.mu.Unlock()
}

// Slow is for logins whose real address is unknown (behind a local proxy that
// does not pass it): a block would lock out everyone, so failures per login
// only add delay — 1 s per recent failure, at most Max — which never locks the
// owner out but makes guessing pointless.
type Slow struct {
	Window time.Duration
	Max    time.Duration
	now    func() time.Time
	mu     sync.Mutex
	m      map[string][]time.Time
}

// NewSlow is a 15 minute memory and a 30 second cap.
func NewSlow() *Slow {
	return &Slow{Window: 15 * time.Minute, Max: 30 * time.Second, now: time.Now, m: map[string][]time.Time{}}
}

// Fail records a failure for key and returns the delay to apply now.
func (s *Slow) Fail(key string) time.Duration {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; !ok && len(s.m) >= maxKeys {
		for k, ts := range s.m {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= s.Window {
				delete(s.m, k)
			}
		}
		if len(s.m) >= maxKeys {
			s.m = map[string][]time.Time{}
		}
	}
	keep := s.m[key][:0]
	for _, t := range s.m[key] {
		if now.Sub(t) < s.Window {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	s.m[key] = keep
	d := time.Duration(len(keep)) * time.Second
	if d > s.Max {
		d = s.Max
	}
	return d
}

// OK forgets failures of key.
func (s *Slow) OK(key string) {
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
}
