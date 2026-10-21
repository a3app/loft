package web

import (
	"strings"
	"testing"

	"rsc.io/qr"
)

func TestQRSVG(t *testing.T) {
	link := "loft://lt_" + strings.Repeat("a", 48) + "@vpn.example.net/static/js?type=get&fp=chrome#alice-phone"
	svg, err := qrSVG(link)
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, `fill="#1A1714" d="M4 4h7`) {
		t.Fatalf("%v %.200s", err, svg)
	}
	// The same code as the library: the finder pattern starts the first row.
	c, _ := qr.Encode(link, qr.M)
	if !c.Black(0, 0) || c.Size < 21 {
		t.Fatal("encode")
	}
	if _, err := qrSVG(""); err == nil {
		t.Fatal("empty text")
	}
	if _, err := qrSVG(strings.Repeat("x", 3000)); err == nil {
		t.Fatal("too long")
	}
}
