package inbound

// socks5_test.go covers IPv4-mapped hosts, native IPv6 reject, and no RST on origin close.

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
)

// TestCanonSOCKSHost maps IPv4-mapped IPv6 to IPv4 and rejects native IPv6.
func TestCanonSOCKSHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"example.com", "example.com", true},
		{"1.2.3.4", "1.2.3.4", true},
		{"::ffff:188.40.167.81", "188.40.167.81", true},
		{"::FFFF:8.8.8.8", "8.8.8.8", true},
		{"2001:db8::1", "", false},
		{"::1", "", false},
	}
	for _, c := range cases {
		got, ok := canonSOCKSHost(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("%s → %q %v want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// recDialer records Open targets and dials a local echo listener.
type recDialer struct {
	mu      sync.Mutex
	targets []string
	ln      net.Listener
}

// Open records target and dials r.ln.
func (r *recDialer) Open(target string) (net.Conn, error) {
	r.mu.Lock()
	r.targets = append(r.targets, target)
	r.mu.Unlock()
	return net.Dial("tcp", r.ln.Addr().String())
}

// Session is unused in these SOCKS tests.
func (r *recDialer) Session() *mux.Session { return nil }

// Done is unused in these SOCKS tests.
func (r *recDialer) Done() <-chan struct{} { return nil }

// Close is a no-op.
func (r *recDialer) Close() error { return nil }

// TestSOCKSMappedIPv6OpensIPv4 CONNECTs ::ffff:1.2.3.4 as 1.2.3.4:80.
func TestSOCKSMappedIPv6OpensIPv4(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, err := echo.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.Copy(c, c)
	}()
	d := &recDialer{ln: echo}
	ln, err := ListenSOCKS5("127.0.0.1:0", d)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	rep := make([]byte, 2)
	if _, err := io.ReadFull(c, rep); err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP("::ffff:1.2.3.4").To16()
	req := []byte{0x05, 0x01, 0x00, 0x04}
	req = append(req, ip...)
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], 80)
	req = append(req, pb[:]...)
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 10)
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr[1] != 0 {
		t.Fatalf("socks %d", hdr[1])
	}
	time.Sleep(50 * time.Millisecond)
	d.mu.Lock()
	got := append([]string(nil), d.targets...)
	d.mu.Unlock()
	if len(got) != 1 || got[0] != "1.2.3.4:80" {
		t.Fatalf("open %v", got)
	}
}

// TestSOCKSNativeIPv6Rejected returns ATYP not supported (0x08) for 2001:db8::1.
func TestSOCKSNativeIPv6Rejected(t *testing.T) {
	d := &recDialer{}
	ln, err := ListenSOCKS5("127.0.0.1:0", d)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	rep := make([]byte, 2)
	if _, err := io.ReadFull(c, rep); err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP("2001:db8::1").To16()
	req := []byte{0x05, 0x01, 0x00, 0x04}
	req = append(req, ip...)
	req = append(req, 1, 187) // 443
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 10)
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr[1] != 0x08 {
		t.Fatalf("want atyp 0x08 got %d", hdr[1])
	}
	d.mu.Lock()
	n := len(d.targets)
	d.mu.Unlock()
	if n != 0 {
		t.Fatalf("opened ipv6 %v", d.targets)
	}
}

// TestSOCKSRemoteCloseIsEOFNotReset checks origin Close is not a TCP RST to the client.
func TestSOCKSRemoteCloseIsEOFNotReset(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		c, err := backend.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 8)
		_, _ = io.ReadFull(c, buf)
		c.Close()
	}()
	d := &recDialer{ln: backend}
	ln, err := ListenSOCKS5("127.0.0.1:0", d)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	rep := make([]byte, 2)
	if _, err := io.ReadFull(c, rep); err != nil {
		t.Fatal(err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0, 80}
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 10)
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr[1] != 0 {
		t.Fatalf("socks %d", hdr[1])
	}
	payload := make([]byte, 256*1024)
	_, _ = c.Write(payload)
	got := make([]byte, 16)
	n, err := c.Read(got)
	if isConnReset(err) {
		t.Fatalf("SOCKS RST after origin close: %v", err)
	}
	if n > 0 && err == nil {
		_, err = io.ReadAll(c)
	}
	if isConnReset(err) {
		t.Fatalf("SOCKS RST after origin close: %v", err)
	}
}

// isConnReset is true for ECONNRESET, including wrapped net.OpError.
func isConnReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return errors.Is(op.Err, syscall.ECONNRESET)
	}
	return false
}

// udpStubDialer serves SOCKS UDP ASSOCIATE through a local PacketConn.
type udpStubDialer struct {
	recDialer
	via net.PacketConn
}

func (u *udpStubDialer) ListenUDP() (net.PacketConn, error) {
	return u.via, nil
}

// TestSOCKSUDPAssociateViaListenUDP relays a SOCKS UDP datagram through ListenUDP.
func TestSOCKSUDPAssociateViaListenUDP(t *testing.T) {
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 2048)
		n, addr, err := echo.ReadFrom(buf)
		if err != nil {
			return
		}
		_, _ = echo.WriteTo(buf[:n], addr)
	}()

	via, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &udpStubDialer{via: via}
	ln, err := ListenSOCKS5("127.0.0.1:0", d)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	rep := make([]byte, 2)
	if _, err := io.ReadFull(c, rep); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 10)
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr[1] != 0 {
		t.Fatalf("socks %d", hdr[1])
	}
	bindPort := binary.BigEndian.Uint16(hdr[8:10])
	uc, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(bindPort)})
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	echoAddr := echo.LocalAddr().(*net.UDPAddr)
	pkt := []byte{0, 0, 0, 0x01, echoAddr.IP.To4()[0], echoAddr.IP.To4()[1], echoAddr.IP.To4()[2], echoAddr.IP.To4()[3], 0, 0}
	binary.BigEndian.PutUint16(pkt[8:], uint16(echoAddr.Port))
	pkt = append(pkt, "ping"...)
	if _, err := uc.Write(pkt); err != nil {
		t.Fatal(err)
	}
	_ = uc.SetReadDeadline(time.Now().Add(2 * time.Second))
	got := make([]byte, 64)
	n, err := uc.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	_, _, data, err := parseSocksUDP(got[:n])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ping" {
		t.Fatalf("%q", data)
	}
}

// TestParseSocksUDPIPv6Dropped parses native IPv6 UDP but canonSOCKSHost rejects it.
func TestParseSocksUDPIPv6Dropped(t *testing.T) {
	ip := net.ParseIP("2001:db8::1").To16()
	b := []byte{0, 0, 0, 0x04}
	b = append(b, ip...)
	b = append(b, 0, 53, 'x')
	host, port, data, err := parseSocksUDP(b)
	if err != nil {
		t.Fatal(err)
	}
	if port != 53 || string(data) != "x" {
		t.Fatalf("%d %q", port, data)
	}
	if _, ok := canonSOCKSHost(host); ok {
		t.Fatalf("native ipv6 must not forward %q", host)
	}
	mapped := net.ParseIP("::ffff:1.2.3.4").To16()
	b = []byte{0, 0, 0, 0x04}
	b = append(b, mapped...)
	b = append(b, 0, 80, 'q')
	host, port, data, err = parseSocksUDP(b)
	if err != nil {
		t.Fatal(err)
	}
	canon, ok := canonSOCKSHost(host)
	if !ok || canon != "1.2.3.4" || port != 80 || string(data) != "q" {
		t.Fatalf("%q %v %d %q", canon, ok, port, data)
	}
}
