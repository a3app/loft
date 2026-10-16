package transport

// Node session trace: GET seq, puller, 404, silence. Off unless SetSessTrace.
// No payloads, no member keys; sid is 8 hex. Join with a client drive log on
// seq + UTC. Does not change status codes or body lengths.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/wire"
)

// SessTrace writes one-line session events to a writer (file, or stdout for "-").
type SessTrace struct {
	mu sync.Mutex
	w  io.Writer
	f  *os.File
	n  int
}

var sessTrace atomic.Pointer[SessTrace]

// SetSessTrace installs the node session tracer. Nil clears it.
func SetSessTrace(t *SessTrace) { sessTrace.Store(t) }

// NewSessTrace writes timestamped events to w.
func NewSessTrace(w io.Writer) *SessTrace {
	return &SessTrace{w: w}
}

// OpenSessTrace appends to path. "-" is stdout. Empty or "off" is nil.
func OpenSessTrace(path string) (*SessTrace, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "off" {
		return nil, nil
	}
	if path == "-" {
		return &SessTrace{w: os.Stdout}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &SessTrace{w: f, f: f}, nil
}

// Close flushes the log file.
func (t *SessTrace) Close() error {
	if t == nil || t.f == nil {
		return nil
	}
	return t.f.Close()
}

// Event writes one line: RFC3339 ms, kind, then key=value fields.
func (t *SessTrace) Event(kind string, kv ...string) {
	if t == nil {
		return
	}
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	var b strings.Builder
	b.WriteString(ts)
	b.WriteByte(' ')
	b.WriteString(kind)
	for _, p := range kv {
		p = strings.ReplaceAll(p, "\n", " ")
		p = strings.ReplaceAll(p, "\r", " ")
		if p == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(p)
	}
	b.WriteByte('\n')
	line := b.String()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.w != nil {
		_, _ = io.WriteString(t.w, line)
	}
	t.n++
	flush := kind == "HS" || kind == "DROP" || kind == "GAP" || kind == "MISS" || t.n%32 == 0
	if flush && t.f != nil {
		_ = t.f.Sync()
	}
}

func sessEvent(kind string, kv ...string) {
	if t := sessTrace.Load(); t != nil {
		t.Event(kind, kv...)
	}
}

func tracing() bool { return sessTrace.Load() != nil }

func shortSID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func h2flag(r *http.Request) string {
	if r != nil && r.ProtoMajor >= 2 {
		return "1"
	}
	return "0"
}

func remoteKV(r *http.Request) string {
	if r == nil {
		return "remote=-"
	}
	return "remote=" + r.RemoteAddr
}

var cmdName = [...]string{
	wire.CmdOpen:   "open",
	wire.CmdData:   "data",
	wire.CmdClose:  "close",
	wire.CmdPing:   "ping",
	wire.CmdPong:   "pong",
	wire.CmdPad:    "pad",
	wire.CmdReset:  "reset",
	wire.CmdDgram:  "dgram",
	wire.CmdLeave:  "leave",
	wire.CmdTune:   "tune",
	wire.CmdReport: "report",
}

func traceCmds(frames []wire.Frame) string {
	if len(frames) == 0 {
		return ""
	}
	var seen [12]bool
	var b strings.Builder
	for _, f := range frames {
		if int(f.Cmd) >= len(cmdName) || cmdName[f.Cmd] == "" || seen[f.Cmd] {
			continue
		}
		seen[f.Cmd] = true
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(cmdName[f.Cmd])
	}
	return b.String()
}

func traceHS(ss *serverSess, r *http.Request, seq uint64) {
	if !tracing() {
		return
	}
	sid := ""
	if ss != nil {
		sid = ss.id
		ss.mu.Lock()
		ss.lastGET = time.Now()
		ss.mu.Unlock()
	}
	sessEvent("HS", "sid="+shortSID(sid), fmt.Sprintf("seq=%d", seq), "h2="+h2flag(r), remoteKV(r))
}

func traceMISS(sid string, seq uint64, reason string, r *http.Request) {
	if !tracing() {
		return
	}
	sessEvent("MISS", "sid="+shortSID(sid), fmt.Sprintf("seq=%d", seq), "reason="+reason, "h2="+h2flag(r), remoteKV(r))
}

func traceGET(ss *serverSess, r *http.Request, seq uint64, frames []wire.Frame, via string, pulled bool, wait time.Duration) {
	if !tracing() {
		return
	}
	sid := ""
	var held, skip int
	var next, down, replied uint64
	var gap time.Duration
	now := time.Now()
	if ss != nil {
		ss.mu.Lock()
		sid = ss.id
		held, skip = len(ss.held), len(ss.skipPull)
		next, down, replied = ss.nextSeq, ss.downSeq, ss.replied
		if !ss.lastGET.IsZero() {
			gap = now.Sub(ss.lastGET)
		}
		ss.lastGET = now
		ss.mu.Unlock()
	}
	if gap >= 2*time.Second {
		sessEvent("GAP", "sid="+shortSID(sid), "silent="+driveMs(gap), fmt.Sprintf("last_seq=%d", replied), fmt.Sprintf("next=%d", seq))
	}
	kv := []string{
		"sid=" + shortSID(sid),
		fmt.Sprintf("seq=%d", seq),
		"via=" + via,
		fmt.Sprintf("pull=%d", btoi(pulled)),
		fmt.Sprintf("held=%d", held),
		fmt.Sprintf("skip=%d", skip),
		fmt.Sprintf("next=%d", next),
		fmt.Sprintf("down=%d", down),
	}
	if wait > 0 {
		kv = append(kv, "wait="+driveMs(wait))
	}
	if gap > 0 {
		kv = append(kv, "gap="+driveMs(gap))
	}
	if cmds := traceCmds(frames); cmds != "" {
		kv = append(kv, "cmds="+cmds)
	}
	kv = append(kv, "h2="+h2flag(r), remoteKV(r))
	sessEvent("GET", kv...)
}

func viaAfterPull(ss *serverSess, seq uint64, pulled bool, replayAfter bool) string {
	if pulled {
		return "ingest"
	}
	if replayAfter {
		return "replay"
	}
	if ss == nil {
		return "wait"
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if seq > 0 && seq < ss.downSeq {
		return "old"
	}
	if ss.skipPull != nil {
		if _, ok := ss.skipPull[seq]; ok {
			return "skip"
		}
	}
	if _, ok := ss.held[seq]; ok {
		return "held"
	}
	return "wait"
}
