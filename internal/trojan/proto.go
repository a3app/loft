package trojan

// Trojan request framing (trojan-gfw / Xray proxy/trojan). TCP, REALITY, XHTTP, UDP.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
)

const (
	cmdTCP        byte = 0x01
	cmdUDP        byte = 0x03
	atypIPv4           = 0x01
	atypDomain         = 0x03
	atypIPv6           = 0x04
	maxUDPPayload      = 8192
)

var crlf = []byte{'\r', '\n'}

// TunIP is the synthetic IPv4 the GUI and utun use. Trojan has no overlay lease.
const TunIP = "198.18.0.1"

// hexSHA224 is the 56-byte hex(SHA224(password)) Trojan hash.
func hexSHA224(password string) []byte {
	// Trojan hash is hex(SHA224(password)). codeql[go/weak-sensitive-data-hashing]
	sum := sha256.Sum224([]byte(password))
	out := make([]byte, 56)
	hex.Encode(out, sum[:])
	return out
}

// writeRequest writes a Trojan TCP or UDP header for host:port onto w.
func writeRequest(w io.Writer, password, target string, udp bool) error {
	addr, err := socksAddr(target)
	if err != nil {
		return err
	}
	cmd := cmdTCP
	if udp {
		cmd = cmdUDP
	}
	buf := make([]byte, 0, 56+2+1+len(addr)+2)
	buf = append(buf, hexSHA224(password)...)
	buf = append(buf, crlf...)
	buf = append(buf, cmd)
	buf = append(buf, addr...)
	buf = append(buf, crlf...)
	_, err = w.Write(buf)
	return err
}

// socksAddr encodes host:port as SOCKS5 ATYP + ADDR + PORT (big-endian).
func socksAddr(target string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	port64, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, err
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port64))
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return append(append([]byte{atypIPv4}, v4...), pb[:]...), nil
		}
		v6 := ip.To16()
		if v6 == nil {
			return nil, fmt.Errorf("trojan: bad ip")
		}
		return append(append([]byte{atypIPv6}, v6...), pb[:]...), nil
	}
	if len(host) == 0 || len(host) > 255 {
		return nil, fmt.Errorf("trojan: bad host")
	}
	out := make([]byte, 0, 1+1+len(host)+2)
	out = append(out, atypDomain, byte(len(host)))
	out = append(out, host...)
	return append(out, pb[:]...), nil
}

func writeUDPPacket(w io.Writer, addr net.Addr, payload []byte) error {
	if len(payload) > maxUDPPayload {
		return fmt.Errorf("trojan udp: oversize")
	}
	target := addr.String()
	if u, ok := addr.(*net.UDPAddr); ok {
		target = net.JoinHostPort(u.IP.String(), strconv.Itoa(u.Port))
	}
	hdr, err := socksAddr(target)
	if err != nil {
		return err
	}
	var ln [2]byte
	binary.BigEndian.PutUint16(ln[:], uint16(len(payload)))
	buf := make([]byte, 0, len(hdr)+2+2+len(payload))
	buf = append(buf, hdr...)
	buf = append(buf, ln[:]...)
	buf = append(buf, crlf...)
	buf = append(buf, payload...)
	_, err = w.Write(buf)
	return err
}

func readUDPPacket(r io.Reader) (net.Addr, []byte, error) {
	host, port, err := readSocksAddr(r)
	if err != nil {
		return nil, nil, err
	}
	var ln [2]byte
	if _, err := io.ReadFull(r, ln[:]); err != nil {
		return nil, nil, err
	}
	n := int(binary.BigEndian.Uint16(ln[:]))
	if n > maxUDPPayload {
		return nil, nil, fmt.Errorf("trojan udp: oversize")
	}
	var cr [2]byte
	if _, err := io.ReadFull(r, cr[:]); err != nil {
		return nil, nil, err
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		return &net.UDPAddr{IP: ip, Port: port}, payload, nil
	}
	return udpHostAddr{host: host, port: port}, payload, nil
}

type udpHostAddr struct {
	host string
	port int
}

func (a udpHostAddr) Network() string { return "udp" }
func (a udpHostAddr) String() string  { return net.JoinHostPort(a.host, strconv.Itoa(a.port)) }

func readSocksAddr(r io.Reader) (host string, port int, err error) {
	var atyp [1]byte
	if _, err = io.ReadFull(r, atyp[:]); err != nil {
		return "", 0, err
	}
	switch atyp[0] {
	case atypIPv4:
		var a [4]byte
		if _, err = io.ReadFull(r, a[:]); err != nil {
			return "", 0, err
		}
		host = net.IP(a[:]).String()
	case atypIPv6:
		var a [16]byte
		if _, err = io.ReadFull(r, a[:]); err != nil {
			return "", 0, err
		}
		host = net.IP(a[:]).String()
	case atypDomain:
		var n [1]byte
		if _, err = io.ReadFull(r, n[:]); err != nil {
			return "", 0, err
		}
		if n[0] == 0 {
			return "", 0, fmt.Errorf("trojan: bad host")
		}
		b := make([]byte, n[0])
		if _, err = io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	default:
		return "", 0, fmt.Errorf("trojan: atyp %d", atyp[0])
	}
	var pb [2]byte
	if _, err = io.ReadFull(r, pb[:]); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(pb[:])), nil
}
