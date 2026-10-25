package transport

import "testing"

func TestWsDialURL(t *testing.T) {
	ok := []struct{ in, want string }{
		{"wss://node.example/api/v1/sync", "wss://node.example/api/v1/sync"},
		{"https://node.example/api/v1/sync", "wss://node.example/api/v1/sync"},
		{"ws://127.0.0.1:8080/ws", "ws://127.0.0.1:8080/ws"},
		{"http://127.0.0.1:8080/ws", "ws://127.0.0.1:8080/ws"},
		{"wss://user:pass@node.example/x", "wss://node.example/x"},
		{"wss://node.example/x#frag", "wss://node.example/x"},
	}
	for _, c := range ok {
		got, err := wsDialURL(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("%s: got %q want %q", c.in, got, c.want)
		}
	}
	for _, in := range []string{"", "file:///etc/passwd", "unix:///tmp/x", "/api/v1/sync", "wss://"} {
		if _, err := wsDialURL(in); err == nil {
			t.Fatalf("%s: expected error", in)
		}
		if isLocalUrl(in) {
			t.Fatalf("%s: isLocalUrl", in)
		}
	}
	if !isLocalUrl("wss://node.example/api/v1/sync") {
		t.Fatal("isLocalUrl wss")
	}
}
