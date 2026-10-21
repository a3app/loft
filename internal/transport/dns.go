package transport

// Overlay DNS stub: the node answers gateway:53 (UDP CmdDgram and TCP Open)
// from dns.upstream (DoH, DoT, or UDP). quic:// is reserved, not served.

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/a3app/loft/internal/mux"
)

// dnsPort, dnsMax, and dnsTimeout are the overlay DNS stub limits.
const (
	dnsPort    = "53"
	dnsMax     = 4096
	dnsTimeout = 5 * time.Second
)

// dnsResolver tries dns.upstream in order (https, tls, udp).
type dnsResolver struct {
	ups []dnsUp
	cli *http.Client
}

// dnsUp is one upstream: DoH URL, DoT host:port, or UDP host:port.
type dnsUp struct {
	kind string // https | tls | udp
	addr string
	sni  string
}

// newDNSResolver parses upstream URLs. Empty list uses Cloudflare DoH.
func newDNSResolver(raw []string) *dnsResolver {
	r := &dnsResolver{
		cli: &http.Client{
			Timeout: dnsTimeout,
			Transport: &http.Transport{
				Proxy:           nil,
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}},
			},
		},
	}
	for _, s := range raw {
		if u, err := parseDNSUp(s); err == nil {
			r.ups = append(r.ups, u)
		}
	}
	if len(r.ups) == 0 {
		u, _ := parseDNSUp("https://1.1.1.1/dns-query")
		r.ups = []dnsUp{u}
	}
	return r
}

// parseDNSUp accepts https:// (DoH), tls:// (DoT, default :853), or host[:port] UDP.
func parseDNSUp(s string) (dnsUp, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "https://"):
		return dnsUp{kind: "https", addr: s}, nil
	case strings.HasPrefix(s, "tls://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return dnsUp{}, fmt.Errorf("tls")
		}
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			host, port = u.Host, "853"
		}
		return dnsUp{kind: "tls", addr: net.JoinHostPort(host, port), sni: host}, nil
	case strings.HasPrefix(s, "quic://"):
		return dnsUp{}, fmt.Errorf("quic reserved")
	case strings.Contains(s, "://"):
		return dnsUp{}, fmt.Errorf("scheme")
	}
	host := s
	port := "53"
	if h, p, err := net.SplitHostPort(s); err == nil {
		host, port = h, p
	}
	return dnsUp{kind: "udp", addr: net.JoinHostPort(host, port)}, nil
}

const dnsTypeAAAA = 28

// Resolve tries each upstream and returns SERVFAIL if none answer.
// AAAA is NODATA while overlay IPv6 is off, so clients do not Happy-Eyeballs
// out the node's native IPv6 and show that address as "our IP".
func (r *dnsResolver) Resolve(q []byte) []byte {
	if r == nil || len(q) < 12 || len(q) > dnsMax {
		return dnsFail(q)
	}
	if dnsQType(q) == dnsTypeAAAA && OverlayIPv6() == "" {
		return dnsNoData(q)
	}
	for _, u := range r.ups {
		var ans []byte
		var err error
		switch u.kind {
		case "https":
			ans, err = r.doH(u.addr, q)
		case "tls":
			ans, err = dnsOverTLS(u.addr, u.sni, q)
		default:
			ans, err = dnsOverUDP(u.addr, q)
		}
		if err != nil || len(ans) < 12 {
			continue
		}
		return ans
	}
	return dnsFail(q)
}

// doH POSTs application/dns-message to a DoH URL.
func (r *dnsResolver) doH(rawURL string, q []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(q))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	res, err := r.cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("doh %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, dnsMax))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// dnsOverTLS exchanges one length-prefixed query on DoT.
func dnsOverTLS(addr, sni string, q []byte) ([]byte, error) {
	d := &net.Dialer{Timeout: dnsTimeout}
	c, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: sni})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(dnsTimeout))
	return dnsTCPExchange(c, q)
}

// dnsOverUDP sends one UDP query and reads one reply.
func dnsOverUDP(addr string, q []byte) ([]byte, error) {
	c, err := net.DialTimeout("udp", addr, dnsTimeout)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(dnsTimeout))
	if _, err := c.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, dnsMax)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// dnsTCPExchange writes u16be length || query and reads one response.
func dnsTCPExchange(c net.Conn, q []byte) ([]byte, error) {
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(q)))
	if _, err := c.Write(hdr[:]); err != nil {
		return nil, err
	}
	if _, err := c.Write(q); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n < 12 || n > dnsMax {
		return nil, fmt.Errorf("dns len %d", n)
	}
	out := make([]byte, n)
	if _, err := io.ReadFull(c, out); err != nil {
		return nil, err
	}
	return out, nil
}

// dnsQType is the first question type, or 0 if the query is truncated.
func dnsQType(q []byte) uint16 {
	if len(q) < 12 {
		return 0
	}
	off := 12
	for off < len(q) {
		l := int(q[off])
		if l == 0 {
			off++
			break
		}
		if l >= 0xc0 {
			off += 2
			break
		}
		off += 1 + l
	}
	if off+2 > len(q) {
		return 0
	}
	return binary.BigEndian.Uint16(q[off : off+2])
}

// dnsNoData is QR=1, rcode 0, no answers (IPv4-only overlay).
func dnsNoData(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	out := append([]byte(nil), q...)
	flags := binary.BigEndian.Uint16(q[2:4])
	flags |= 0x8000
	flags &= 0xfff0
	binary.BigEndian.PutUint16(out[2:4], flags)
	binary.BigEndian.PutUint16(out[6:8], 0)
	binary.BigEndian.PutUint16(out[8:10], 0)
	binary.BigEndian.PutUint16(out[10:12], 0)
	return out
}

// dnsFail copies q into a SERVFAIL response (QR set, rcode 2).
func dnsFail(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	out := append([]byte(nil), q...)
	flags := binary.BigEndian.Uint16(q[2:4])
	flags |= 0x8000
	flags = (flags & 0xfff0) | 2
	binary.BigEndian.PutUint16(out[2:4], flags)
	return out
}

// IsDNS reports whether host is the overlay gateway (Status dns / :53).
func (o *Overlay) IsDNS(host string) bool {
	if o == nil || o.N() == nil {
		return false
	}
	ip := net.ParseIP(host)
	gw := net.ParseIP(o.N().Gateway)
	return ip != nil && gw != nil && ip.Equal(gw)
}

// ResolveDNS answers a wire-format query via dns.upstream.
func (o *Overlay) ResolveDNS(q []byte) []byte {
	if o == nil || o.dns.Load() == nil {
		return dnsFail(q)
	}
	return o.dns.Load().Resolve(q)
}

// DeliverDNS answers a UDP DNS datagram and writes the reply to addr.
func (o *Overlay) DeliverDNS(from *mux.Session, addr string, q []byte) {
	if from == nil {
		return
	}
	ans := o.ResolveDNS(q)
	if len(ans) == 0 {
		return
	}
	_ = from.WriteDgram(addr, ans)
}

// serveDNSTCP answers length-prefixed DNS on an overlay TCP Open to gateway:53.
func (o *Overlay) serveDNSTCP(st *mux.Stream) {
	defer st.Close()
	for {
		var hdr [2]byte
		if _, err := io.ReadFull(st, hdr[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[:]))
		if n < 12 || n > dnsMax {
			return
		}
		q := make([]byte, n)
		if _, err := io.ReadFull(st, q); err != nil {
			return
		}
		ans := o.ResolveDNS(q)
		if len(ans) == 0 {
			return
		}
		binary.BigEndian.PutUint16(hdr[:], uint16(len(ans)))
		if _, err := st.Write(hdr[:]); err != nil {
			return
		}
		if _, err := st.Write(ans); err != nil {
			return
		}
	}
}
