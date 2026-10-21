package transport

import (
	"net"
	"testing"
	"time"
)

func TestStickyUDPEcho(t *testing.T) {
	pc, err := listenUDPSticky("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	c, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, addr, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "ping" {
		t.Fatalf("got %q", buf[:n])
	}
	if _, err := pc.WriteTo([]byte("pong"), addr); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err = c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "pong" {
		t.Fatalf("reply %q", buf[:n])
	}
}

func TestDstFromEmptyOOB(t *testing.T) {
	if dstFromOOB(nil) != nil || dstFromOOB([]byte{}) != nil {
		t.Fatal("empty")
	}
}
