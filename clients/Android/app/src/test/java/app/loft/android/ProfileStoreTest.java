package app.loft.android;

import org.junit.Test;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public final class ProfileStoreTest {
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
