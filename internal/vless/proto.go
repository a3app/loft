package vless

// VLESS request/response framing (Xray proxy/vless). TCP, REALITY, Vision, XHTTP.

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
)

const (
	version         = 0
	cmdTCP     byte = 1
	atypIPv4        = 1
	atypDomain      = 2
	atypIPv6        = 3
	flowVision      = "xtls-rprx-vision"
)

// TunIP is the synthetic IPv4 the GUI and utun use. VLESS has no overlay lease.
const TunIP = "198.18.0.1"

// parseUUID accepts 8-4-4-4-12 or 32 hex digits.
func parseUUID(s string) ([16]byte, error) {
	var out [16]byte
	hexes := make([]byte, 0, 32)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' {
			continue
		}
		if !isHex(c) {
			return out, fmt.Errorf("vless uuid")
		}
		hexes = append(hexes, c)
	}
	if len(hexes) != 32 {
		return out, fmt.Errorf("vless uuid")
	}
	n, err := hex.Decode(out[:], hexes)
	if err != nil || n != 16 {
		return out, fmt.Errorf("vless uuid")
	}
	return out, nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// writeRequest writes a VLESS TCP header for host:port onto w.
func writeRequest(w io.Writer, uuid [16]byte, target string, vision bool) error {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port64, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return err
	}
	port := uint16(port64)
	var addr []byte
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			addr = append([]byte{atypIPv4}, v4...)
		} else {
			v6 := ip.To16()
			if v6 == nil {
				return fmt.Errorf("vless: bad ip")
			}
			addr = append([]byte{atypIPv6}, v6...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return fmt.Errorf("vless: bad host")
		}
		addr = append([]byte{atypDomain, byte(len(host))}, []byte(host)...)
	}
	addon := visionAddon(vision)
	buf := make([]byte, 0, 18+len(addon)+2+len(addr))
	buf = append(buf, version)
	buf = append(buf, uuid[:]...)
	buf = append(buf, byte(len(addon)))
	buf = append(buf, addon...)
	buf = append(buf, cmdTCP)
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], port)
	buf = append(buf, pb[:]...)
	buf = append(buf, addr...)
	_, err = w.Write(buf)
	return err
}

// readResponse consumes the VLESS response header.
func readResponse(r io.Reader) error {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	if hdr[0] != version {
		return fmt.Errorf("vless: version %d", hdr[0])
	}
	if hdr[1] > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(hdr[1])); err != nil {
			return err
		}
	}
	return nil
}

// visionAddon is the protobuf Addons{Flow: xtls-rprx-vision} blob, or nil.
func visionAddon(on bool) []byte {
	if !on {
		return nil
	}
	flow := flowVision
	b := make([]byte, 0, 2+len(flow))
	b = append(b, 0x0A, byte(len(flow)))
	return append(b, flow...)
}
