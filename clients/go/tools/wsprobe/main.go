// Command wsprobe is a one-shot HTTP GET through a loft GET front.
// Manual transport check. Production probe is loft-probe.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/transport"
)

// main connects one GET front, opens a stream to -open through it, sends a
// plain HTTP/1.1 GET and prints the status line and the first 200 body
// bytes. Despite the name, it uses the GET front, not WebSocket.
func main() {
	token := flag.String("token", os.Getenv("LOFT_TOKEN"), "member key")
	rawURL := flag.String("url", os.Getenv("LOFT_URL"), "GET front URL")
	path := flag.String("path", "/static/js", "GET path")
	open := flag.String("open", "example.com:80", "host:port to open on the overlay")
	insecure := flag.Bool("insecure", true, "skip TLS verify on the front")
	timeout := flag.Duration("timeout", 25*time.Second, "handshake + fetch timeout")
	flag.Parse()
	if *token == "" || *rawURL == "" {
		fmt.Fprintln(os.Stderr, "wsprobe: need -token or LOFT_TOKEN, and -url or LOFT_URL")
		flag.Usage()
		os.Exit(2)
	}

	// Connect the front; the handshake and the fetch share one timeout.
	o := config.Outbound{
		Name: "probe-get", Type: "get",
		URL: *rawURL, Path: *path,
		Fingerprint: "chrome", Insecure: *insecure,
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	d, err := transport.Start(ctx, *token, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start %v\n", err)
		os.Exit(2)
	}
	defer d.Close()
	fmt.Fprintf(os.Stderr, "up overlay=%s\n", transport.OverlayIP())
	// Open a mux stream to -open and do one HTTP request over it.
	c, err := d.Open(*open)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %v\n", err)
		os.Exit(2)
	}
	defer c.Close()
	host, _, splitErr := net.SplitHostPort(*open)
	if splitErr != nil {
		host = *open
	}
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"))
	_ = c.SetDeadline(time.Now().Add(12 * time.Second))
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	fmt.Fprintf(os.Stderr, "http %q err=%v\n", line, err)
	rest, _ := io.ReadAll(io.LimitReader(br, 200))
	fmt.Fprintf(os.Stderr, "body %q\n", rest)
}
