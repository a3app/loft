package transport

// Origin TLS application-record lengths, used to pad mask ciphertext.

import (
	"io"
	"net"
)

const maskPadMax = 12

// recordCollector notes application-record sizes while tls.Client reads.
type recordCollector struct {
	net.Conn
	pending []byte
	appLens []int
}

// Read copies from the inner conn and records complete application records.
func (c *recordCollector) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.pending = append(c.pending, p[:n]...)
		for len(c.pending) >= 5 && len(c.appLens) < maskPadMax {
			ln := int(c.pending[3])<<8 | int(c.pending[4])
			if ln < 0 || ln > (16<<10)+256 {
				break
			}
			if len(c.pending) < 5+ln {
				break
			}
			if c.pending[0] == recApp {
				c.appLens = append(c.appLens, 5+ln)
			}
			c.pending = c.pending[5+ln:]
		}
		if len(c.pending) > 64<<10 {
			c.pending = c.pending[len(c.pending)-4096:]
		}
	}
	return n, err
}

// padFor is the inner-plaintext zero padding so the sealed record is target
// bytes long (TLS header plus ciphertext). Zero when the payload already
// exceeds the sampled dest record.
func padFor(targetRec, payload, overhead int) int {
	if targetRec <= 0 || payload < 0 || overhead < 0 {
		return 0
	}
	inner := targetRec - 5 - overhead
	pad := inner - 1 - payload
	if pad < 0 {
		return 0
	}
	maxPad := maskAppMax - payload
	if maxPad < 0 {
		return 0
	}
	if pad > maxPad {
		pad = maxPad
	}
	return pad
}

// chunkFor splits p so the next dest-sized record can hold it.
func chunkFor(targetRec, leftover, overhead int) int {
	if leftover <= 0 {
		return 0
	}
	if targetRec <= 0 {
		if leftover > maskAppMax {
			return maskAppMax
		}
		return leftover
	}
	maxPayload := targetRec - 5 - overhead - 1
	if maxPayload < 1 {
		maxPayload = maskAppMax
	}
	if maxPayload > maskAppMax {
		maxPayload = maskAppMax
	}
	if leftover > maxPayload {
		return maxPayload
	}
	return leftover
}

// destRec is the dest application-record size for the i-th loft record.
// The first len(pads) follow the sampled origin; later records pick from that set.
func destRec(pads []int, i int) int {
	n := len(pads)
	if n == 0 {
		return 0
	}
	if i < 0 {
		i = 0
	}
	if i < n {
		return pads[i]
	}
	return pads[randN(n)]
}

// drainHTTP reads a bounded response so the spy sees dest homepage records.
func drainHTTP(r io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r, 32<<10))
}
