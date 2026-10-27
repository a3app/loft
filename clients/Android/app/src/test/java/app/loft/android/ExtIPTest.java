/*
 * Android client unit tests (JVM, no device) for ExtIP.parse: reading the
 * public IP from plain-text and JSON echo-service replies, and rejecting junk.
 */
package app.loft.android;

import org.junit.Test;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

public final class ExtIPTest {
    @Test
    public void parsePlain() {
        assertEquals("203.0.113.9", ExtIP.parse("203.0.113.9\n"));
    }

    @Test
    public void parseJson() {
        assertEquals("198.51.100.4", ExtIP.parse("{\"ip\":\"198.51.100.4\"}"));
        assertEquals("198.51.100.5", ExtIP.parse("{\"query\":\"198.51.100.5\"}"));
    }

    @Test
    public void parseRejectsJunk() {
        assertTrue(ExtIP.parse("not an address").isEmpty());
        assertTrue(ExtIP.parse("").isEmpty());
    }
}
