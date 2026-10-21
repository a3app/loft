package transport

import "testing"

func TestEnableHTTP2ExtendedConnect(t *testing.T) {
	http2DisableXConnect = true
	EnableHTTP2ExtendedConnect()
	if http2DisableXConnect {
		t.Fatal("SETTINGS_ENABLE_CONNECT_PROTOCOL still off")
	}
}
