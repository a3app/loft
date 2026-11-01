package main

import "testing"

func TestPct(t *testing.T) {
	xs := []float64{5, 1, 4, 2, 3}
	if pct(xs, 50) != 3 || pct(xs, 95) != 5 || pct(nil, 50) != 0 {
		t.Fatalf("p50=%v p95=%v", pct(xs, 50), pct(xs, 95))
	}
}

func TestABVariants(t *testing.T) {
	vs := abVariants(true, true)
	if len(vs) != 2 || vs[0].disorder || vs[0].l3Shape || !vs[1].disorder || !vs[1].l3Shape || vs[1].name != "disorder+l3_shape" {
		t.Fatalf("%+v", vs)
	}
	if v := abVariants(false, true)[1]; v.name != "l3_shape" || v.disorder {
		t.Fatalf("%+v", v)
	}
}

func TestOriginLoopback(t *testing.T) {
	for in, want := range map[string]bool{"127.0.0.1:9474": true, "localhost:1": true, "[::1]:2": true, "203.0.113.5:9474": false, "bad": false} {
		if originLoopback(in) != want {
			t.Fatalf("%s", in)
		}
	}
}
