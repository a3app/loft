package inbound

// tun_tcp_test.go checks gVisor SYN-ACK for overlay and Darwin dummy sources.

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// pipeTun is a TUN stub: Read from r, record Writes.
type pipeTun struct {
	mu sync.Mutex
	w  [][]byte
	r  chan []byte
}

// Read copies one packet from r, or EOF when r is closed.
func (p *pipeTun) Read(b []byte) (int, error) {
	pkt, ok := <-p.r
	if !ok {
		return 0, io.EOF
	}
	n := copy(b, pkt)
	return n, nil
}

// Write stores a copy of b.
func (p *pipeTun) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.w = append(p.w, append([]byte(nil), b...))
	p.mu.Unlock()
	return len(b), nil
}

// Close closes the Read channel.
func (p *pipeTun) Close() error {
	close(p.r)
	return nil
}

// Name is a stub interface name.
func (p *pipeTun) Name() string { return "utun-test" }

// TestIPv4DatagramDropsTruncated refuses an IPv4 header whose Total Length
// exceeds the TUN read, so a short buffer cannot inject a torn packet.
func TestIPv4DatagramDropsTruncated(t *testing.T) {
	p := make([]byte, 40)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], 100)
	if ipv4Datagram(p) != nil {
		t.Fatal("truncated")
	}
	binary.BigEndian.PutUint16(p[2:4], 40)
	if got := ipv4Datagram(p); len(got) != 40 {
		t.Fatalf("len %d", len(got))
	}
}

// TestGVisorSYNAck expects SYN-ACK for a SYN sourced from overlay IPv4.
func TestGVisorSYNAck(t *testing.T) {
	ifc := &pipeTun{r: make(chan []byte, 4)}
	_, cidr, _ := net.ParseCIDR("10.8.0.0/24")
	ts, err := newStack(ifc, nil, net.IPv4(10, 8, 0, 6), cidr)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()

	waitSYNAck(t, ts, ifc, makeTCPSyn(net.IPv4(10, 0, 0, 1), 12345, net.IPv4(8, 8, 8, 8), 443))
}

// TestGVisorSYNAckDummy expects SYN-ACK for a SYN sourced from Darwin dummy 169.254.8.1.
func TestGVisorSYNAckDummy(t *testing.T) {
	ifc := &pipeTun{r: make(chan []byte, 4)}
	_, cidr, _ := net.ParseCIDR("10.8.0.0/24")
	ts, err := newStack(ifc, nil, net.IPv4(10, 8, 0, 6), cidr)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	waitSYNAck(t, ts, ifc, makeTCPSyn(net.IPv4(169, 254, 8, 1), 12345, net.IPv4(8, 8, 8, 8), 443))
}

// waitSYNAck injects syn and fails if no SYN-ACK is written within 2s.
func waitSYNAck(t *testing.T, ts *tunStack, ifc *pipeTun, syn []byte) {
	t.Helper()
	ifc.r <- syn
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ifc.mu.Lock()
		n := len(ifc.w)
		var last []byte
		if n > 0 {
			last = ifc.w[n-1]
		}
		ifc.mu.Unlock()
		if n > 0 && len(last) >= 40 && last[9] == 6 {
			flags := last[33]
			t.Logf("out n=%d proto=%d flags=0x%x %s -> %s", len(last), last[9], flags, net.IP(last[12:16]), net.IP(last[16:20]))
			if flags&0x12 == 0x12 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	st := ts.stk.Stats()
	t.Logf("ip recv=%d valid=%d delivered=%d malformed=%d badsrc=%d baddst=%d tcp invalid=%d",
		st.IP.PacketsReceived.Value(),
		st.IP.ValidPacketsReceived.Value(),
		st.IP.PacketsDelivered.Value(),
		st.IP.MalformedPacketsReceived.Value(),
		st.IP.InvalidSourceAddressesReceived.Value(),
		st.IP.InvalidDestinationAddressesReceived.Value(),
		st.TCP.InvalidSegmentsReceived.Value(),
	)
	ifc.mu.Lock()
	t.Fatalf("no SYN-ACK, writes=%d", len(ifc.w))
	ifc.mu.Unlock()
}

// makeTCPSyn builds a checksummed IPv4 TCP SYN.
func makeTCPSyn(src net.IP, sport uint16, dst net.IP, dport uint16) []byte {
	ihl := 20
	thl := 20
	pkt := make([]byte, ihl+thl)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = 6
	copy(pkt[12:16], src.To4())
	copy(pkt[16:20], dst.To4())
	binary.BigEndian.PutUint16(pkt[10:12], checksum(pkt[:ihl]))
	binary.BigEndian.PutUint16(pkt[ihl:ihl+2], sport)
	binary.BigEndian.PutUint16(pkt[ihl+2:ihl+4], dport)
	binary.BigEndian.PutUint32(pkt[ihl+4:ihl+8], 1)
	pkt[ihl+12] = 0x50
	pkt[ihl+13] = 0x02 // SYN
	binary.BigEndian.PutUint16(pkt[ihl+14:ihl+16], 65535)
	sum := tcpChecksum(src.To4(), dst.To4(), pkt[ihl:])
	binary.BigEndian.PutUint16(pkt[ihl+16:ihl+18], sum)
	return pkt
}

// tcpChecksum is the IPv4 TCP checksum over the pseudo-header and segment.
func tcpChecksum(src, dst net.IP, tcp []byte) uint16 {
	var s uint32
	s4, d4 := src.To4(), dst.To4()
	s += uint32(s4[0])<<8 | uint32(s4[1])
	s += uint32(s4[2])<<8 | uint32(s4[3])
	s += uint32(d4[0])<<8 | uint32(d4[1])
	s += uint32(d4[2])<<8 | uint32(d4[3])
	s += 6
	s += uint32(len(tcp))
	for i := 0; i+1 < len(tcp); i += 2 {
		s += uint32(tcp[i])<<8 | uint32(tcp[i+1])
	}
	if len(tcp)%2 == 1 {
		s += uint32(tcp[len(tcp)-1]) << 8
	}
	for s > 0xffff {
		s = (s >> 16) + (s & 0xffff)
	}
	return ^uint16(s)
}
