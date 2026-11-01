package main

import (
	"strings"
	"testing"

	"github.com/a3app/loft/internal/config"
)

func TestOutboundForH2IgnoresGETCoverPath(t *testing.T) {
	for _, kind := range []string{"h2", "h3", "l3"} {
		for _, path := range []string{"", "/static/js", "/static/v3/build", "app.js"} {
			o := outboundFor("https://loft.example:444", kind, path, "")
			if o.Path != config.DefaultH2Path {
				t.Fatalf("%s path %q → %q", kind, path, o.Path)
			}
		}
		o := outboundFor("https://loft.example:444", kind, "/custom/connect", "")
		if o.Path != "/custom/connect" {
			t.Fatalf("%s custom %q", kind, o.Path)
		}
	}
	get := outboundFor("https://loft.example:444", "get", "/static/v3/build", "")
	if get.Path != "/static/v3/build" {
		t.Fatalf("get kept %q", get.Path)
	}
}

func TestDialFrontOrderL3(t *testing.T) {
	o := outboundFor("https://loft.example:444", "l3", "/static/v3/build", "")
	if o.Type != "l3" || o.Path != config.DefaultH2Path {
		t.Fatalf("%+v", o)
	}
	if !strings.HasPrefix(o.URL, "https://") {
		t.Fatalf("url %s", o.URL)
	}
}

func TestOutboundForPBK(t *testing.T) {
	t.Cleanup(func() { probePBK = "" })
	probePBK = "q3ucJuIkRdJYS8_N0Si4U7FA3Qo7jqcblDghyDUXIc8"
	o := outboundFor("https://loft.example:444", "quic", "", "")
	if o.MaskPub != probePBK {
		t.Fatalf("pbk %q", o.MaskPub)
	}
}
