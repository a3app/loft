package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

// quicCertFiles writes a self-signed node certificate for 127.0.0.1.
func quicCertFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, DNSNames: []string{"node.test"}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0o600)
	return certFile, keyFile
}

// firstInitial dials a silent UDP socket and returns the first datagram.
func firstInitial(t *testing.T, dial func(addr string)) []byte {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go dial(pc.LocalAddr().String())
	_ = pc.SetReadDeadline(time.Now().Add(5 * time.Second))
	b := make([]byte, 2048)
	n, _, err := pc.ReadFrom(b)
	if err != nil {
		t.Fatal(err)
	}
	return b[:n]
}

func markedDial(token []byte) func(string) {
	return func(addr string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, err := dialQUIC(ctx, addr, token, &tls.Config{ServerName: "node.test", NextProtos: []string{quicALPN}, InsecureSkipVerify: true}, quicCfg())
		if err == nil {
			c.CloseWithError(0, "")
		}
	}
}

func plainDial(addr string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := quic.DialAddr(ctx, addr, &tls.Config{ServerName: "node.test", NextProtos: []string{"h3"}, InsecureSkipVerify: true}, nil)
	if err == nil {
		c.CloseWithError(0, "")
	}
}

// TestQUICMarkInRealInitial reads the mark back out of a quic-go Initial:
// v3 when mask_pub is known, v2 under the member key otherwise.
func TestQUICMarkInRealInitial(t *testing.T) {
	node := []byte("node-token")
	var addr string
	pkt := firstInitial(t, func(a string) {
		addr = a
		RememberMaskPub(a, NodeMaskPub(node))
		markedDial([]byte("someone-else"))(a)
	})
	t.Cleanup(func() { views.Delete(addr) })
	scid, random, err := initialHello(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(scid) != quicMarkLen || !verifyQUICMark(node, random, scid) {
		t.Fatalf("v3 mark not found: scid %x", scid)
	}
	if verifyQUICMark([]byte("other-node"), random, scid) {
		t.Fatal("mark verified under another node")
	}

	pkt = firstInitial(t, markedDial(node)) // no mask_pub for this port: v2
	scid, random, err = initialHello(pkt)
	if err != nil || !verifyQUICMark(node, random, scid) {
		t.Fatalf("v2 mark not found: %v", err)
	}

	pkt = firstInitial(t, plainDial)
	scid, random, err = initialHello(pkt)
	if err != nil {
		t.Fatalf("plain quic-go Initial must parse: %v", err)
	}
	if verifyQUICMark(node, random, scid) {
		t.Fatal("unmarked Initial verified")
	}
	if _, _, err := initialHello([]byte{0x40, 1, 2, 3, 4, 5, 6, 7}); err != errNotInitial {
		t.Fatalf("short header: %v", err)
	}
}

func TestServerCIDsOwn(t *testing.T) {
	s := newServerCIDs([]byte("node"))
	id, err := s.GenerateConnectionID()
	if err != nil {
		t.Fatal(err)
	}
	pkt := append([]byte{0x41}, id.Bytes()...)
	pkt = append(pkt, make([]byte, 30)...)
	if !s.owns(pkt) {
		t.Fatal("own connection ID not recognised")
	}
	if newServerCIDs([]byte("other")).owns(pkt) {
		t.Fatal("another node's ID recognised")
	}
	pkt[1] ^= 1
	if s.owns(pkt) {
		t.Fatal("altered ID recognised")
	}
}

// TestListenUDPDecoyRoutes: a loft client gets the quic front on the shared
// port; an unmarked QUIC client and a replayed loft Initial reach dest.
func TestListenUDPDecoyRoutes(t *testing.T) {
	token := "node-token"
	dest, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	got := make(chan []byte, 64)
	go func() {
		b := make([]byte, 2048)
		for {
			n, _, err := dest.ReadFrom(b)
			if err != nil {
				return
			}
			got <- append([]byte(nil), b[:n]...)
		}
	}()
	probe, _ := net.ListenPacket("udp", "127.0.0.1:0")
	listen := probe.LocalAddr().String()
	probe.Close()
	certFile, keyFile := quicCertFiles(t)
	info := NodeInfo{Token: []byte(token), Caps: wire.NodeCaps}
	go ListenUDPDecoy(listen, certFile, keyFile, info, []string{wire.FrontQUIC}, dest.LocalAddr().String())
	time.Sleep(100 * time.Millisecond)

	plainDial(listen)
	select {
	case p := <-got:
		if p[0]&0xf0 != 0xc0 {
			t.Fatalf("dest got %x, want an Initial", p[:1])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unmarked QUIC did not reach dest")
	}

	q := NewQUIC(token, config.Outbound{Type: wire.FrontQUIC, URL: "https://" + listen, Insecure: true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := q.Start(ctx); err != nil {
		t.Fatalf("loft QUIC through the demux: %v", err)
	}
	defer q.Close()
	for len(got) > 0 {
		<-got
	}

	// Replay a loft Initial from another port: it must go to dest.
	pkt := firstInitial(t, markedDial([]byte(token)))
	c, err := net.Dial("udp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(pkt)
	c2, _ := net.Dial("udp", listen)
	defer c2.Close()
	c2.Write(pkt)
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("replayed loft Initial did not reach dest")
	}
}

// TestQUICAndH3ShareALPN: with both fronts on, one port serves the quic
// mux and HTTP/3 CONNECT side by side, with and without a decoy.
func TestQUICAndH3ShareALPN(t *testing.T) {
	token := "node-token"
	certFile, keyFile := quicCertFiles(t)
	info := NodeInfo{Token: []byte(token), Caps: wire.NodeCaps}
	dest, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	for _, decoy := range []bool{false, true} {
		probe, _ := net.ListenPacket("udp", "127.0.0.1:0")
		listen := probe.LocalAddr().String()
		probe.Close()
		fronts := []string{wire.FrontQUIC, wire.FrontH3}
		if decoy {
			go ListenUDPDecoy(listen, certFile, keyFile, info, fronts, dest.LocalAddr().String())
		} else {
			go ListenUDP(listen, certFile, keyFile, info, fronts)
		}
		time.Sleep(100 * time.Millisecond)
		for _, kind := range []string{wire.FrontQUIC, wire.FrontH3, wire.FrontQUIC} {
			o := config.Outbound{Type: kind, URL: "https://" + listen, Insecure: true}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var err error
			if kind == wire.FrontQUIC {
				q := NewQUIC(token, o)
				if err = q.Start(ctx); err == nil {
					q.Close()
				}
			} else {
				h := NewH3(token, o, wire.MemberCaps)
				if err = h.Start(ctx); err == nil {
					h.Close()
				}
			}
			cancel()
			if err != nil {
				t.Fatalf("decoy=%v %s: %v", decoy, kind, err)
			}
		}
	}
}
