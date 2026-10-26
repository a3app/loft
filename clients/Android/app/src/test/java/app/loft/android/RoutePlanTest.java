package app.loft.android;

import org.junit.Test;

import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public final class RoutePlanTest {
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

    private static boolean covers(List<Cidr> list, String host) {
        Cidr h = Cidr.parse(host);
        for (Cidr c : list) {
            if (c.contains(h)) {
                return true;
            }
        }
        return false;
    }

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
