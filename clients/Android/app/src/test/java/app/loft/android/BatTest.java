package app.loft.android;

import org.junit.Test;

import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public final class BatTest {
    @Test
    public void parseRouteAddAndIgnore() {
        String bat = "@echo off\nREM zone: global\n"
                + "route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube\n"
                + "REM ignore 104.16.0.0/12\n";
        List<Bat.Row> rows = Bat.parse(bat);
        assertEquals(2, rows.size());
        assertEquals("YouTube", rows.get(0).name);
        assertEquals("142.250.0.0/16", rows.get(0).cidr);
        assertFalse(rows.get(0).ignore);
        assertTrue(rows.get(1).ignore);
        assertEquals("104.16.0.0/12", rows.get(1).cidr);
        assertEquals("global", Bat.zoneHint(bat));
    }

    @Test
    public void ipMaskBits() {
        Cidr c = Cidr.fromIpMask("10.0.0.0", "255.0.0.0");
        assertEquals("10.0.0.0/8", c.display());
        assertEquals("255.255.0.0", Cidr.dottedMask(16));
    }

    @Test
    public void seedHasLocalLanAndGlobal() {
        SplitStore st = new SplitStore();
        st.seed();
        assertTrue(st.zone(SplitStore.LOCAL).builtin);
        assertFalse(st.zone(SplitStore.LOCAL).through);
        assertTrue(st.zone(SplitStore.GLOBAL).through);
        assertEquals(1, st.count(SplitStore.LOCAL));
        assertEquals(3, st.svc("lan").cidrs.size());
    }

    @Test
    public void autoDueWhenNeverPulled() {
        SplitStore st = new SplitStore();
        st.seed();
        assertTrue(st.due(st.zone(SplitStore.GLOBAL)));
        st.zone(SplitStore.GLOBAL).pulled = System.currentTimeMillis();
        assertFalse(st.due(st.zone(SplitStore.GLOBAL)));
        assertFalse(st.due(st.zone(SplitStore.LOCAL)));
        st.zone(SplitStore.GLOBAL).auto = false;
        st.zone(SplitStore.GLOBAL).pulled = 0;
        assertFalse(st.due(st.zone(SplitStore.GLOBAL)));
    }
}
