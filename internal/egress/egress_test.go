package egress

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `# test
channel wg-nl dev wg-nl fail px
channel px socks5 127.0.0.1:1 user "lo ft" pass "s\"3" udp proxy fail direct
channel web http 127.0.0.1:2 tls fail block
channel office sstp vpn.example.org:443 user office pass x auth mschapv2 insecure off
rule member bob via px
rule tag office via wg-nl
rule list blocked via web
default direct
`

func TestParseRender(t *testing.T) {
	c, err := Parse(sample)
	if err != nil {
		t.Fatal(err)
	}
	if px := c.Channel("px"); px.User != "lo ft" || px.Pass != `s"3` || px.UDP != "proxy" || px.Fail != Direct {
		t.Fatalf("px %+v", px)
	}
	c2, err := Parse(Render(c))
	if err != nil || Render(c2) != Render(c) {
		t.Fatalf("round trip: %v\n%s", err, Render(c2))
	}
	for _, bad := range []string{
		"channel A dev x",                                // upper case
		"channel a dev x\nchannel a dev y",               // twice
		"channel a socks5 nohost",                        // no port
		"channel a http 1.2.3.4:5 udp proxy",             // udp proxy needs socks5
		"channel a dev x fail b",                         // unknown fail
		"channel a dev x fail b\nchannel b dev y fail a", // loop
		"channel a socks5 1.2.3.4:5 via b\nchannel b socks5 1.2.3.4:6 via a",
		"channel a dev x via b\nchannel b dev y", // only proxies via
		"rule member bob via nowhere",
		"rule host x via direct",
		"default via nowhere",
		"frobnicate",
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
}

func TestRoute(t *testing.T) {
	c, _ := Parse(sample)
	r := New(c, Options{Lists: map[string][]string{"blocked": {".example.ru", "198.51.100.0/24", "exact.net"}}, SysNet: t.TempDir()})
	defer r.Close()
	for _, x := range []struct {
		who  Who
		host string
		want string
	}{
		{Who{Member: "bob"}, "a.com", "px"},
		{Who{Member: "alice", Tags: []string{"Office"}}, "a.com", "wg-nl"},
		{Who{}, "www.example.ru", "web"},
		{Who{}, "example.ru", "web"},
		{Who{}, "198.51.100.9", "web"},
		{Who{}, "exact.net", "web"},
		{Who{}, "notexact.net", Direct},
	} {
		if got := r.Route(x.who, x.host); got != x.want {
			t.Fatalf("%+v %s: %s, want %s", x.who, x.host, got, x.want)
		}
	}
	if s := strings.Join(r.steps("wg-nl"), ">"); s != "wg-nl>px>direct" {
		t.Fatalf("steps %s", s)
	}
	if s := strings.Join(r.steps("office"), ">"); s != "office>direct" {
		t.Fatalf("steps %s", s)
	}
}

// socksServer is a minimal SOCKS5 server: CONNECT and UDP ASSOCIATE, with a login.
func socksServer(t *testing.T, user, pass string) (addr string, connects chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	connects = make(chan string, 16)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				h := make([]byte, 2)
				io.ReadFull(br, h)
				m := make([]byte, h[1])
				io.ReadFull(br, m)
				c.Write([]byte{5, 2})
				v := make([]byte, 2)
				io.ReadFull(br, v)
				u := make([]byte, v[1])
				io.ReadFull(br, u)
				pl := make([]byte, 1)
				io.ReadFull(br, pl)
				p := make([]byte, pl[0])
				io.ReadFull(br, p)
				if string(u) != user || string(p) != pass {
					c.Write([]byte{1, 1})
					return
				}
				c.Write([]byte{1, 0})
				req := make([]byte, 4)
				io.ReadFull(br, req)
				target, _ := readSocksAddr(br, req[3])
				switch req[1] {
				case 1:
					up, err := net.Dial("tcp", target)
					if err != nil {
						c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
						return
					}
					defer up.Close()
					connects <- target
					c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
					go io.Copy(up, br)
					io.Copy(c, up)
				case 3:
					pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
					defer pc.Close()
					port := pc.LocalAddr().(*net.UDPAddr).Port
					rep := []byte{5, 0, 0, 1, 127, 0, 0, 1}
					c.Write(binary.BigEndian.AppendUint16(rep, uint16(port)))
					connects <- "udp"
					go func() {
						buf := make([]byte, 2048)
						for {
							n, from, err := pc.ReadFrom(buf)
							if err != nil {
								return
							}
							r := &byteReader{b: buf[4:n]}
							dst, _ := readSocksAddr(r, buf[3])
							up, _ := net.Dial("udp", dst)
							up.Write(r.b)
							up.SetReadDeadline(time.Now().Add(time.Second))
							rb := make([]byte, 2048)
							m, err := up.Read(rb)
							up.Close()
							if err != nil {
								continue
							}
							hdr, _ := socksAddr(dst)
							pc.WriteTo(append(append([]byte{0, 0, 0}, hdr...), rb[:m]...), from)
						}
					}()
					io.Copy(io.Discard, br)
				}
			}()
		}
	}()
	return ln.Addr().String(), connects
}

func echoTCP(t *testing.T) string {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, c net.Conn) {
	t.Helper()
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
		t.Fatalf("echo: %q %v", b, err)
	}
}

func TestDialThroughProxiesAndFail(t *testing.T) {
	echo := echoTCP(t)
	socks, connects := socksServer(t, "u", "p w")
	hp, _ := net.Listen("tcp", "127.0.0.1:0")
	defer hp.Close()
	go http.Serve(hp, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") == "" {
			http.Error(w, "no", http.StatusProxyAuthRequired)
			return
		}
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		hj, _ := w.(http.Hijacker)
		c, _, _ := hj.Hijack()
		io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
		go io.Copy(up, c)
		io.Copy(c, up)
		c.Close()
		up.Close()
	}))
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	dead.Close()
	conf := "channel px socks5 " + socks + " user u pass \"p w\" udp proxy\n" +
		"channel web http " + hp.Addr().String() + " user a pass b\n" +
		"channel chain http " + hp.Addr().String() + " user a pass b via px\n" +
		"channel gone socks5 " + deadAddr + " fail web\n" +
		"channel wg dev wg0 fail block\n" +
		"rule member bob via px\nrule member carol via web\nrule member dave via gone\nrule member erin via wg\nrule member frank via chain\n"
	c, err := Parse(conf)
	if err != nil {
		t.Fatal(err)
	}
	sys := t.TempDir()
	r := New(c, Options{SysNet: sys})
	defer r.Close()
	ctx := context.Background()
	for _, x := range [][2]string{{"bob", "px"}, {"carol", "web"}, {"dave", "web"}, {"frank", "chain"}, {"", Direct}} {
		who, want := x[0], x[1]
		conn, used, err := r.DialTCP(ctx, Who{Member: who}, echo)
		if err != nil || used != want {
			t.Fatalf("%s: %s %v", who, used, err)
		}
		roundTrip(t, conn)
		conn.Close()
	}
	if got := <-connects; got != echo {
		t.Fatalf("socks saw %s", got)
	}
	// frank's HTTP proxy was reached through the SOCKS proxy.
	if got := <-connects; got != hp.Addr().String() {
		t.Fatalf("chain: socks saw %s", got)
	}
	// wg0 has no interface: its fail is block.
	if _, used, err := r.DialTCP(ctx, Who{Member: "erin"}, echo); err == nil || used != Block {
		t.Fatalf("erin: %s %v", used, err)
	}
	os.MkdirAll(filepath.Join(sys, "wg0"), 0o755)
	os.WriteFile(filepath.Join(sys, "wg0", "operstate"), []byte("unknown\n"), 0o644)
	if !r.up(r.ch["wg"]) {
		t.Fatal("wg0 with operstate unknown counts as up")
	}
	st := r.Status()
	if st[0].Name != "px" || st[0].TX == 0 || st[0].RX == 0 || st[3].State != "down" || st[3].Error == "" {
		t.Fatalf("status %+v", st)
	}

	// UDP through the SOCKS proxy.
	echoU, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer echoU.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, a, err := echoU.ReadFrom(b)
			if err != nil {
				return
			}
			echoU.WriteTo(b[:n], a)
		}
	}()
	got := make(chan string, 1)
	u := r.NewUDP(Who{Member: "bob"}, func(from string, data []byte) { got <- from + " " + string(data) })
	defer u.Close()
	u.Send(echoU.LocalAddr().String(), []byte("dgram"))
	select {
	case s := <-got:
		if s != echoU.LocalAddr().String()+" dgram" {
			t.Fatalf("udp back %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no UDP reply through SOCKS")
	}
}
