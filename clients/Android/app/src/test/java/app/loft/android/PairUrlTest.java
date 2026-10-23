package app.loft.android;

import org.junit.Test;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public final class PairUrlTest {
    @Test
    public void buildAndDetect() {
        String u = PairUrl.build("192.168.1.8", 43111, "K7M2P4");
        assertEquals("http://192.168.1.8:43111/pair?c=K7M2P4", u);
        assertTrue(PairUrl.isPair(u));
        assertEquals("K7M2P4", PairUrl.code(u));
    }

    @Test
    public void loftUriIsNotPair() {
        assertFalse(PairUrl.isPair("loft://lt_ab@api.example/static/js?type=get"));
        assertFalse(PairUrl.isPair("http://192.168.1.8:80/"));
        assertFalse(PairUrl.isPair(""));
        assertFalse(PairUrl.isPair(null));
    }
}
