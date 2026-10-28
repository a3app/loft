package ifcfg

// netlink_linux.go is the default backend: it changes addresses, links and
// routes by talking to the kernel over a NETLINK_ROUTE socket, which is what
// the "ip" program does under the hood. The message layout lives in
// nlmsg_linux.go; this file opens the socket, sends one request, and waits
// for the kernel's answer.
//
// Each method mirrors one "ip" command and fills the request the way
// iproute2 does for that command (same table, protocol, scope, flags), so
// the kernel ends up in the same state either way. The tests in
// netlink_linux_test.go check exactly that against the real "ip".

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// Netlink returns the netlink backend.
func Netlink() Backend { return netlinkBackend{} }

type netlinkBackend struct{}

func (netlinkBackend) Name() string { return "netlink" }

// ioTimeout bounds every wait on the socket. The kernel answers route and
// address changes in microseconds; if it has not answered in this time,
// something is badly wrong and hanging loft's startup forever is worse
// than an error.
const ioTimeout = 5 * time.Second

// seqCounter numbers requests so each answer can be matched to its request.
var seqCounter atomic.Uint32

// conn is one open NETLINK_ROUTE socket.
type conn struct {
	fd  int
	pid uint32 // our port id, the "address" the kernel answers to
}

// dial opens a netlink socket. We open a fresh one per operation: loft makes
// only a few changes, at startup and when the overlay address changes, so
// the cost does not matter, and nothing is shared between goroutines.
func dial() (*conn, error) {
	// SOCK_CLOEXEC: do not leak the socket into child processes.
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("netlink socket: %w", err)
	}
	c := &conn{fd: fd}
	// Pid 0 in Bind means "kernel, pick a free port id for me".
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		c.close()
		return nil, fmt.Errorf("netlink bind: %w", err)
	}
	sa, err := unix.Getsockname(fd)
	if err != nil {
		c.close()
		return nil, fmt.Errorf("netlink getsockname: %w", err)
	}
	nl, ok := sa.(*unix.SockaddrNetlink)
	if !ok {
		c.close()
		return nil, errors.New("netlink getsockname: not a netlink address")
	}
	c.pid = nl.Pid
	tv := unix.NsecToTimeval(ioTimeout.Nanoseconds())
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv)
	return c, nil
}

func (c *conn) close() { _ = unix.Close(c.fd) }

// send finishes req, sends it to the kernel and returns its sequence number.
func (c *conn) send(req *request) (uint32, error) {
	seq := seqCounter.Add(1)
	// Pid 0 as destination = the kernel.
	if err := unix.Sendto(c.fd, req.finish(seq, c.pid), 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return 0, fmt.Errorf("netlink send: %w", err)
	}
	return seq, nil
}

// recvBufLen is big enough for one datagram of any dump: the kernel fills
// dump datagrams up to about 32 KiB.
const recvBufLen = 64 << 10

// receive reads datagrams until it has the messages for seq and passes them
// to handle one by one. handle returns done=true when the answer is
// complete. Messages for other sequence numbers or from anyone but the
// kernel are ignored: they are not ours.
func (c *conn) receive(seq uint32, handle func(m message) (done bool, err error)) error {
	buf := make([]byte, recvBufLen)
	for {
		n, _, rflags, from, err := unix.Recvmsg(c.fd, buf, nil, 0)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.EAGAIN) {
				return errors.New("netlink: no answer from the kernel in time")
			}
			return fmt.Errorf("netlink receive: %w", err)
		}
		// MSG_TRUNC: the datagram was larger than buf and the rest is
		// gone. Parsing a cut message would give garbage, so stop.
		if rflags&unix.MSG_TRUNC != 0 {
			return errors.New("netlink: answer truncated")
		}
		// Only the kernel (port id 0) may answer. Another process could
		// in principle send to our port; never take its word for it.
		if nl, ok := from.(*unix.SockaddrNetlink); !ok || nl.Pid != 0 {
			continue
		}
		msgs, err := parseMessages(buf[:n])
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if m.seq != seq || m.pid != c.pid {
				continue
			}
			done, err := handle(m)
			if err != nil || done {
				return err
			}
		}
	}
}

// do sends one change request and waits for the kernel's acknowledgement.
// The result is nil or the kernel's errno (wrapped by the caller).
func do(req *request) error {
	c, err := dial()
	if err != nil {
		return err
	}
	defer c.close()
	seq, err := c.send(req)
	if err != nil {
		return err
	}
	return c.receive(seq, func(m message) (bool, error) {
		if m.typ != unix.NLMSG_ERROR {
			// A change request gets nothing but the ack; skip anything else.
			return false, nil
		}
		errno, err := errnoOf(m)
		if err != nil {
			return true, err
		}
		if errno != 0 {
			return true, errno
		}
		return true, nil
	})
}

// dump sends a "list everything" request and collects the data messages up
// to NLMSG_DONE. Used to read addresses and routes back (tests, checks).
func dump(req *request) ([]message, error) {
	c, err := dial()
	if err != nil {
		return nil, err
	}
	defer c.close()
	seq, err := c.send(req)
	if err != nil {
		return nil, err
	}
	var out []message
	err = c.receive(seq, func(m message) (bool, error) {
		switch m.typ {
		case unix.NLMSG_DONE:
			// Newer kernels put a status in DONE too; older ones leave
			// it empty. Only a real error counts.
			if errno, e := errnoOf(m); e == nil && errno != 0 {
				return true, errno
			}
			return true, nil
		case unix.NLMSG_ERROR:
			errno, e := errnoOf(m)
			if e != nil {
				return true, e
			}
			if errno != 0 {
				return true, errno
			}
			return false, nil
		}
		// NLM_F_DUMP_INTR: the table changed while the kernel was listing
		// it, so the list may be inconsistent. The caller may retry.
		if m.flags&unix.NLM_F_DUMP_INTR != 0 {
			return true, errors.New("netlink: dump interrupted by a concurrent change, retry")
		}
		// m.data points into the receive buffer, which the next datagram
		// overwrites. A dump spans several datagrams, so keep a copy.
		m.data = append([]byte(nil), m.data...)
		out = append(out, m)
		return false, nil
	})
	return out, err
}

// ---- the Backend methods ----

func (netlinkBackend) AddrAdd(dev string, a *net.IPNet) error {
	// CREATE|EXCL: create, and fail if the address is already there,
	// which is what "ip addr add" does.
	return addrChange(unix.RTM_NEWADDR, unix.NLM_F_CREATE|unix.NLM_F_EXCL, "add", dev, a)
}

func (netlinkBackend) AddrDel(dev string, a *net.IPNet) error {
	return addrChange(unix.RTM_DELADDR, 0, "del", dev, a)
}

// addrChange adds or deletes the address a on dev.
func addrChange(typ, flags uint16, verb, dev string, a *net.IPNet) error {
	what := "addr " + verb + " " + cidrString(a) + " dev " + dev
	idx, err := ifIndex(dev)
	if err != nil {
		return fmt.Errorf("netlink %s: %w", what, err)
	}
	family, ip, err := familyBytes(a.IP)
	if err != nil {
		return fmt.Errorf("netlink %s: %w", what, err)
	}
	ones, bits := a.Mask.Size()
	if bits != len(ip)*8 {
		return fmt.Errorf("netlink %s: mask does not match the address family", what)
	}
	req := newRequest(typ, flags)
	// Scope universe ("global") is what "ip addr add" uses by default.
	req.body(ifaddrmsg(family, uint8(ones), unix.RT_SCOPE_UNIVERSE, uint32(idx)))
	// IFA_LOCAL is the interface's own address. IFA_ADDRESS is the far
	// end on point-to-point links; with no peer given, "ip" sets it to the
	// same address, and so do we, so both ways leave identical state.
	req.attr(unix.IFA_LOCAL, ip)
	req.attr(unix.IFA_ADDRESS, ip)
	if err := do(req); err != nil {
		return fmt.Errorf("netlink %s: %w", what, err)
	}
	return nil
}

func (netlinkBackend) LinkUp(dev string, mtu int) error {
	what := "link set dev " + dev + " up"
	if mtu > 0 {
		what += fmt.Sprintf(" mtu %d", mtu)
	}
	idx, err := ifIndex(dev)
	if err != nil {
		return fmt.Errorf("netlink %s: %w", what, err)
	}
	// RTM_NEWLINK on an existing interface changes it. Only the IFF_UP bit
	// is touched (change mask = IFF_UP); the MTU travels as an attribute in
	// the same message, like "ip link set ... up mtu N" sends it.
	req := newRequest(unix.RTM_NEWLINK, 0)
	req.body(ifinfomsg(int32(idx), unix.IFF_UP, unix.IFF_UP))
	if mtu > 0 {
		req.attrU32(unix.IFLA_MTU, uint32(mtu))
	}
	if err := do(req); err != nil {
		return fmt.Errorf("netlink %s: %w", what, err)
	}
	return nil
}

func (netlinkBackend) RouteAdd(r Route) error {
	return routeChange(unix.RTM_NEWROUTE, unix.NLM_F_CREATE|unix.NLM_F_EXCL, "add", r)
}

func (netlinkBackend) RouteReplace(r Route) error {
	return routeChange(unix.RTM_NEWROUTE, unix.NLM_F_CREATE|unix.NLM_F_REPLACE, "replace", r)
}

func (netlinkBackend) RouteDel(r Route) error {
	return routeChange(unix.RTM_DELROUTE, 0, "del", r)
}

// routeChange builds and sends one route request. The values below copy
// what iproute2 puts into the same request, see the comments.
func routeChange(typ, flags uint16, verb string, r Route) error {
	what := strings.Join(routeArgs(verb, r), " ")
	if r.Dst == nil {
		return fmt.Errorf("netlink %s: no destination", what)
	}
	family, dst, err := familyBytes(r.Dst.IP)
	if err != nil {
		return fmt.Errorf("netlink %s: %w", what, err)
	}
	ones, bits := r.Dst.Mask.Size()
	if bits != len(dst)*8 {
		return fmt.Errorf("netlink %s: mask does not match the address family", what)
	}

	var protocol, scope, rtype uint8
	if typ == unix.RTM_DELROUTE {
		// A delete names only what must match. Zero protocol and, for a
		// normal route, zero type and scope "nowhere" are wildcards to the
		// kernel: "delete the route to this destination, whoever added
		// it". A blackhole/unreachable delete names its type, as
		// "ip route del unreachable 2000::/3" does.
		protocol = 0
		switch r.Type {
		case Blackhole:
			rtype, scope = unix.RTN_BLACKHOLE, unix.RT_SCOPE_UNIVERSE
		case Unreachable:
			rtype, scope = unix.RTN_UNREACHABLE, unix.RT_SCOPE_UNIVERSE
		default:
			rtype, scope = unix.RTN_UNSPEC, unix.RT_SCOPE_NOWHERE
		}
	} else {
		// "boot" marks a route set by an admin tool, not by a routing
		// daemon or the kernel; it is the iproute2 default.
		protocol = unix.RTPROT_BOOT
		switch r.Type {
		case Blackhole:
			rtype, scope = unix.RTN_BLACKHOLE, unix.RT_SCOPE_UNIVERSE
		case Unreachable:
			rtype, scope = unix.RTN_UNREACHABLE, unix.RT_SCOPE_UNIVERSE
		default:
			rtype = unix.RTN_UNICAST
			// A route straight out of an interface, with no next hop,
			// reaches only hosts on that link: scope "link". With a next
			// hop it reaches anywhere: scope "universe".
			if r.Gw == nil {
				scope = unix.RT_SCOPE_LINK
			} else {
				scope = unix.RT_SCOPE_UNIVERSE
			}
		}
	}

	// The table: 0 means main. The one-byte field in rtmsg only holds
	// numbers below 256; a bigger one goes into an RTA_TABLE attribute with
	// the byte left "unspecified", exactly as iproute2 sends it.
	table, tableAttr := tableFields(r.Table)
	req := newRequest(typ, flags)
	req.body(rtmsg(family, uint8(ones), table, protocol, scope, rtype))
	if tableAttr != 0 {
		req.attrU32(unix.RTA_TABLE, tableAttr)
	}
	// The default route (prefix length 0) carries no RTA_DST at all.
	if ones > 0 {
		req.attr(unix.RTA_DST, dst)
	}
	if r.Gw != nil {
		gf, gw, err := familyBytes(r.Gw)
		if err != nil || gf != family {
			return fmt.Errorf("netlink %s: next hop is not in the route's address family", what)
		}
		req.attr(unix.RTA_GATEWAY, gw)
	}
	if r.Dev != "" {
		idx, err := ifIndex(r.Dev)
		if err != nil {
			return fmt.Errorf("netlink %s: %w", what, err)
		}
		req.attrU32(unix.RTA_OIF, uint32(idx))
	}
	if err := do(req); err != nil {
		return fmt.Errorf("netlink %s: %w", what, err)
	}
	return nil
}

// tableFields splits a table number into the one-byte header field and,
// for numbers of 256 and up, the 32-bit attribute that carries it instead.
func tableFields(t int) (byteField uint8, attr uint32) {
	switch {
	case t == 0:
		return unix.RT_TABLE_MAIN, 0
	case t < 256:
		return uint8(t), 0
	default:
		return unix.RT_TABLE_UNSPEC, uint32(t)
	}
}

func (netlinkBackend) RuleAdd(r Rule) error {
	return ruleChange(unix.RTM_NEWRULE, unix.NLM_F_CREATE|unix.NLM_F_EXCL, "add", r)
}

func (netlinkBackend) RuleDel(r Rule) error {
	return ruleChange(unix.RTM_DELRULE, 0, "del", r)
}

// ruleChange sends one policy-routing rule request (IPv4 only: loft's
// overlay is IPv4). Its fixed body, struct fib_rule_hdr, has the same
// 12-byte shape as rtmsg: family, dst_len, src_len, tos, table, two
// reserved bytes, action, flags.
func ruleChange(typ, flags uint16, verb string, r Rule) error {
	what := strings.Join(ruleArgs(verb, r), " ")
	if r.Table == 0 {
		return fmt.Errorf("netlink %s: rule needs a table", what)
	}
	table, tableAttr := tableFields(r.Table)
	// An added rule "goes to a table" (FR_ACT_TO_TBL). A delete leaves the
	// action unspecified, which matches any, like "ip rule del" does.
	var action uint8
	if typ == unix.RTM_NEWRULE {
		action = unix.FR_ACT_TO_TBL
	}
	req := newRequest(typ, flags)
	req.body(rtmsg(unix.AF_INET, 0, table, 0, 0, action))
	if tableAttr != 0 {
		req.attrU32(unix.FRA_TABLE, tableAttr)
	}
	if r.IIF != "" {
		// Interface names travel as C strings: with the closing zero byte.
		req.attr(unix.FRA_IIFNAME, append([]byte(r.IIF), 0))
	}
	if r.Priority > 0 {
		req.attrU32(unix.FRA_PRIORITY, uint32(r.Priority))
	}
	if err := do(req); err != nil {
		return fmt.Errorf("netlink %s: %w", what, err)
	}
	return nil
}

// DefaultGatewayV4 reads the IPv4 default route of the main table and
// returns its next hop and interface name, e.g. 172.31.20.1 and
// "veth-loft". Only reads, so it uses netlink whatever LOFT_NETCFG says.
// With several default routes the one with the lowest metric wins, as it
// does for the kernel.
func DefaultGatewayV4() (net.IP, string, error) {
	req := newRequest(unix.RTM_GETROUTE, unix.NLM_F_DUMP)
	req.body(rtmsg(unix.AF_INET, 0, 0, 0, 0, 0))
	msgs, err := dump(req)
	if err != nil {
		return nil, "", fmt.Errorf("netlink route dump: %w", err)
	}
	var best net.IP
	var bestDev string
	bestMetric := ^uint32(0)
	for _, m := range msgs {
		if m.typ != unix.RTM_NEWROUTE || len(m.data) < unix.SizeofRtMsg {
			continue
		}
		d := m.data
		// Only "0.0.0.0/0, normal route": dst_len 0, type unicast.
		if d[1] != 0 || d[7] != unix.RTN_UNICAST {
			continue
		}
		attrs, err := parseAttrs(d[unix.SizeofRtMsg:])
		if err != nil {
			return nil, "", err
		}
		table := uint32(d[4])
		var gw net.IP
		var oif int
		var metric uint32
		for _, a := range attrs {
			switch a.typ {
			case unix.RTA_TABLE:
				if len(a.value) >= 4 {
					table = nativeEndian.Uint32(a.value)
				}
			case unix.RTA_GATEWAY:
				gw = net.IP(append([]byte(nil), a.value...))
			case unix.RTA_OIF:
				if len(a.value) >= 4 {
					oif = int(nativeEndian.Uint32(a.value))
				}
			case unix.RTA_PRIORITY:
				if len(a.value) >= 4 {
					metric = nativeEndian.Uint32(a.value)
				}
			}
		}
		if table != unix.RT_TABLE_MAIN || gw.To4() == nil || metric >= bestMetric {
			continue
		}
		dev := ""
		if ifi, err := net.InterfaceByIndex(oif); err == nil {
			dev = ifi.Name
		}
		best, bestDev, bestMetric = gw.To4(), dev, metric
	}
	if best == nil {
		return nil, "", errors.New("no IPv4 default route with a gateway")
	}
	return best, bestDev, nil
}

// ifIndex turns an interface name into the kernel's interface number,
// which is what netlink messages carry.
func ifIndex(dev string) (int, error) {
	ifi, err := net.InterfaceByName(dev)
	if err != nil {
		return 0, err
	}
	return ifi.Index, nil
}

// familyBytes returns the address family and the raw address bytes:
// 4 bytes for IPv4, 16 for IPv6. Go often stores IPv4 in 16 bytes
// ("IPv4-mapped"); netlink wants the short form.
func familyBytes(ip net.IP) (uint8, []byte, error) {
	if v4 := ip.To4(); v4 != nil {
		return unix.AF_INET, []byte(v4), nil
	}
	if v6 := ip.To16(); v6 != nil {
		return unix.AF_INET6, []byte(v6), nil
	}
	return 0, nil, fmt.Errorf("bad IP address %v", ip)
}
