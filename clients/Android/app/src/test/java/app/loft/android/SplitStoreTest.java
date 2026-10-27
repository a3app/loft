/*
 * Android client unit tests (JVM, no device) for SplitStore: deleting
 * repository versus user services, clearing a zone, skipping duplicate
 * prefixes on import, and exporting every zone.
 */
package app.loft.android;

import org.junit.Test;

import java.util.Collections;
import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

/** Tests for deleting, clearing, importing and exporting split-list entries. */
public final class SplitStoreTest {
    /**
     * Deleting a service that came from a repository (downloaded list) turns its prefixes into
     * exclusions so the next auto-update does not bring it back; restoring the snapshot undoes it.
     */
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

    /** Deleting a user's own service simply removes it, with no exclusion left behind. */
    @Test
    public void ownDeleteDoesNotCreateExclusion() {
        SplitStore st = new SplitStore();
        st.seed();
        SplitStore.Gone g = st.removeSvcs(Collections.singletonList("lan"));
        assertFalse(g.toExcl);
        assertEquals(0, st.count(SplitStore.LOCAL));
        assertEquals(0, st.exclCount(SplitStore.LOCAL));
    }

    /** Clearing a whole zone also forgets its repository and turns off auto-update. */
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

    /** Importing rows skips prefixes the zone already has. */
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

    /** Catalog URLs sit under loft-routes; labels follow the UI language; a null id is not a zone. */
    @Test
    public void catalogUrlAndLabel() {
        assertEquals("https://raw.githubusercontent.com/a3app/loft-routes/main/ru.bat",
                SplitStore.catalogUrl("ru"));
        assertEquals("Россия", SplitStore.catalogLabel("ru", "ru"));
        assertEquals("Russia", SplitStore.catalogLabel("ru", "en"));
        assertEquals("俄罗斯", SplitStore.catalogLabel("ru", "zh"));
        assertEquals("Мир", SplitStore.catalogLabel("global", "ru"));
        SplitStore st = new SplitStore();
        st.seed();
        assertTrue(st.zone(null) == null);
        SplitStore.Zone nameless = new SplitStore.Zone();
        nameless.id = null;
        st.zones.add(nameless);
        assertTrue(st.zone(SplitStore.GLOBAL) != null);
    }

    /**
     * Exporting writes a zone header for every zone and the result parses back to the same rows.
     */
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
