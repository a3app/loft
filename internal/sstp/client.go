package sstp

// SSTP client: TLS, duplex POST, SSTP control, PPP, IP packets.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/protect"
)

// Config is one SSTP VPN endpoint.
type Config struct {
	URL      string
	User     string
	Password string
	Insecure bool
	// PAPCHAP allows PAP and CHAP-MD5. Default is MSCHAPv2 only (SSTP Connect).
	PAPCHAP bool
	// SNI overrides TLS ServerName; empty uses the URL host.
	SNI string
	Log func(string)
}

// Session is a live SSTP+PPP tunnel with an assigned IPv4.
type Session struct {
	IP   net.IP
	Peer net.IP
	DNS  []net.IP
	MPPE bool

	conn    net.Conn
	mu      sync.Mutex
	ppp     *ppp
	sha256  bool
	closed  chan struct{}
	once    sync.Once
	inbound chan []byte
	log     func(string)
	mtu     int
	bw      *bufio.Writer
	// lastRxUnix is wall time of the last SSTP packet (echo or data).
	// Wall clock, not monotonic: sleep would hide a dead peer otherwise.
	lastRxUnix int64
}

// Dial opens TLS, SSTP, PPP auth (PAP/CHAP/MSCHAPv2) and IPCP.
func Dial(ctx context.Context, cfg Config) (*Session, error) {
	host, addr, query, err := sstpAddr(cfg.URL)
	if err != nil {
		return nil, err
	}
	log := cfg.Log
	if log == nil {
		log = func(string) {}
	}
	log("tls")
	sni := strings.TrimSpace(cfg.SNI)
	if sni == "" {
		sni = host
	}
	d := tls.Dialer{
		NetDialer: protect.NetDialer(15 * time.Second),
		Config: &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: cfg.Insecure,
			// SSTP on old Windows. codeql[go/insecure-tls]
			MinVersion: tls.VersionTLS10,
		},
	}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("sstp tls: %w", err)
	}
	tc, ok := c.(*tls.Conn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("sstp tls conn")
	}
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("sstp handshake: %w", err)
	}
	tuneTLS(tc)
	st := tc.ConnectionState()
	if len(st.PeerCertificates) == 0 {
		_ = c.Close()
		return nil, fmt.Errorf("sstp: no server cert")
	}
	raw := st.PeerCertificates[0].Raw
	sha1sum := sha1.Sum(raw)
	sha256sum := sha256.Sum256(raw)

	corr := newCorrelationID()
	uri := duplexPath
	if query != "" {
		uri = duplexPath + "?" + query
	}
	log("http")
	// Header order and GUID match SSTP Connect 3.9.10 (Domosekai) and MS-SSTP.
	req := "SSTP_DUPLEX_POST " + uri + " HTTP/1.1\r\n" +
		"Content-Length: 18446744073709551615\r\n" +
		"Host: " + host + "\r\n" +
		"SSTPCORRELATIONID: " + corr + "\r\n" +
		"\r\n"
	if _, err := tc.Write([]byte(req)); err != nil {
		_ = c.Close()
		return nil, err
	}
	br := bufio.NewReader(tc)
	_ = tc.SetReadDeadline(time.Now().Add(20 * time.Second))
	head, err := br.Peek(4)
	if err != nil {
		_ = c.Close()
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return nil, fmt.Errorf("sstp http timeout")
		}
		return nil, fmt.Errorf("sstp http: %w", err)
	}
	if string(head) == "HTTP" {
		statusLine, err := br.ReadString('\n')
		if err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("sstp http: %w", err)
		}
		if !strings.Contains(statusLine, " 200") {
			_ = c.Close()
			return nil, fmt.Errorf("sstp http %s", strings.TrimSpace(statusLine))
		}
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				_ = c.Close()
				return nil, err
			}
			if line == "\r\n" || line == "\n" {
				break
			}
		}
	}
	if _, err := tc.Write(connectRequest().encode()); err != nil {
		_ = c.Close()
		return nil, err
	}

	log("ack")
	s := &Session{conn: tc, ppp: newPPP(cfg.User, cfg.Password, cfg.PAPCHAP), closed: make(chan struct{}), inbound: make(chan []byte, 512), log: log}

	deadline, _ := ctx.Deadline()
	if deadline.IsZero() {
		deadline = time.Now().Add(60 * time.Second)
	}
	_ = tc.SetReadDeadline(deadline)

	var nonce []byte
	var hashMask byte
	for {
		pk, err := readPkt(br)
		if err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("sstp wait ack: %w", err)
		}
		if !pk.ctrl {
			continue
		}
		switch pk.typ {
		case msgConnectAck:
			val := attrByID(pk, attrCryptoBindingReq)
			hashMask, nonce = parseBindingReq(val)
			if nonce == nil {
				nonce = make([]byte, 32)
			}
			goto ppp
		case msgConnectNak, msgAbort:
			_ = c.Close()
			return nil, fmt.Errorf("sstp nak/abort")
		}
	}
ppp:
	log("ppp")
	s.sha256 = hashMask&hashSHA256 != 0
	s.ppp.start()
	if err := s.flushPPP(); err != nil {
		_ = c.Close()
		return nil, err
	}

	bound := false
	for !s.ppp.ipcpOpen {
		if time.Now().After(deadline) {
			_ = c.Close()
			return nil, fmt.Errorf("sstp ppp timeout")
		}
		pk, err := readPkt(br)
		if err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("sstp ppp: %w", err)
		}
		if pk.ctrl {
			switch pk.typ {
			case msgEchoRequest:
				_ = s.writePkt(echoResponse())
			case msgAbort, msgDisconnect:
				_ = c.Close()
				return nil, fmt.Errorf("sstp dropped")
			}
			continue
		}
		if err := s.ppp.handle(pk.data); err != nil {
			_ = c.Close()
			return nil, err
		}
		if s.ppp.authDone && !bound {
			cert := sha1sum[:]
			if s.sha256 {
				cert = sha256sum[:]
			}
			cmac := compoundMAC(s.ppp.hlak, nonce, cert, s.sha256)
			if err := s.writePkt(connectedPkt(nonce, cert, cmac, s.sha256)); err != nil {
				_ = c.Close()
				return nil, err
			}
			bound = true
		}
		if err := s.flushPPP(); err != nil {
			_ = c.Close()
			return nil, err
		}
	}
	if s.ppp.ip == nil {
		_ = c.Close()
		return nil, fmt.Errorf("sstp: no IP from IPCP")
	}
	s.IP = s.ppp.ip
	s.Peer = s.ppp.peer
	s.DNS = s.ppp.dns
	s.MPPE = s.ppp.mppeOn
	s.mtu = sstpInnerMTU(s.ppp.infoMTU())
	log("mtu")
	s.mtu = s.probeMTU(br, s.mtu)
	s.startCork()
	log(fmt.Sprintf("up ip=%s peer=%s dns=%s mppe=%v mtu=%d", s.IP, s.Peer, joinIPs(s.DNS), s.MPPE, s.mtu))
	_ = tc.SetReadDeadline(time.Time{})
	atomic.StoreInt64(&s.lastRxUnix, time.Now().Unix())
	go s.readLoop(br)
	go s.echoLoop()
	return s, nil
}

// MTU is the inner IPv4 MTU (LCP, 1400 cap, ICMP DF probe, runtime PMTU).
func (s *Session) MTU() int {
	s.mu.Lock()
	n := s.mtu
	s.mu.Unlock()
	if n < sstpMinMTU {
		return sstpSafeMTU
	}
	return n
}

// SetMTU lowers the inner MTU (ICMP fragmentation-needed). Never raises it.
func (s *Session) SetMTU(n int) {
	n = sstpInnerMTU(n)
	s.mu.Lock()
	if s.mtu == 0 || n < s.mtu {
		s.mtu = n
	}
	s.mu.Unlock()
}

func (s *Session) flushPPP() error {
	for _, f := range s.ppp.out {
		if err := s.writePkt(pkt{data: f}); err != nil {
			return err
		}
	}
	s.ppp.out = nil
	return nil
}

func (s *Session) writePkt(p pkt) error {
	return s.writePktFlush(p, p.ctrl)
}

func (s *Session) writePktFlush(p pkt, flush bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := p.encode()
	if s.bw != nil {
		if _, err := s.bw.Write(b); err != nil {
			return err
		}
		if flush {
			return s.bw.Flush()
		}
		return nil
	}
	_, err := s.conn.Write(b)
	return err
}

func (s *Session) startCork() {
	s.mu.Lock()
	s.bw = bufio.NewWriterSize(s.conn, 16*1024)
	s.mu.Unlock()
	go s.flushLoop()
}

func (s *Session) flushLoop() {
	t := time.NewTicker(2 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-t.C:
			s.mu.Lock()
			if s.bw != nil {
				_ = s.bw.Flush()
			}
			s.mu.Unlock()
		}
	}
}

func tuneTLS(tc *tls.Conn) {
	raw, ok := tc.NetConn().(*net.TCPConn)
	if !ok {
		return
	}
	_ = raw.SetNoDelay(true)
	_ = raw.SetReadBuffer(256 << 10)
	_ = raw.SetWriteBuffer(256 << 10)
	_ = raw.SetKeepAlive(true)
	_ = raw.SetKeepAlivePeriod(10 * time.Second)
}

func (s *Session) readLoop(br *bufio.Reader) {
	defer s.Close()
	for {
		pk, err := readPkt(br)
		if err != nil {
			s.note("read %v", err)
			return
		}
		atomic.StoreInt64(&s.lastRxUnix, time.Now().Unix())
		if pk.ctrl {
			switch pk.typ {
			case msgEchoRequest:
				_ = s.writePkt(echoResponse())
			case msgEchoResponse:
				continue
			case msgAbort, msgDisconnect:
				s.note("ctrl %d", pk.typ)
				_ = s.writePkt(pkt{ctrl: true, typ: msgDisconnectAck})
				return
			}
			continue
		}
		if ip, ok := s.ppp.unwrapIP(pk.data); ok && len(ip) > 0 {
			select {
			case s.inbound <- ip:
			case <-s.closed:
				return
			}
			continue
		}
		if err := s.ppp.handle(pk.data); err != nil {
			s.note("ppp %v", err)
			_ = s.flushPPP()
			return
		}
		_ = s.flushPPP()
	}
}

const (
	sstpEchoEvery = 30 * time.Second
	sstpEchoDead  = 75 * time.Second
	sstpEchoWrite = 8 * time.Second
)

func (s *Session) echoLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var lastSend int64
	for {
		select {
		case <-s.closed:
			return
		case <-t.C:
			now := time.Now().Unix()
			last := atomic.LoadInt64(&s.lastRxUnix)
			if last > 0 && now-last > int64(sstpEchoDead.Seconds()) {
				s.note("echo timeout")
				_ = s.Close()
				return
			}
			if lastSend > 0 && now-lastSend < int64(sstpEchoEvery.Seconds()) {
				continue
			}
			lastSend = now
			s.note("echo")
			if err := s.writeEcho(); err != nil {
				s.note("echo write %v", err)
				_ = s.Close()
				return
			}
		}
	}
}

func (s *Session) writeEcho() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(sstpEchoWrite))
	defer s.conn.SetWriteDeadline(time.Time{})
	b := echoRequest().encode()
	if s.bw != nil {
		if _, err := s.bw.Write(b); err != nil {
			return err
		}
		return s.bw.Flush()
	}
	_, err := s.conn.Write(b)
	return err
}

// WriteIP sends one IPv4/IPv6 packet into the tunnel.
func (s *Session) WriteIP(ip []byte) error {
	select {
	case <-s.closed:
		return io.ErrClosedPipe
	default:
	}
	return s.writePkt(pkt{data: s.ppp.wrapIP(ip)})
}

// ReadIP blocks for one IP packet from the tunnel.
func (s *Session) ReadIP() ([]byte, error) {
	select {
	case <-s.closed:
		return nil, io.EOF
	case p := <-s.inbound:
		return p, nil
	}
}

// Close tears down SSTP.
func (s *Session) Close() error {
	s.once.Do(func() {
		close(s.closed)
		_ = s.writePkt(disconnectPkt())
		s.mu.Lock()
		if s.bw != nil {
			_ = s.bw.Flush()
		}
		s.mu.Unlock()
		_ = s.conn.Close()
	})
	return nil
}

// Pump copies IP packets between tun (no AF header) and the SSTP session.
func (s *Session) Pump(ctx context.Context, tun io.ReadWriteCloser) error {
	errc := make(chan error, 2)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := tun.Read(buf)
			if err != nil {
				errc <- err
				return
			}
			if n == 0 {
				continue
			}
			pkt := append([]byte(nil), buf[:n]...)
			if err := s.WriteIP(pkt); err != nil {
				errc <- err
				return
			}
		}
	}()
	go func() {
		for {
			ip, err := s.ReadIP()
			if err != nil {
				errc <- err
				return
			}
			if _, err := tun.Write(ip); err != nil {
				errc <- err
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errc:
		return err
	}
}

func (s *Session) note(f string, a ...any) {
	if s.log == nil {
		return
	}
	s.log(fmt.Sprintf(f, a...))
}

func joinIPs(ips []net.IP) string {
	if len(ips) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip != nil {
			parts = append(parts, ip.String())
		}
	}
	return strings.Join(parts, ",")
}

func sstpAddr(raw string) (host, addr, query string, err error) {
	s := strings.TrimSpace(raw)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", "", "", err
	}
	host = u.Hostname()
	if host == "" {
		return "", "", "", fmt.Errorf("sstp host")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return host, net.JoinHostPort(host, port), u.RawQuery, nil
}

func newCorrelationID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("{%08X-%04X-%04X-%04X-%s}",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		strings.ToUpper(hex.EncodeToString(b[10:])))
}

// HexNonce is for tests/logs.
func HexNonce(b []byte) string { return hex.EncodeToString(b) }
