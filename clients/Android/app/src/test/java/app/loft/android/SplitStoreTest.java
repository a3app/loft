package app.loft.android;

import org.junit.Test;

import java.util.Collections;
import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public final class SplitStoreTest {
    @Test
    public void repoDeleteMovesCidrsToExclusions() {
        SplitStore st = new SplitStore();
        st.seed();
        SplitStore.Svc yt = new SplitStore.Svc();
        yt.name = "YouTube";
        yt.zone = SplitStore.GLOBAL;
        yt.src = "repo";
        yt.cidrs.add("142.250.0.0/16");
        yt.cidrs.add("172.217.0.0/16");
        st.services.add(yt);
        SplitStore.Snap snap = st.snap();
        SplitStore.Gone g = st.removeSvcs(Collections.singletonList(yt.id));
        assertEquals(1, g.n);
        assertTrue(g.toExcl);
        assertEquals(0, st.count(SplitStore.GLOBAL));
        assertEquals(2, st.exclCount(SplitStore.GLOBAL));
        st.restore(snap);
        assertEquals("YouTube", st.svc(yt.id).name);
        assertEquals(0, st.exclCount(SplitStore.GLOBAL));
    }

    @Test
    public void ownDeleteDoesNotCreateExclusion() {
        SplitStore st = new SplitStore();
        st.seed();
        SplitStore.Gone g = st.removeSvcs(Collections.singletonList("lan"));
        assertFalse(g.toExcl);
        assertEquals(0, st.count(SplitStore.LOCAL));
        assertEquals(0, st.exclCount(SplitStore.LOCAL));
    }

    @Test
    public void clearAllUnlinksRepo() {
        SplitStore st = new SplitStore();
        st.seed();
        SplitStore.Svc yt = new SplitStore.Svc();
        yt.name = "YouTube";
        yt.zone = SplitStore.GLOBAL;
        yt.src = "repo";
        yt.cidrs.add("142.250.0.0/16");
        st.services.add(yt);
        SplitStore.Svc mine = new SplitStore.Svc();
        mine.name = "Mine";
        mine.zone = SplitStore.GLOBAL;
        mine.src = "me";
        mine.cidrs.add("1.2.3.0/24");
        st.services.add(mine);
        st.clearZone(SplitStore.GLOBAL, "all");
        assertEquals(0, st.count(SplitStore.GLOBAL));
        assertEquals("", st.zone(SplitStore.GLOBAL).repo);
        assertFalse(st.zone(SplitStore.GLOBAL).auto);
    }

    @Test
    public void addRowsSkipsExistingCidr() {
        SplitStore st = new SplitStore();
        st.seed();
        Bat.Row a = new Bat.Row();
        a.name = "LAN";
        a.cidr = "10.0.0.0/8";
        Bat.Row b = new Bat.Row();
        b.name = "Office";
        b.cidr = "203.0.113.0/24";
        int n = st.addRows(SplitStore.LOCAL, java.util.Arrays.asList(a, b));
        assertEquals(1, n);
        assertEquals(2, st.count(SplitStore.LOCAL));
    }

    @Test
    public void emitAllWritesEveryZone() {
        SplitStore st = new SplitStore();
        st.seed();
        String bat = Bat.emitAll(st);
        assertTrue(bat.contains("REM zone: local"));
        assertTrue(bat.contains("REM zone: global"));
        assertTrue(bat.contains(":: rem LAN"));
        List<Bat.Row> rows = Bat.parse(bat);
        assertEquals(3, rows.size());
    }
}
