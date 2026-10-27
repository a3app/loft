/*
 * Android client unit tests (JVM, no device) for RoutePlan, which turns split
 * lists and LAN bypass into TUN routes. Covers the API 33+ excludeRoute path
 * and the older subtract-from-default path, plus block, ignore and the plan
 * key that tells LoftVpnService when to rebuild the TUN.
 */
package app.loft.android;

import org.junit.Test;

import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

/**
 * Tests for building the TUN route plan. RoutePlan.build arguments, in order: default route
 * through the tunnel, LAN bypass, split on, split store, API 33+ (excludeRoute available),
 * overlay address and prefix length, IPv6 address and length, node-pushed routes, and front
 * IPs (node addresses that must bypass the tunnel so it does not loop into itself).
 */
public final class RoutePlanTest {
    /** Below API 33 the LAN is cut out of the default route; the overlay address stays routed. */
    @Test
    public void subtractLanOnOldApi() {
        RoutePlan p = RoutePlan.build(true, true, false, null, false,
                "10.8.0.2", 24, "", 0, "", "");
        assertFalse(covers(p.include, "192.168.1.1"));
        assertFalse(covers(p.include, "10.0.0.1"));
        assertTrue(covers(p.include, "8.8.8.8"));
        assertTrue(covers(p.include, "10.8.0.2"));
        assertTrue(p.exclude.isEmpty());
    }

    /**
     * On API 33+ the default route stays whole and LAN ranges plus front IPs become exclusions.
     */
    @Test
    public void excludeLanOnApi33() {
        RoutePlan p = RoutePlan.build(true, true, false, null, true,
                "10.8.0.2", 24, "", 0, "", "1.1.1.1");
        assertTrue(hasPrefix(p.include, "0.0.0.0/0"));
        assertTrue(covers(p.include, "8.8.8.8"));
        assertTrue(hasPrefix(p.exclude, "10.0.0.0/8"));
        assertTrue(hasPrefix(p.exclude, "192.168.0.0/16"));
        assertTrue(hasPrefix(p.exclude, "1.1.1.1/32"));
    }

    /** The seeded Local zone (private ranges) becomes exclusions on API 33+. */
    @Test
    public void splitBypassIsExcludeOnApi33() {
        SplitStore st = new SplitStore();
        st.seed();
        RoutePlan p = RoutePlan.build(true, false, true, st, true,
                "10.8.0.2", 24, "", 0, "", "");
        assertTrue(hasPrefix(p.include, "0.0.0.0/0"));
        assertTrue(hasPrefix(p.exclude, "10.0.0.0/8"));
        assertTrue(hasPrefix(p.exclude, "172.16.0.0/12"));
        assertTrue(hasPrefix(p.exclude, "192.168.0.0/16"));
    }

    /** Without the default route, only Global-zone prefixes and the overlay go into the TUN. */
    @Test
    public void splitThroughWithoutDefault() {
        SplitStore st = new SplitStore();
        st.seed();
        SplitStore.Svc s = new SplitStore.Svc();
        s.name = "yt";
        s.zone = SplitStore.GLOBAL;
        s.cidrs.add("142.250.0.0/16");
        st.services.add(s);
        RoutePlan p = RoutePlan.build(false, true, true, st, true,
                "10.8.0.2", 24, "", 0, "", "");
        assertTrue(hasPrefix(p.include, "142.250.0.0/16"));
        assertTrue(covers(p.include, "10.8.0.2"));
        assertFalse(covers(p.include, "8.8.8.8"));
    }

    /**
     * Block-zone prefixes are routed into the TUN and marked to drop, so traffic is blackholed
     * rather than leaking around the tunnel.
     */
    @Test
    public void splitBlockGoesToDropAndInclude() {
        SplitStore st = new SplitStore();
        st.seed();
        SplitStore.Svc ads = new SplitStore.Svc();
        ads.name = "Ads";
        ads.zone = SplitStore.BLOCK;
        ads.cidrs.add("6.6.6.0/24");
        st.services.add(ads);
        RoutePlan p = RoutePlan.build(true, false, true, st, true,
                "10.8.0.2", 24, "", 0, "", "");
        assertTrue(hasPrefix(p.include, "6.6.6.0/24"));
        assertTrue(hasPrefix(p.drop, "6.6.6.0/24"));
        assertFalse(hasPrefix(p.exclude, "6.6.6.0/24"));
    }

    /** An ignore entry removes its prefix from the tunnel. */
    @Test
    public void splitIgnoreIsExclude() {
        SplitStore st = new SplitStore();
        st.seed();
        SplitStore.Svc ign = new SplitStore.Svc();
        ign.ignore = true;
        ign.src = "ignore";
        ign.zone = SplitStore.GLOBAL;
        ign.cidrs.add("1.1.1.0/24");
        st.services.add(ign);
        RoutePlan p = RoutePlan.build(true, true, true, st, true,
                "10.8.0.2", 24, "", 0, "", "");
        assertTrue(hasPrefix(p.exclude, "1.1.1.0/24"));
    }

    /** When split is off, the store's services have no effect on routes. */
    @Test
    public void splitOffIgnoresStore() {
        SplitStore st = new SplitStore();
        st.seed();
        SplitStore.Svc s = new SplitStore.Svc();
        s.zone = SplitStore.GLOBAL;
        s.cidrs.add("8.8.8.8/32");
        st.services.add(s);
        RoutePlan p = RoutePlan.build(true, true, false, st, true,
                "10.8.0.2", 24, "", 0, "", "");
        assertFalse(hasPrefix(p.include, "8.8.8.8/32"));
    }

    /**
     * Adding a through-tunnel service changes the plan key, which tells the VPN service to
     * rebuild the TUN.
     */
    @Test
    public void keyChangesWhenSplitAddsThrough() {
        SplitStore st = new SplitStore();
        st.seed();
        RoutePlan a = RoutePlan.build(true, true, true, st, true,
                "10.8.0.2", 24, "", 0, "", "");
        SplitStore.Svc s = new SplitStore.Svc();
        s.zone = SplitStore.GLOBAL;
        s.cidrs.add("8.8.8.8/32");
        st.services.add(s);
        RoutePlan b = RoutePlan.build(true, true, true, st, true,
                "10.8.0.2", 24, "", 0, "", "");
        assertFalse(a.key().equals(b.key()));
    }

    /** Below API 33 the Local zone is subtracted from the default route instead of excluded. */
    @Test
    public void splitBypassSubtractsOnOldApi() {
        SplitStore st = new SplitStore();
        st.seed();
        RoutePlan p = RoutePlan.build(true, false, true, st, false,
                "10.8.0.2", 24, "", 0, "", "");
        assertFalse(covers(p.include, "192.168.1.1"));
        assertTrue(covers(p.include, "8.8.8.8"));
        assertTrue(covers(p.include, "10.8.0.2"));
        assertTrue(p.exclude.isEmpty());
    }

    /** Returns true if any prefix in the list contains the given host. */
    private static boolean covers(List<Cidr> list, String host) {
        Cidr h = Cidr.parse(host);
        for (Cidr c : list) {
            if (c.contains(h)) {
                return true;
            }
        }
        return false;
    }

    /** Returns true if the list holds exactly this prefix (compared in canonical form). */
    private static boolean hasPrefix(List<Cidr> list, String cidr) {
        Cidr want = Cidr.parse(cidr).canonical();
        for (Cidr c : list) {
            if (c.canonical().display().equals(want.display())) {
                return true;
            }
        }
        return false;
    }
}
