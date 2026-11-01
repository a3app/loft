/*
 * Android client unit tests (JVM, no device) for Cidr: IPv4/IPv6 prefix
 * parsing and canonical form, rejecting hostnames, and the prefix
 * subtraction that RoutePlan uses for LAN bypass on older Android versions.
 */
package app.loft.android;

import org.junit.Test;

import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

/** Tests for parsing, normalising and subtracting IP prefixes. */
public final class CidrTest {
    /** An IPv4 prefix keeps its host address and length as typed. */
    @Test
    public void parseV4() {
        Cidr c = Cidr.parse("10.8.0.2/24");
        assertNotNull(c);
        assertEquals("10.8.0.2", c.address());
        assertEquals(24, c.bits);
        assertFalse(c.v6);
    }

    /** canonical() clears the host bits, so 10.8.0.2/24 becomes 10.8.0.0/24. */
    @Test
    public void canonicalZerosHostBits() {
        Cidr c = Cidr.parse("10.8.0.2/24").canonical();
        assertEquals("10.8.0.0", c.address());
        assertEquals(24, c.bits);
        assertEquals("10.8.0.0/24", c.display());
    }

    /** IPv6 prefixes parse and are flagged as v6. */
    @Test
    public void parseV6() {
        Cidr c = Cidr.parse("2001:db8::1/64");
        assertNotNull(c);
        assertTrue(c.v6);
        assertEquals(64, c.bits);
    }

    /** Hostnames and URLs are not prefixes and parse to null. */
    @Test
    public void parseDropsHostnames() {
        assertNull(Cidr.parse("youtube.com"));
        assertNull(Cidr.parse("youtube.com/32"));
        assertNull(Cidr.parse("http://example.com"));
    }

    /** Removing one /32 from 0.0.0.0/0 leaves 32 pieces, none of which contain that address. */
    @Test
    public void subtractHostFromDefault() {
        Cidr all = Cidr.parse("0.0.0.0/0");
        Cidr one = Cidr.parse("1.2.3.4/32");
        List<Cidr> rest = Cidr.subtract(all, one);
        assertEquals(32, rest.size());
        boolean covered = false;
        for (Cidr r : rest) {
            assertFalse(r.display(), r.contains(one));
            covered = true;
        }
        assertTrue(covered);
    }

    /**
     * After cutting the private ranges out of the default route, the overlay subnet (the tunnel's
     * own 10.8.0.0/24) can be added back, while home LAN addresses stay outside.
     */
    @Test
    public void lanBypassKeepsOverlay() {
        List<Cidr> v4 = Cidr.of("0.0.0.0/0");
        v4 = Cidr.subtractAll(v4, Cidr.rfc1918());
        Cidr overlay = Cidr.parse("10.8.0.0/24");
        v4.add(overlay);
        boolean found = false;
        for (Cidr c : v4) {
            if (c.contains(Cidr.parse("10.8.0.2/32"))) {
                found = true;
            }
            assertFalse(c.display(), c.contains(Cidr.parse("192.168.1.1/32")));
        }
        assertTrue(found);
    }
}
