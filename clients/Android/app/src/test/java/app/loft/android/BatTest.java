/*
 * Android client unit tests (JVM, no device) for Bat: parsing Keenetic /
 * loft-routes .bat lists, mask-to-prefix conversion, and the SplitStore
 * helpers that use them (seed zones, row previews, auto-update timing).
 */
package app.loft.android;

import org.junit.Test;

import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

/** Tests for the .bat list parser and the SplitStore helpers built on it. */
public final class BatTest {
    /**
     * A {@code route ADD} line becomes a named prefix, a {@code REM ignore} line becomes an
     * ignore row, and the {@code REM zone:} header is read as the zone hint.
     */
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

    /** Dotted masks convert to prefix lengths and back (255.0.0.0 is /8, /16 is 255.255.0.0). */
    @Test
    public void ipMaskBits() {
        Cidr c = Cidr.fromIpMask("10.0.0.0", "255.0.0.0");
        assertEquals("10.0.0.0/8", c.display());
        assertEquals("255.255.0.0", Cidr.dottedMask(16));
    }

    /**
     * A fresh store has a built-in Local zone that bypasses the tunnel, a Global zone that goes
     * through it, and one LAN service with the three private IPv4 ranges.
     */
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

    /**
     * Row previews show the first prefix plus a count instead of the whole list, and search inside
     * a zone matches by address or by service name.
     */
    @Test
    public void previewDoesNotJoinEveryCidr() {
        SplitStore.Svc s = new SplitStore.Svc();
        s.cidrs.add("1.1.1.1/32");
        assertEquals("1.1.1.1/32", SplitStore.preview(s.cidrs));
        s.cidrs.add("1.0.0.1/32");
        assertEquals("1.1.1.1/32 +1", SplitStore.preview(s.cidrs));
        s.name = "Cloud";
        s.zone = SplitStore.GLOBAL;
        SplitStore st = new SplitStore();
        st.seed();
        st.services.add(s);
        assertEquals(1, st.in(SplitStore.GLOBAL, "1.0.0.1").size());
        assertEquals(0, st.in(SplitStore.GLOBAL, "9.9.9.9").size());
        assertEquals(1, st.in(SplitStore.GLOBAL, "cloud").size());
    }

    /**
     * An auto-updating zone is due when it was never downloaded, not due right after a download,
     * and never due when auto-update is off or the zone has no source.
     */
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
