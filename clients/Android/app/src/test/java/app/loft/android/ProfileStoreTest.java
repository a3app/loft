/*
 * Android client unit tests (JVM, no device) for ProfileStore, the catalog of
 * route profiles: copying a profile marks its services as the user's own,
 * and the operator profile (sent by the node) can be the active one.
 */
package app.loft.android;

import org.junit.Test;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

/** Tests for copying route profiles and detecting the operator profile. */
public final class ProfileStoreTest {
    /**
     * Copying the operator profile gives a new id and marks the copy and its services as the
     * user's own ("me" instead of "op").
     */
    @Test
    public void copyMarksServicesMine() {
        ProfileStore ps = new ProfileStore();
        ProfileStore.Prof op = new ProfileStore.Prof();
        op.id = ProfileStore.OP;
        op.src = "op";
        op.name = "RU direct";
        SplitStore.Svc s = new SplitStore.Svc();
        s.src = "op";
        s.name = "Russia";
        op.store.services.add(s);
        ps.profiles.add(op);
        ProfileStore.Prof copy = ps.copyOf(op, "RU direct (copy)");
        assertEquals("me", copy.src);
        assertEquals("me", copy.store.services.get(0).src);
        assertEquals("RU direct (copy)", copy.name);
        assertEquals(2, ps.profiles.size());
        assertFalse(copy.id.equals(op.id));
    }

    /** operatorActive() is true only while the profile sent by the node is selected. */
    @Test
    public void operatorActive() {
        ProfileStore ps = new ProfileStore();
        ProfileStore.Prof mine = new ProfileStore.Prof();
        mine.id = ProfileStore.MINE;
        mine.src = "me";
        ps.profiles.add(mine);
        ps.active = ProfileStore.MINE;
        assertFalse(ps.operatorActive());
        ProfileStore.Prof op = new ProfileStore.Prof();
        op.id = ProfileStore.OP;
        op.src = "op";
        ps.profiles.add(op);
        ps.active = ProfileStore.OP;
        assertTrue(ps.operatorActive());
    }
}
