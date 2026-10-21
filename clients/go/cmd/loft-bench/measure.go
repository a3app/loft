package main

// Mux, SOCKS, and direct HTTPS measurements.

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/a3app/loft/internal/transport"
)

// measureMux runs echo RTT plus origin down/up over mux Open.
func measureMux(ctx context.Context, d transport.Dialer, origin string, nbyte int, timeout time.Duration, kind string) []row {
	var rows []row
	echo, err := withConn(ctx, timeout, func(cctx context.Context) (net.Conn, error) {
		return d.Open(origin)
	}, func(c net.Conn) (time.Duration, error) {
		return originEchoRTT(c)
	})
	er := row{Kind: kind, Path: "mux-rtt"}
	if err != nil {
		er.Err = err.Error()
		er.MS = float64(echo.Milliseconds())
	} else {
		er.MS = float64(echo.Milliseconds())
	}
	rows = append(rows, er)

	down, err := withConn(ctx, timeout, func(cctx context.Context) (net.Conn, error) {
		return d.Open(origin)
	}, func(c net.Conn) (time.Duration, error) {
		return originDownload(c, nbyte)
	})
	dr := row{Kind: kind, Path: "mux-down"}
	dr.MS = float64(down.Milliseconds())
	if err != nil {
		dr.Err = err.Error()
	} else {
		dr.Down = mbit(nbyte, down)
	}
	rows = append(rows, dr)

	up, err := withConn(ctx, timeout, func(cctx context.Context) (net.Conn, error) {
		return d.Open(origin)
	}, func(c net.Conn) (time.Duration, error) {
		return originUpload(c, nbyte)
	})
	ur := row{Kind: kind, Path: "mux-up"}
	ur.MS = float64(up.Milliseconds())
	if err != nil {
		ur.Err = err.Error()
	} else {
		ur.Up = mbit(nbyte, up)
	}
	rows = append(rows, ur)

	rows = append(rows, measureMuxMulti(ctx, d, origin, nbyte, 4, timeout, kind))
	return rows
}

// measureMuxMulti downloads nbyte split across nstream mux Opens at once.
// StreamCount>1 switches GET Puller to 32 KiB live bodies.
func measureMuxMulti(ctx context.Context, d transport.Dialer, origin string, nbyte, nstream int, timeout time.Duration, kind string) row {
	if nstream < 2 {
		nstream = 2
	}
	each := nbyte / nstream
	if each < 64<<10 {
		each = 64 << 10
	}
	type res struct {
		err error
	}
	ch := make(chan res, nstream)
	t0 := time.Now()
	for i := 0; i < nstream; i++ {
		go func() {
			_, err := withConn(ctx, timeout, func(cctx context.Context) (net.Conn, error) {
				return d.Open(origin)
			}, func(c net.Conn) (time.Duration, error) {
				return originDownload(c, each)
			})
			ch <- res{err: err}
		}()
	}
	var first error
	for i := 0; i < nstream; i++ {
		r := <-ch
		if r.err != nil && first == nil {
			first = r.err
		}
	}
	wall := time.Since(t0)
	out := row{Kind: kind, Path: "mux-down-4", MS: float64(wall.Milliseconds())}
	if first != nil {
		out.Err = first.Error()
	} else {
		out.Down = mbit(each*nstream, wall)
		out.Extra = fmt.Sprintf("%d streams × %dB", nstream, each)
	}
	return out
}

// measureSOCKS is the same origin bulk plus Cloudflare HTTPS through SOCKS.
func measureSOCKS(ctx context.Context, socksAddr, origin string, nbyte, httpN int, timeout time.Duration, kind string) []row {
	var rows []row
	down, err := withConn(ctx, timeout, func(cctx context.Context) (net.Conn, error) {
		return socksDial(cctx, socksAddr, origin)
	}, func(c net.Conn) (time.Duration, error) {
		return originDownload(c, nbyte)
	})
	dr := row{Kind: kind, Path: "socks-down"}
	if err != nil {
		dr.Err = err.Error()
	} else {
		dr.MS = float64(down.Milliseconds())
		dr.Down = mbit(nbyte, down)
	}
	rows = append(rows, dr)

	up, err := withConn(ctx, timeout, func(cctx context.Context) (net.Conn, error) {
		return socksDial(cctx, socksAddr, origin)
	}, func(c net.Conn) (time.Duration, error) {
		return originUpload(c, nbyte)
	})
	ur := row{Kind: kind, Path: "socks-up"}
	if err != nil {
		ur.Err = err.Error()
	} else {
		ur.MS = float64(up.Milliseconds())
		ur.Up = mbit(nbyte, up)
	}
	rows = append(rows, ur)

	cf, err := httpsThrough(ctx, socksAddr, httpN, timeout)
	cr := row{Kind: kind, Path: "socks-https"}
	if err != nil {
		cr.Err = err.Error()
	} else {
		cr.MS = float64(cf.Milliseconds())
		cr.Down = mbit(httpN, cf)
		cr.Extra = "speed.cloudflare.com"
	}
	rows = append(rows, cr)
	return rows
}

// measureDirect is HTTPS to Cloudflare without loft (clean-path sample).
func measureDirect(ctx context.Context, httpN int, timeout time.Duration) []row {
	t0 := time.Now()
	err := httpsDirect(ctx, httpN, timeout)
	dt := time.Since(t0)
	r := row{Kind: "direct", Path: "https", MS: float64(dt.Milliseconds()), Extra: "speed.cloudflare.com"}
	if err != nil {
		r.Err = err.Error()
	} else {
		r.Down = mbit(httpN, dt)
	}
	return []row{r}
}

func mbit(n int, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / d.Seconds() * 8 / 1e6
}

func withConn(ctx context.Context, timeout time.Duration, dial func(context.Context) (net.Conn, error), fn func(net.Conn) (time.Duration, error)) (time.Duration, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := dial(cctx)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	return fn(c)
}

func httpsThrough(ctx context.Context, socksAddr string, n int, timeout time.Duration) (time.Duration, error) {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return socksDial(ctx, socksAddr, addr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
	}
	return httpsGet(ctx, tr, n, timeout)
}

func httpsDirect(ctx context.Context, n int, timeout time.Duration) error {
	_, err := httpsGet(ctx, http.DefaultTransport, n, timeout)
	return err
}

func httpsGet(ctx context.Context, tr http.RoundTripper, n int, timeout time.Duration) (time.Duration, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := "https://speed.cloudflare.com/__down?bytes=" + strconv.Itoa(n)
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	cl := &http.Client{Transport: tr}
	t0 := time.Now()
	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("http %s", resp.Status)
	}
	got, err := io.Copy(io.Discard, resp.Body)
	dt := time.Since(t0)
	if err != nil {
		return dt, err
	}
	if int(got) < n/2 {
		return dt, fmt.Errorf("short body %d", got)
	}
	return dt, nil
}
