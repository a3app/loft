package egress

import (
	"context"
	"net"

	"golang.org/x/time/rate"
)

// limiter is a byte-rate limiter for mbit Mbit/s; nil when there is no cap.
func limiter(mbit int) *rate.Limiter {
	if mbit <= 0 {
		return nil
	}
	bps := mbit * 125_000
	return rate.NewLimiter(rate.Limit(bps), max(64<<10, bps/10))
}

// limited caps a connection with limiters its priority list shares: in is
// what the member reads, out what it writes.
type limited struct {
	net.Conn
	in, out *rate.Limiter
}

func (c *limited) Read(p []byte) (int, error) {
	if c.in != nil && len(p) > c.in.Burst() {
		p = p[:c.in.Burst()]
	}
	n, err := c.Conn.Read(p)
	if c.in != nil && n > 0 {
		c.in.WaitN(context.Background(), n)
	}
	return n, err
}

func (c *limited) Write(p []byte) (int, error) {
	if c.out == nil {
		return c.Conn.Write(p)
	}
	done := 0
	for len(p) > 0 {
		k := min(len(p), c.out.Burst())
		c.out.WaitN(context.Background(), k)
		n, err := c.Conn.Write(p[:k])
		done += n
		if err != nil {
			return done, err
		}
		p = p[k:]
	}
	return done, nil
}
