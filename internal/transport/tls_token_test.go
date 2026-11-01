package transport

import (
	"testing"

	"github.com/a3app/loft/internal/config"
)

func TestTLSTokenSkipsMaskOnCDN(t *testing.T) {
	tok := []byte("lt_0123456789abcdef0123456789abcdef0123456789abcdef")
	if got := tlsToken(config.Outbound{CDN: "yandex"}, tok); got != nil {
		t.Fatalf("cdn yandex still injects token")
	}
	if got := tlsToken(config.Outbound{CDN: " cloudflare "}, tok); got != nil {
		t.Fatalf("cdn with spaces still injects token")
	}
	if got := tlsToken(config.Outbound{}, tok); string(got) != string(tok) {
		t.Fatalf("direct path dropped token")
	}
}
