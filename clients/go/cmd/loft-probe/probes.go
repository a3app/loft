// probes.go runs ICMP, TCP, UDP, DNS, HTTP, and HTTPS checks over a via.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/internal/netconf"
)

// result is one probe outcome printed by printResults.
type result struct {
	Proto  string
	Target string
	OK     bool
	Skip   bool
	RTT    time.Duration
	Detail string
}

// runSpec dispatches one spec; ICMP repeats count times.
func runSpec(ctx context.Context, v via, sp spec, count int, timeout time.Duration, qname string, insecure bool, bodyBytes, uploadBytes int) []result {
	if count < 1 {
		count = 1
	}
	switch sp.proto {
	case "icmp":
		out := make([]result, 0, count)
		for i := 1; i <= count; i++ {
			out = append(out, runICMP(ctx, v, sp, uint16(i), timeout))
		}
		return out
	case "tcp":
		return []result{runTCP(ctx, v, sp, timeout)}
	case "udp":
		return []result{runUDP(ctx, v, sp, timeout)}
	case "dns":
		return []result{runDNS(ctx, v, sp, timeout, qname)}
	case "http", "https":
		return []result{runHTTP(ctx, v, sp, timeout, insecure, bodyBytes, uploadBytes)}
	default:
		return []result{{Proto: sp.proto, Target: displayTarget(sp), Detail: "unknown proto"}}
	}
}

// runICMP sends one overlay ICMP echo and waits for a matching reply.
func runICMP(ctx context.Context, v via, sp spec, seq uint16, timeout time.Duration) result {
	r := result{Proto: "icmp", Target: displayTarget(sp)}
	if !v.canICMP() {
		r.Skip = true
		r.Detail = "SOCKS has no ICMP; use -via mux"
		return r
	}
	dst := net.ParseIP(sp.host)
	if dst == nil || dst.To4() == nil {
		r.Detail = "icmp needs an IPv4 address"
		return r
	}
	src := net.ParseIP(v.overlayIP())
	if src == nil || src.To4() == nil {
		src = net.ParseIP(netconf.FallbackClientIP)
	}
	var idb [2]byte
	_, _ = rand.Read(idb[:])
	id := binary.BigEndian.Uint16(idb[:])
	pkt := makeEcho(src, dst, id, seq, []byte("loft-probe"))
	if pkt == nil {
		r.Detail = "bad echo packet"
		return r
	}
	addr := net.JoinHostPort(dst.String(), "1")
	t0 := time.Now()
	if err := v.writeDgram(addr, pkt); err != nil {
		r.Detail = err.Error()
		return r
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dg, err := v.waitDgram(cctx, func(dg datagram) bool {
		_, ok := parseICMPReply(dg.data, id, seq)
		return ok
	})
	r.RTT = time.Since(t0)
	if err != nil {
		r.Detail = "timeout"
		if err != context.DeadlineExceeded && !strings.Contains(err.Error(), "deadline") {
			r.Detail = err.Error()
		}
		return r
	}
	srcIP, _ := parseICMPReply(dg.data, id, seq)
	r.OK = true
	r.Detail = fmt.Sprintf("echo-reply from %s id=%d seq=%d", srcIP, id, seq)
	return r
}

// runTCP dials host:port; :443 does TLS, :80 sends HEAD, else checks the stream.
func runTCP(ctx context.Context, v via, sp spec, timeout time.Duration) result {
	port := sp.port
	if port == 0 {
		port = 80
	}
	hostport := net.JoinHostPort(sp.host, strconv.Itoa(port))
	r := result{Proto: "tcp", Target: hostport}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t0 := time.Now()
	c, err := v.dialTCP(cctx, hostport)
	if err != nil {
		r.RTT = time.Since(t0)
		r.Detail = err.Error()
		return r
	}
	defer c.Close()
	if dl, ok := cctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	switch port {
	case 443:
		cfg := &tls.Config{ServerName: sp.host, InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}}
		tc := tls.Client(c, cfg)
		if err := tc.HandshakeContext(cctx); err != nil {
			r.RTT = time.Since(t0)
			r.Detail = "tls " + err.Error()
			return r
		}
		_ = tc.Close()
		r.RTT = time.Since(t0)
		r.OK = true
		r.Detail = "tls handshake"
		return r
	case 80:
		if _, err := io.WriteString(c, "HEAD / HTTP/1.0\r\nHost: "+sp.host+"\r\n\r\n"); err != nil {
			r.RTT = time.Since(t0)
			r.Detail = err.Error()
			return r
		}
		br := bufio.NewReader(c)
		line, err := br.ReadString('\n')
		r.RTT = time.Since(t0)
		if err != nil {
			r.Detail = err.Error()
			return r
		}
		r.OK = true
		r.Detail = strings.TrimSpace(line)
		return r
	}
	errc := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := c.Read(buf)
		errc <- err
	}()
	select {
	case err := <-errc:
		r.RTT = time.Since(t0)
		if err == nil || err == io.EOF {
			r.OK = true
			r.Detail = "peer closed"
			return r
		}
		r.Detail = err.Error()
		return r
	case <-time.After(400 * time.Millisecond):
		r.RTT = time.Since(t0)
		r.OK = true
		r.Detail = "stream open"
		return r
	case <-cctx.Done():
		r.RTT = time.Since(t0)
		r.Detail = "timeout"
		return r
	}
}

// runUDP writes a payload and waits for any datagram from the same host:port.
func runUDP(ctx context.Context, v via, sp spec, timeout time.Duration) result {
	port := sp.port
	if port == 0 {
		port = 53
	}
	hostport := net.JoinHostPort(sp.host, strconv.Itoa(port))
	r := result{Proto: "udp", Target: hostport}
	payload := []byte("loft-probe")
	t0 := time.Now()
	if err := v.writeDgram(hostport, payload); err != nil {
		r.Detail = err.Error()
		return r
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := v.waitDgram(cctx, func(dg datagram) bool {
		h, p, e := net.SplitHostPort(dg.addr)
		if e != nil {
			return false
		}
		return hostEq(h, sp.host) && p == strconv.Itoa(port)
	})
	r.RTT = time.Since(t0)
	if err != nil {
		r.Detail = "no reply (many UDP dests stay silent)"
		return r
	}
	r.OK = true
	r.Detail = "reply"
	return r
}

// runDNS sends a UDP A query and reports rcode or answer addresses.
func runDNS(ctx context.Context, v via, sp spec, timeout time.Duration, qname string) result {
	port := sp.port
	if port == 0 {
		port = 53
	}
	if qname == "" {
		qname = "example.com"
	}
	hostport := net.JoinHostPort(sp.host, strconv.Itoa(port))
	r := result{Proto: "dns", Target: hostport + " " + qname}
	var idb [2]byte
	_, _ = rand.Read(idb[:])
	id := binary.BigEndian.Uint16(idb[:])
	q := dnsQuery(id, qname)
	t0 := time.Now()
	if err := v.writeDgram(hostport, q); err != nil {
		r.Detail = err.Error()
		return r
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dg, err := v.waitDgram(cctx, func(dg datagram) bool {
		if len(dg.data) < 12 {
			return false
		}
		return binary.BigEndian.Uint16(dg.data[0:2]) == id
	})
	r.RTT = time.Since(t0)
	if err != nil {
		r.Detail = "timeout"
		return r
	}
	rcode, addrs, err := parseDNSAnswers(dg.data)
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	if rcode != 0 {
		r.Detail = fmt.Sprintf("rcode=%d", rcode)
		return r
	}
	r.OK = true
	if len(addrs) == 0 {
		r.Detail = "NOERROR empty"
		return r
	}
	var ss []string
	for _, a := range addrs {
		ss = append(ss, a.String())
	}
	r.Detail = strings.Join(ss, ",")
	return r
}

// runHTTP GETs (or POSTs -upload bytes) over TCP, with TLS when proto is https.
func runHTTP(ctx context.Context, v via, sp spec, timeout time.Duration, insecure bool, bodyBytes, uploadBytes int) result {
	port := sp.port
	if port == 0 {
		port = defaultPort(sp.proto)
	}
	path := sp.path
	if path == "" {
		path = "/"
	}
	hostport := net.JoinHostPort(sp.host, strconv.Itoa(port))
	disp := displayTarget(sp)
	r := result{Proto: sp.proto, Target: disp}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t0 := time.Now()
	c, err := v.dialTCP(cctx, hostport)
	if err != nil {
		r.RTT = time.Since(t0)
		r.Detail = err.Error()
		return r
	}
	defer c.Close()
	if sp.proto == "https" {
		cfg := &tls.Config{ServerName: sp.host, InsecureSkipVerify: insecure, NextProtos: []string{"http/1.1"}}
		tc := tls.Client(c, cfg)
		if err := tc.HandshakeContext(cctx); err != nil {
			r.RTT = time.Since(t0)
			r.Detail = "tls " + err.Error()
			return r
		}
		c = tc
	}
	hsDone := time.Since(t0)
	hostHdr := sp.host
	if (sp.proto == "https" && port != 443) || (sp.proto == "http" && port != 80) {
		hostHdr = hostport
	}
	if dl, ok := cctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	if uploadBytes > 0 {
		hdr := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: loft-probe\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", path, hostHdr, uploadBytes)
		if _, err := io.WriteString(c, hdr); err != nil {
			r.RTT = time.Since(t0)
			r.Detail = err.Error()
			return r
		}
		chunk := make([]byte, 32*1024)
		left := uploadBytes
		var sent int
		last := time.Now()
		stalls := 0
		var maxGap time.Duration
		for left > 0 {
			n := len(chunk)
			if n > left {
				n = left
			}
			wn, err := c.Write(chunk[:n])
			now := time.Now()
			if wn > 0 {
				gap := now.Sub(last)
				if gap > 250*time.Millisecond {
					stalls++
					if gap > maxGap {
						maxGap = gap
					}
				}
				last = now
				sent += wn
				left -= wn
			}
			if err != nil {
				r.RTT = time.Since(t0)
				r.Detail = fmt.Sprintf("upload %dB %v", sent, err)
				return r
			}
		}
		br := bufio.NewReader(c)
		line, err := br.ReadString('\n')
		r.RTT = time.Since(t0)
		status := strings.TrimSpace(line)
		if err != nil {
			r.Detail = err.Error()
			return r
		}
		code := 0
		if f := strings.Split(status, " "); len(f) >= 2 {
			code, _ = strconv.Atoi(f[1])
		}
		sec := r.RTT.Seconds()
		bps := 0.0
		if sec > 0 {
			bps = float64(sent) / sec
		}
		r.OK = code >= 200 && code < 400 && sent == uploadBytes
		xfer := r.RTT - hsDone
		if xfer > 0 {
			bps = float64(sent) / xfer.Seconds()
		}
		r.Detail = fmt.Sprintf("%s  upload %dB hs=%0.0fms xfer=%0.0fms %0.0f B/s stalls=%d maxgap=%0.0fms",
			status, sent, hsDone.Seconds()*1000, xfer.Seconds()*1000, bps, stalls, maxGap.Seconds()*1000)
		return r
	}
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: loft-probe\r\nAccept: */*\r\nConnection: close\r\n\r\n", path, hostHdr)
	if _, err := io.WriteString(c, req); err != nil {
		r.RTT = time.Since(t0)
		r.Detail = err.Error()
		return r
	}
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	r.RTT = time.Since(t0)
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	line = strings.TrimSpace(line)
	status := line
	code := 0
	if f := strings.Split(line, " "); len(f) >= 2 {
		code, _ = strconv.Atoi(f[1])
		status = f[1]
		if len(f) > 2 {
			status = strings.Join(f[1:], " ")
		}
	}
	for {
		h, err := br.ReadString('\n')
		if err != nil || h == "\r\n" || h == "\n" {
			break
		}
	}
	limit := int64(256)
	if bodyBytes > 0 {
		limit = int64(bodyBytes)
	}
	var (
		got    int64
		stalls int
		maxGap time.Duration
		last   = time.Now()
		ttfb   = r.RTT
	)
	buf := make([]byte, 4096)
	for got < limit {
		n, err := br.Read(buf)
		now := time.Now()
		if n > 0 {
			gap := now.Sub(last)
			if gap > 250*time.Millisecond {
				stalls++
				if gap > maxGap {
					maxGap = gap
				}
			}
			last = now
			got += int64(n)
		}
		if err != nil {
			if err != io.EOF && got == 0 {
				r.Detail = err.Error()
				return r
			}
			break
		}
	}
	r.RTT = time.Since(t0)
	r.OK = code >= 200 && code < 400 && (bodyBytes <= 0 || got > 0)
	if bodyBytes > 0 {
		sec := r.RTT.Seconds()
		bps := 0.0
		if sec > 0 {
			bps = float64(got) / sec
		}
		r.Detail = fmt.Sprintf("%s  %dB ttfb=%0.0fms %0.0f B/s stalls=%d maxgap=%0.0fms",
			status, got, ttfb.Seconds()*1000, bps, stalls, maxGap.Seconds()*1000)
		if bodyBytes > 0 && got < int64(bodyBytes)/2 {
			r.OK = false
		}
		return r
	}
	snippet := strings.TrimSpace(string(buf[:min(int(got), 256)]))
	snippet = strings.ReplaceAll(snippet, "\r", "")
	if i := strings.Index(snippet, "\n"); i > 0 {
		snippet = snippet[:i]
	}
	if len(snippet) > 80 {
		snippet = snippet[:80]
	}
	r.Detail = status
	if snippet != "" {
		r.Detail = status + "  " + snippet
	}
	return r
}

// hostEq compares IPs with Equal, otherwise hostnames case-insensitively.
func hostEq(a, b string) bool {
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	if ia != nil && ib != nil {
		return ia.Equal(ib)
	}
	return strings.EqualFold(a, b)
}

// printResults writes one line per result and returns the fail count (skips excluded).
func printResults(rs []result) int {
	fails := 0
	for _, r := range rs {
		state := "fail"
		if r.Skip {
			state = "skip"
		} else if r.OK {
			state = "ok"
		} else {
			fails++
		}
		rtt := ""
		if r.RTT > 0 && !r.Skip {
			rtt = fmt.Sprintf("%6.0fms", r.RTT.Seconds()*1000)
		} else {
			rtt = "       "
		}
		fmt.Printf("%-4s %-5s %-28s %s  %s\n", state, r.Proto, r.Target, rtt, r.Detail)
	}
	return fails
}
