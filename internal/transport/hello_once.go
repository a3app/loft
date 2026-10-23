package transport

// Single-use client_random. A second TCP ClientHello with the same random
// inside 10 minutes is treated as a probe and spliced. QUIC already does
// this per source address in the UDP demux; this covers the TCP front.

import (
	"sync"
	"time"
)

const helloOnceTTL = 10 * time.Minute

type helloOnceCache struct {
	mu sync.Mutex
	m  map[[32]byte]time.Time
}

var helloOnce helloOnceCache

// take reports whether random is seen for the first time in the TTL.
func (c *helloOnceCache) take(random []byte) bool {
	if len(random) != 32 {
		return false
	}
	var k [32]byte
	copy(k[:], random)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[[32]byte]time.Time{}
	}
	if len(c.m) > 50000 {
		for id, at := range c.m {
			if now.Sub(at) > helloOnceTTL {
				delete(c.m, id)
			}
		}
	}
	if at, ok := c.m[k]; ok && now.Sub(at) < helloOnceTTL {
		return false
	}
	c.m[k] = now
	return true
}
