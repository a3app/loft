package protect

import "testing"

func TestTunnelIface(t *testing.T) {
	for _, n := range []string{"utun6", "tun0", "ppp0", "lo0", "awdl0", "ipsec0", "wg0", "Loft"} {
		if !tunnelIface(n) {
			t.Fatalf("%s", n)
		}
	}
	for _, n := range []string{"en0", "en1", "bridge0", "eth0", "wlan0"} {
		if tunnelIface(n) {
			t.Fatalf("%s", n)
		}
	}
}

func TestParseNetstatDefaultsSkipsVPN(t *testing.T) {
	s := "" +
		"Internet:\n" +
		"Destination        Gateway            Flags        Netif Expire\n" +
		"default            172.16.223.1       UGSc           utun6\n" +
		"default            192.168.12.1       UGScIg           en0\n" +
		"default            link#20            UCSIg          utun6\n" +
		"10.8/24            10.8.0.1           UGSc           utun3\n"
	got := pickPhysical(parseNetstatDefaults(s))
	if got != "en0" {
		t.Fatal(got)
	}
}

func TestPickLinuxRouteSkipsTun(t *testing.T) {
	s := "" +
		"Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"tun0\t00000000\t0100A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"wlan0\t00000000\t0100A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"
	if g := pickLinuxRoute(s); g != "wlan0" {
		t.Fatal(g)
	}
}

func TestPhysicalNameNotTunnel(t *testing.T) {
	n := PhysicalName()
	if n == "" {
		t.Skip("no uplink")
	}
	if tunnelIface(n) {
		t.Fatal(n)
	}
}

func TestBindUplinkEmptyPinsPhysical(t *testing.T) {
	t.Cleanup(func() { _ = Bind("") })
	if err := BindUplink(""); err != nil {
		t.Fatal(err)
	}
	n := PhysicalName()
	if n == "" {
		if Bound() != "" {
			t.Fatal(Bound())
		}
		return
	}
	if BoundName() != n {
		t.Fatalf("bound %q want %q", BoundName(), n)
	}
	bindMu.Lock()
	auto := bindAuto
	bindMu.Unlock()
	if !auto {
		t.Fatal("auto")
	}
}
