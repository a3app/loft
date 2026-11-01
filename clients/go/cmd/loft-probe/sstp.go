package main

// SSTP via for loft-probe: userspace TCP/UDP/ICMP over the PPP session.
//
// A "via" (see via.go) is the path probes take. This one dials an SSTP VPN
// server (TLS, then PPP) and runs inbound.SSTPNet, a userspace TCP/IP stack,
// on top, so probes leave through the VPN without a utun device or system
// routes. The rest of the file reads the desktop app's SSTP profile YAML.

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/sstp"

	"gopkg.in/yaml.v3"
)

// sstpVia is one SSTP session plus a gVisor stack (no utun, no system routes).
type sstpVia struct {
	sess *sstp.Session
	net  *inbound.SSTPNet
	ch   chan datagram
	once sync.Once
}

// openSSTP dials SSTP and binds the userspace stack.
func openSSTP(ctx context.Context, url, user, pass, sni string, insecure, pap, verbose bool, dnsList string, dnsReplace bool) (*sstpVia, error) {
	log := func(s string) {
		fmt.Fprintln(os.Stderr, "sstp", s)
	}
	sess, err := sstp.Dial(ctx, sstp.Config{
		URL: url, User: user, Password: pass, Insecure: insecure, PAPCHAP: pap, SNI: sni, Log: log,
	})
	if err != nil {
		return nil, err
	}
	sn, err := inbound.NewSSTPNet(sess)
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	sn.ApplyDNS(inbound.ParseDNSList(dnsList), dnsReplace)
	if verbose {
		sn.SetLog(func(f string, a ...any) {
			fmt.Fprintf(os.Stderr, "sstp: "+f+"\n", a...)
		})
	}
	v := &sstpVia{sess: sess, net: sn, ch: make(chan datagram, 64)}
	sn.SetTap(func(ip []byte) {
		if payload, addr, ok := udpFromIP(ip); ok {
			select {
			case v.ch <- datagram{addr: addr, data: payload}:
			default:
			}
			return
		}
		host := ""
		if len(ip) >= 20 && ip[0]>>4 == 4 {
			host = net.IP(ip[12:16]).String()
		}
		select {
		case v.ch <- datagram{addr: net.JoinHostPort(host, "0"), data: append([]byte(nil), ip...)}:
		default:
		}
	})
	return v, nil
}

// udpFromIP extracts the payload and the sender "ip:port" from an IPv4 UDP
// packet. ok is false for anything else. openSSTP's tap uses it to pass UDP
// answers (DNS, for example) to waitDgram.
func udpFromIP(ip []byte) ([]byte, string, bool) {
	if len(ip) < 28 || ip[0]>>4 != 4 || ip[9] != 17 {
		return nil, "", false
	}
	ihl := int(ip[0]&0xf) * 4
	if ihl < 20 || len(ip) < ihl+8 {
		return nil, "", false
	}
	src := net.IP(ip[12:16]).String()
	sport := int(binary.BigEndian.Uint16(ip[ihl : ihl+2]))
	return append([]byte(nil), ip[ihl+8:]...), net.JoinHostPort(src, strconv.Itoa(sport)), true
}

// looksIPv4 reports whether data is a whole IPv4 packet carrying ICMP, TCP
// or UDP. writeDgram sends such packets raw into the PPP link (that is how
// ICMP probes are built) and treats anything else as a UDP payload.
func looksIPv4(data []byte) bool {
	if len(data) < 20 || data[0]>>4 != 4 {
		return false
	}
	ihl := int(data[0]&0xf) * 4
	if ihl < 20 || len(data) < ihl {
		return false
	}
	switch data[9] {
	case 1, 6, 17:
		return true
	}
	return false
}

// kind names this via in the output.
func (v *sstpVia) kind() string { return "sstp" }

// canICMP is true: raw IP packets can go through PPP.
func (v *sstpVia) canICMP() bool { return true }

// overlayIP returns the address the SSTP server gave this client over PPP.
func (v *sstpVia) overlayIP() string {
	if v.sess.IP != nil {
		return v.sess.IP.String()
	}
	return ""
}

// detail describes the session for the header line: IP, PPP peer, whether
// MPPE encryption is on, and the DNS servers in use.
func (v *sstpVia) detail() string {
	s := fmt.Sprintf("ip=%s peer=%s mppe=%v", v.sess.IP, v.sess.Peer, v.sess.MPPE)
	if dns := inbound.JoinDNS(v.net.DNS()); dns != "" {
		s += " dns=" + dns
	}
	return s
}

// dialTCP opens a TCP connection through the userspace stack and applies
// the context deadline to it.
func (v *sstpVia) dialTCP(ctx context.Context, hostport string) (net.Conn, error) {
	c, err := v.net.Open(hostport)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	if la, ra := c.LocalAddr(), c.RemoteAddr(); la != nil && ra != nil {
		fmt.Fprintf(os.Stderr, "tcp local=%s remote=%s\n", la, ra)
	}
	return c, nil
}

// writeDgram sends data to addr: a whole IPv4 packet goes into PPP as is,
// anything else is sent as one UDP datagram from a fresh local port.
func (v *sstpVia) writeDgram(addr string, data []byte) error {
	if looksIPv4(data) {
		return v.sess.WriteIP(data)
	}
	c, err := v.net.DialUDP(addr)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write(data)
	return err
}

// waitDgram returns the first datagram seen by the tap for which match is
// true. It fails when ctx ends or the SSTP session closes.
func (v *sstpVia) waitDgram(ctx context.Context, match func(datagram) bool) (datagram, error) {
	for {
		select {
		case <-ctx.Done():
			return datagram{}, ctx.Err()
		case <-v.net.Done():
			return datagram{}, fmt.Errorf("sstp closed")
		case dg := <-v.ch:
			if match(dg) {
				return dg, nil
			}
		}
	}
}

// close shuts the stack and the SSTP session once; later calls do nothing.
func (v *sstpVia) close() error {
	v.once.Do(func() {
		_ = v.net.Close()
		_ = v.sess.Close()
	})
	return nil
}

// guiProfile is a loft.app SSTP YAML (username/password, not token).
type guiProfile struct {
	Token      string         `yaml:"token"`
	Username   string         `yaml:"username"`
	Password   string         `yaml:"password"`
	TLSCheck   *bool          `yaml:"tls_check"`
	PAPCHAP    bool           `yaml:"pap_chap"`
	SNI        string         `yaml:"sni"`
	DNS        interface{}    `yaml:"dns"`
	DNSReplace bool           `yaml:"dns_replace"`
	Select     string         `yaml:"select"`
	Outbounds  []yamlOutbound `yaml:"outbounds"`
}

// dnsString returns the profile's dns field as one comma-separated string;
// the YAML may hold either a string or a list.
func (p *guiProfile) dnsString() string {
	switch v := p.DNS.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, x := range v {
			switch n := x.(type) {
			case string:
				parts = append(parts, n)
			case fmt.Stringer:
				parts = append(parts, n.String())
			}
		}
		return strings.Join(parts, ",")
	default:
		return ""
	}
}

// yamlOutbound is one entry of the profile's outbounds list.
type yamlOutbound struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	URL  string `yaml:"url"`
	Path string `yaml:"path"`
	SNI  string `yaml:"sni"`
}

// loadGUIProfile reads and parses a desktop app profile YAML.
func loadGUIProfile(path string) (*guiProfile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p guiProfile
	if err := yaml.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// sstpOutbound returns the profile's SSTP outbound: the first entry of type
// sstp, or the first entry when the profile selects "sstp". ok is false when
// the profile is not an SSTP one.
func (p *guiProfile) sstpOutbound() (yamlOutbound, bool) {
	for _, o := range p.Outbounds {
		if strings.EqualFold(o.Type, "sstp") {
			return o, true
		}
	}
	if p.Select == "sstp" && len(p.Outbounds) > 0 {
		return p.Outbounds[0], true
	}
	return yamlOutbound{}, false
}

// tlsInsecure reports whether the profile turns TLS certificate checking off
// (tls_check: false). A missing field means checking stays on.
func (p *guiProfile) tlsInsecure() bool {
	if p.TLSCheck == nil {
		return false
	}
	return !*p.TLSCheck
}
