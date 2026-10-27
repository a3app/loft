/*
 * Android client unit tests (JVM, no device) for PairUrl, the phone-to-TV
 * LAN pairing URL: building and detecting it, not confusing it with a
 * loft:// link, and accepting only a POST that carries the right code.
 */
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

    @Test
    public void onlyPostWithTheCodeIsAuthorized() {
        assertTrue(PairUrl.authorized("POST /pair?c=K7M2P4 HTTP/1.1", "K7M2P4"));
        assertFalse(PairUrl.authorized("POST /pair?c=K7M2P5 HTTP/1.1", "K7M2P4"));
        assertFalse(PairUrl.authorized("POST /pair HTTP/1.1", "K7M2P4"));
        assertFalse(PairUrl.authorized("POST /pair?c= HTTP/1.1", "K7M2P4"));
        assertFalse(PairUrl.authorized("GET /pair?c=K7M2P4 HTTP/1.1", "K7M2P4"));
        assertFalse(PairUrl.authorized("POST /other?c=K7M2P4 HTTP/1.1", "K7M2P4"));
        assertFalse(PairUrl.authorized("POST /pair?c=K7M2P4 HTTP/1.1", ""));
        assertFalse(PairUrl.authorized("", "K7M2P4"));
        assertFalse(PairUrl.authorized(null, "K7M2P4"));
    }
}
