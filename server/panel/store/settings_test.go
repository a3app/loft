package store

import "testing"

func TestOutboundName(t *testing.T) {
	host, member, front := "api.iqos.help:443", "bob", "get"
	cases := []struct {
		o    *LoftOutbound
		want string
	}{
		{nil, "bob-get"},
		{&LoftOutbound{Member: true, Front: true}, "bob-get"},
		{&LoftOutbound{Host: true, Front: true}, "api.iqos.help-get"},
		{&LoftOutbound{Host: true, Member: true, Front: true}, "api.iqos.help-bob-get"},
		{&LoftOutbound{Front: true}, "get"},
		{&LoftOutbound{Host: true, Member: true}, "api.iqos.help-bob"},
		{&LoftOutbound{}, "get"},
	}
	for _, c := range cases {
		got := Settings{LoftOutbound: c.o}.OutboundName(host, member, front)
		if got != c.want {
			t.Errorf("%+v: got %q want %q", c.o, got, c.want)
		}
	}
	if g := (Settings{}).OutboundName("https://Api.Example.NET/x", "alice", "h2"); g != "alice-h2" {
		t.Errorf("default with url host: %q", g)
	}
	if g := (Settings{LoftOutbound: &LoftOutbound{Host: true, Front: true}}).OutboundName("https://Api.Example.NET/x", "alice", "h2"); g != "api.example.net-h2" {
		t.Errorf("host from url: %q", g)
	}
}
