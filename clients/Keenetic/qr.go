package keenetic

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/liyue201/goqr"
)

// TextFromImage reads the first QR payload. The picture is a loft://,
// vless://, trojan:// or a short YAML document.
func TextFromImage(b []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("image")
	}
	codes, err := goqr.Recognize(img)
	if err != nil || len(codes) == 0 {
		return "", fmt.Errorf("qr")
	}
	s := strings.TrimSpace(string(codes[0].Payload))
	if s == "" {
		return "", fmt.Errorf("qr")
	}
	return s, nil
}

func looksImage(name, ctype string, b []byte) bool {
	n := strings.ToLower(name)
	if strings.HasSuffix(n, ".png") || strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg") || strings.HasSuffix(n, ".gif") {
		return true
	}
	if strings.HasPrefix(ctype, "image/") {
		return true
	}
	if len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return true
	}
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return true
	}
	return false
}
