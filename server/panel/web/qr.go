package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"rsc.io/qr"
)

// QR codes are drawn here, on the server: a member's link carries its key,
// and it must not go to a third-party QR service or a page script fetched
// from elsewhere.

// qrSVG draws text as a QR code (error correction M, quiet zone 4).
func qrSVG(text string) (string, error) {
	if text == "" || len(text) > 2048 {
		return "", errors.New("QR: 1–2048 characters")
	}
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	n := c.Size + 8
	var d strings.Builder
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if !c.Black(x, y) {
				continue
			}
			run := 1
			for x+run < c.Size && c.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&d, "M%d %dh%dv1h-%dz", x+4, y+4, run, run)
			x += run - 1
		}
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="QR"><rect width="%d" height="%d" fill="#fff"/><path fill="#1A1714" d="%s"/></svg>`, n, n, n, n, d.String()), nil
}

// qrGet draws a QR code of a link the page already holds (a profile link).
// The text is not logged.
func (s *Server) qrGet(w http.ResponseWriter, r *http.Request) {
	auditSkip(r)
	var in struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	svg, err := qrSVG(in.Text)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"svg": svg})
}
