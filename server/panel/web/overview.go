package web

import (
	"bufio"
	"bytes"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
)

// The overview (#main) for every role that sees it: the node's role and
// fronts, the server's addresses and its load. Keys and tokens stay out.

type overviewJSON struct {
	Node   *overviewNode      `json:"node,omitempty"`
	Addrs  []admproto.NetAddr `json:"addrs"`
	Server serverInfo         `json:"server"`
}

type overviewNode struct {
	Role      string   `json:"role"`
	Listen    string   `json:"listen"`
	Advertise string   `json:"advertise"`
	Fronts    []string `json:"fronts"`
	Decoy     []string `json:"decoy"`
}

type serverInfo struct {
	OS       string  `json:"os,omitempty"`
	Arch     string  `json:"arch"`
	CPU      float64 `json:"cpu"` // percent, all cores
	MemUsed  uint64  `json:"mem_used"`
	MemTotal uint64  `json:"mem_total"`
	DiskUsed uint64  `json:"disk_used"`
	DiskSize uint64  `json:"disk_size"`
	RX       uint64  `json:"rx"` // bytes over Hours
	TX       uint64  `json:"tx"`
	Hours    float64 `json:"hours"` // the span RX/TX cover, up to 24
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	a := s.Adm(userOf(r).Login)
	out := overviewJSON{Addrs: []admproto.NetAddr{}, Server: readServer()}
	var nc admproto.NodeConfig
	if err := a.Call(r.Context(), admproto.OpNodeConfig, nil, &nc); err == nil {
		out.Node = &overviewNode{Role: nc.Role, Listen: nc.Listen, Advertise: nc.Advertise, Fronts: nc.Fronts, Decoy: nc.Decoy}
	}
	var st admproto.NetState
	if err := a.Call(r.Context(), admproto.OpNetState, nil, &st); err == nil {
		out.Addrs = append(out.Addrs, st.Addrs...)
	}
	ok(w, out)
}

func readServer() serverInfo {
	si := serverInfo{Arch: runtime.GOARCH}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
				si.OS = strings.Trim(v, `"`)
			}
		}
	}
	si.CPU = cpuPercent()
	if m := meminfo(); m["MemTotal"] > 0 {
		si.MemTotal, si.MemUsed = m["MemTotal"]*1024, (m["MemTotal"]-m["MemAvailable"])*1024
	}
	var fs syscall.Statfs_t
	if syscall.Statfs("/", &fs) == nil {
		si.DiskSize = fs.Blocks * uint64(fs.Bsize)
		si.DiskUsed = (fs.Blocks - fs.Bfree) * uint64(fs.Bsize)
	}
	si.RX, si.TX, si.Hours = traffic.day()
	return si
}

// cpuPercent is the busy share over a quarter second.
func cpuPercent() float64 {
	a, ta := cpuTimes()
	if ta == 0 {
		return 0
	}
	time.Sleep(250 * time.Millisecond)
	b, tb := cpuTimes()
	if tb <= ta {
		return 0
	}
	return 100 * float64((tb-ta)-(b-a)) / float64(tb-ta)
}

// cpuTimes is idle (with iowait) and total jiffies of all cores.
func cpuTimes() (idle, total uint64) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	l, _, _ := bytes.Cut(b, []byte("\n"))
	f := strings.Fields(string(l))
	for i, x := range f[1:] {
		n, _ := strconv.ParseUint(x, 10, 64)
		total += n
		if i == 3 || i == 4 {
			idle += n
		}
	}
	return idle, total
}

func meminfo() map[string]uint64 {
	m := map[string]uint64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), ":")
		n, _ := strconv.ParseUint(strings.Fields(v + " 0")[0], 10, 64)
		m[k] = n
	}
	return m
}

// netBytes sums the counters of every interface but lo.
func netBytes() (rx, tx uint64, okk bool) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, false
	}
	for _, l := range strings.Split(string(b), "\n")[2:] {
		name, rest, found := strings.Cut(l, ":")
		if !found || strings.TrimSpace(name) == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx, tx = rx+r, tx+t
	}
	return rx, tx, true
}

// traffic keeps a sample every 10 minutes for a day, so the overview can
// say how much went through the last 24 hours (less while the panel is new).
var traffic = &netRing{}

type netSample struct {
	at     time.Time
	rx, tx uint64
}

type netRing struct {
	once sync.Once
	mu   sync.Mutex
	s    []netSample
}

func (n *netRing) add() {
	rx, tx, ok := netBytes()
	if !ok {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	n.s = append(n.s, netSample{now, rx, tx})
	for len(n.s) > 1 && now.Sub(n.s[0].at) > 24*time.Hour {
		n.s = n.s[1:]
	}
}

func (n *netRing) day() (rx, tx uint64, hours float64) {
	n.once.Do(func() {
		n.add()
		go func() {
			for range time.Tick(10 * time.Minute) {
				n.add()
			}
		}()
	})
	crx, ctx, ok := netBytes()
	if up := uptime(); ok && up > 0 && up <= 24*time.Hour {
		return crx, ctx, up.Hours() // the counters start at boot
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !ok || len(n.s) == 0 {
		return 0, 0, 0
	}
	o := n.s[0]
	if crx < o.rx || ctx < o.tx { // counters reset (reboot)
		n.s = n.s[:0]
		return 0, 0, 0
	}
	return crx - o.rx, ctx - o.tx, time.Since(o.at).Hours()
}

func uptime() time.Duration {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.Fields(string(b) + " 0")[0], 64)
	return time.Duration(f * float64(time.Second))
}
