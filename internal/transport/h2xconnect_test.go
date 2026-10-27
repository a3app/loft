package transport

import (
	"slices"
	"testing"
)

func TestEnableHTTP2ExtendedConnect(t *testing.T) {
	http2DisableXConnect = true
	EnableHTTP2ExtendedConnect()
	if http2DisableXConnect {
		t.Fatal("SETTINGS_ENABLE_CONNECT_PROTOCOL still off")
	}
}

func TestWithXConnect(t *testing.T) {
	for _, c := range []struct {
		in      []string
		want    []string
		changed bool
	}{
		{[]string{"PATH=/bin"}, []string{"PATH=/bin", "GODEBUG=http2xconnect=1"}, true},
		{[]string{"GODEBUG="}, []string{"GODEBUG=http2xconnect=1"}, true},
		{[]string{"GODEBUG=madvdontneed=1", "HOME=/"}, []string{"GODEBUG=madvdontneed=1,http2xconnect=1", "HOME=/"}, true},
		{[]string{"GODEBUG=http2xconnect=1"}, []string{"GODEBUG=http2xconnect=1"}, false},
		// An operator who turned it off on purpose keeps that.
		{[]string{"GODEBUG=x=1,http2xconnect=0"}, []string{"GODEBUG=x=1,http2xconnect=0"}, false},
	} {
		got, changed := withXConnect(c.in)
		if !slices.Equal(got, c.want) || changed != c.changed {
			t.Fatalf("withXConnect(%q) = %q, %v; want %q, %v", c.in, got, changed, c.want, c.changed)
		}
	}
}
