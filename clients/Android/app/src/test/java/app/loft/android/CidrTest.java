package app.loft.android;

import org.junit.Test;

import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertTrue;

public final class CidrTest {
    @Test
    public void parseV4() {
        Cidr c = Cidr.parse("10.8.0.2/24");
        assertNotNull(c);
        assertEquals("10.8.0.2", c.address());
        assertEquals(24, c.bits);
        assertFalse(c.v6);
    }

    @Test
    public void parseV6() {
        Cidr c = Cidr.parse("2001:db8::1/64");
        assertNotNull(c);
        assertTrue(c.v6);
        assertEquals(64, c.bits);
    }

    @Test
    public void subtractHostFromDefault() {
        Cidr all = Cidr.parse("0.0.0.0/0");
        Cidr one = Cidr.parse("1.2.3.4/32");
        List<Cidr> rest = Cidr.subtract(all, one);
        assertEquals(32, rest.size());
        boolean covered = false;
        for (Cidr r : rest) {
            assertFalse(r.contains(one));
            covered = true;
        }
        assertTrue(covered);
    }

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
            assertFalse(c.contains(Cidr.parse("192.168.1.1/32")));
        }
        assertTrue(found);
    }
}
