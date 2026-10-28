package ndms

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// OpkgIndex is the range Keenetic shows as OpkgTun0 … OpkgTun15.
const OpkgMax = 15

var (
	// descBad rejects controls and characters that would break out of a
	// quoted ndmc argument or start a second command.
	descBad = regexp.MustCompile(`[\x00-\x1f"'\\;|&$` + "`]")
	// ispRe is an NDM interface name. A slash is allowed (GigabitEthernet0/0).
	ispRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._/-]{0,40}$`)
	// relRe and verRe read the major from "show version". The first match wins.
	relRe = regexp.MustCompile(`(?i)release:\s*(\d+)`)
	verRe = regexp.MustCompile(`(?i)(?:version|ndms?):\s*(\d+)`)
)

// Names is the NDM interface and the Linux device for one index.
func Names(index int) (ndm, tun string, err error) {
	if index < 0 || index > OpkgMax {
		return "", "", fmt.Errorf("opkg %d: want 0..%d", index, OpkgMax)
	}
	return fmt.Sprintf("OpkgTun%d", index), fmt.Sprintf("opkgtun%d", index), nil
}

// Up is the ndmc script that publishes one OpkgTun. Host routes are pinned
// on the ISP interface before "up". There is no default route: NDM owns it.
type Up struct {
	Index int
	Desc  string
	IP    string
	MTU   int
	ISP   string
	Hosts []string // IPv4, no mask
}

// UpLines is the ndmc command list for Up. Description is quoted.
func UpLines(u Up) ([]string, error) {
	ndm, _, err := Names(u.Index)
	if err != nil {
		return nil, err
	}
	desc, err := quoteDesc(u.Desc)
	if err != nil {
		return nil, err
	}
	ip := strings.TrimSpace(u.IP)
	if strings.Contains(ip, "/") {
		ip = strings.Split(ip, "/")[0]
	}
	if ip == "" {
		return nil, fmt.Errorf("overlay address")
	}
	mtu := u.MTU
	if mtu <= 0 {
		mtu = 1280
	}
	lines := []string{
		"interface " + ndm,
		"interface " + ndm + " description " + desc,
		"interface " + ndm + " security-level public",
		"interface " + ndm + " ip address " + ip + " 255.255.255.255",
		"interface " + ndm + " ip global auto",
		"interface " + ndm + " ip mtu " + strconv.Itoa(mtu),
		"interface " + ndm + " ip tcp adjust-mss pmtu",
	}
	pins, err := pinLines(u.ISP, u.Hosts, false)
	if err != nil {
		return nil, err
	}
	lines = append(lines, pins...)
	lines = append(lines, "interface "+ndm+" up", "system configuration save")
	return lines, nil
}

// DownLines brings the interface down and removes host pins. The NDM
// object stays, so a connection priority that names it is kept.
func DownLines(u Up) ([]string, error) {
	ndm, _, err := Names(u.Index)
	if err != nil {
		return nil, err
	}
	pins, err := pinLines(u.ISP, u.Hosts, true)
	if err != nil {
		return nil, err
	}
	lines := []string{"interface " + ndm + " down"}
	lines = append(lines, pins...)
	lines = append(lines, "system configuration save")
	return lines, nil
}

// DeleteLines is DownLines plus removal of the interface.
func DeleteLines(u Up) ([]string, error) {
	down, err := DownLines(u)
	if err != nil {
		return nil, err
	}
	ndm, _, err := Names(u.Index)
	if err != nil {
		return nil, err
	}
	// save is already last; insert "no interface" before it.
	out := append([]string{}, down[:len(down)-1]...)
	out = append(out, "no interface "+ndm, "system configuration save")
	return out, nil
}

// pinLines is "ip route A.B.C.D/32 ISP", or "no ip route" when remove is set.
// An empty ISP or an empty host list yields no lines: the interface still
// comes up, the session is just not protected from hairpin. Hosts are IPv4
// literals. A space or a shell metacharacter is rejected so one line cannot
// become two commands.
func pinLines(isp string, hosts []string, remove bool) ([]string, error) {
	if len(hosts) == 0 {
		return nil, nil
	}
	isp = strings.TrimSpace(isp)
	if isp == "" {
		return nil, nil
	}
	if !ispRe.MatchString(isp) {
		return nil, fmt.Errorf("isp %q", isp)
	}
	verb := "ip route "
	if remove {
		verb = "no ip route "
	}
	var lines []string
	seen := map[string]bool{}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if strings.ContainsAny(h, " \t;/|&") {
			return nil, fmt.Errorf("host %q", h)
		}
		lines = append(lines, verb+h+"/32 "+isp)
	}
	return lines, nil
}

// quoteDesc wraps a connection name for "interface … description".
// Empty becomes "Loft". More than 32 runes, or a character in descBad,
// is rejected. The quotes stay inside the single ndmc -c argument.
func quoteDesc(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "Loft"
	}
	if len([]rune(s)) > 32 || descBad.MatchString(s) {
		return "", fmt.Errorf("name %q", s)
	}
	return `"` + s + `"`, nil
}

// TooOld reports a KeeneticOS / NDMS major below 5. Unparsed text is not
// old: a firmware we cannot read is left for the OpkgTun commands to refuse.
func TooOld(text string) bool {
	m := relRe.FindStringSubmatch(text)
	if m == nil {
		m = verRe.FindStringSubmatch(text)
	}
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return false
	}
	return n > 0 && n < 5
}

// Runner executes one ndmc command line (the argument of ndmc -c).
type Runner interface {
	Run(ctx context.Context, line string) error
}

// Bin is the ndmc binary. Empty Path means "ndmc" on PATH.
type Bin struct{ Path string }

// Run executes one command. ndmc parses the line itself, so quotes stay
// inside the argument and the shell is not involved.
func (b Bin) Run(ctx context.Context, line string) error {
	bin := b.Path
	if bin == "" {
		bin = "ndmc"
	}
	cmd := exec.CommandContext(ctx, bin, "-c", line)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("ndmc %s: %w", line, err)
		}
		return fmt.Errorf("ndmc %s: %s", line, msg)
	}
	return nil
}

// Apply runs lines in order. A failing "ip route" pin is skipped so one
// bad host does not hide the interface. "no …" failures are skipped.
// Any other failure stops the script.
func Apply(ctx context.Context, r Runner, lines []string) error {
	if r == nil {
		return fmt.Errorf("ndmc")
	}
	for _, ln := range lines {
		if err := r.Run(ctx, ln); err != nil {
			if strings.HasPrefix(ln, "ip route ") || strings.HasPrefix(ln, "no ") {
				continue
			}
			return err
		}
	}
	return nil
}
