/*
 * Android client, unit tests (JVM, no device): Ranges, the sorted address
 * range set behind route subtraction and the per-packet block check. Random
 * prefix lists are checked address by address against a plain scan over the
 * Cidr list, which is slow but obviously right.
 */
package app.loft.android;

import org.junit.Test;

import java.util.ArrayList;
import java.util.List;
import java.util.Random;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

/** Tests for Ranges and the Cidr / RoutePlan code built on it. */
public class RangesTest {
    /** Merging: overlapping and adjacent prefixes collapse into the fewest prefixes. */
    @Test
    public void mergesAdjacentAndOverlapping() {
        Ranges r = Ranges.of(Cidr.of("10.0.0.0/25", "10.0.0.128/25", "10.0.0.7/32", "10.0.1.0/24"), false);
        assertEquals(1, r.size());
        List<Cidr> c = r.toCidrs();
        assertEquals(1, c.size());
        assertEquals("10.0.0.0/23", c.get(0).display());
    }

    /** The whole address space survives the 128-bit edge cases (all-ones end, zero start). */
    @Test
    public void fullSpaceBothFamilies() {
        assertEquals("0.0.0.0/0", Ranges.of(Cidr.of("0.0.0.0/0"), false).toCidrs().get(0).display());
        Cidr all6 = Ranges.of(Cidr.of("::/0"), true).toCidrs().get(0);
        assertEquals(0, all6.bits);
        assertTrue(all6.v6);
        Ranges top = Ranges.of(Cidr.of("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128", "::/128"), true);
        assertEquals(2, top.size());
    }

    /** Cutting a host out of the default route gives the classic 32 pieces. */
    @Test
    public void subtractHostMatchesOldAlgorithm() {
        List<Cidr> rest = Cidr.subtractAll(Cidr.of("0.0.0.0/0"), Cidr.of("1.2.3.4/32"));
        assertEquals(32, rest.size());
    }

    /** Random IPv4 lists: subtraction and lookup agree with a brute-force scan. */
    @Test
    public void randomIPv4AgainstBruteForce() {
        Random rnd = new Random(42);
        for (int round = 0; round < 200; round++) {
            List<Cidr> set = randomV4(rnd, 1 + rnd.nextInt(6), 8);
            List<Cidr> cut = randomV4(rnd, rnd.nextInt(40), 16);
            List<Cidr> got = Cidr.subtractAll(set, cut);
            Ranges drop = Ranges.of(cut, false);
            for (int i = 0; i < 400; i++) {
                byte[] a = probeV4(rnd, set, cut);
                boolean want = any(set, a, false) && !any(cut, a, false);
                assertEquals(Cidr.parse(ip4(a)).display(), want, any(got, a, false));
                assertEquals(any(cut, a, false), drop.contains(a, 0));
            }
        }
    }

    /** Random IPv6 lists: the high and low 64-bit halves both carry bits. */
    @Test
    public void randomIPv6AgainstBruteForce() {
        Random rnd = new Random(7);
        for (int round = 0; round < 100; round++) {
            List<Cidr> set = randomV6(rnd, 1 + rnd.nextInt(4), 4, 70);
            List<Cidr> cut = randomV6(rnd, rnd.nextInt(30), 20, 128);
            List<Cidr> got = Cidr.subtractAll(set, cut);
            Ranges drop = Ranges.of(cut, true);
            for (int i = 0; i < 300; i++) {
                byte[] a = probeV6(rnd, set, cut);
                boolean want = any(set, a, true) && !any(cut, a, true);
                assertEquals(want, any(got, a, true));
                assertEquals(any(cut, a, true), drop.contains(a, 0));
            }
        }
    }

    /** RoutePlan reads the destination straight out of the packet buffer, at the right offset. */
    @Test
    public void destDroppedReadsPacketInPlace() {
        RoutePlan p = new RoutePlan();
        p.drop.addAll(Cidr.of("6.6.6.0/24", "2001:db8::/32"));
        p.compileDrop();
        byte[] buf = new byte[2048];
        buf[0] = 0x45;
        buf[16] = 6;
        buf[17] = 6;
        buf[18] = 6;
        buf[19] = 9;
        assertTrue(p.destDropped(buf, 20));
        buf[18] = 7;
        assertFalse(p.destDropped(buf, 20));
        byte[] v6 = new byte[40];
        v6[0] = 0x60;
        v6[24] = 0x20;
        v6[25] = 0x01;
        v6[26] = 0x0d;
        v6[27] = (byte) 0xb8;
        assertTrue(p.destDropped(v6, 40));
        assertFalse(p.destDropped(v6, 39));
        assertFalse(new RoutePlan().destDropped(buf, 20));
    }

    // ---- helpers ----

    /** Random IPv4 prefixes with lengths from minBits to 32, packed into 10.0.0.0/8 so they collide. */
    private static List<Cidr> randomV4(Random rnd, int n, int minBits) {
        List<Cidr> out = new ArrayList<>();
        for (int i = 0; i < n; i++) {
            int bits = minBits + rnd.nextInt(33 - minBits);
            byte[] a = {10, (byte) rnd.nextInt(4), (byte) rnd.nextInt(256), (byte) rnd.nextInt(256)};
            if (bits < 8) {
                a[0] = (byte) rnd.nextInt(256);
            }
            out.add(Cidr.parse(ip4(a) + "/" + bits).canonical());
        }
        return out;
    }

    /** Random IPv6 prefixes inside 2001:db8::/32, with bits set on both sides of bit 64. */
    private static List<Cidr> randomV6(Random rnd, int n, int minBits, int maxBits) {
        List<Cidr> out = new ArrayList<>();
        for (int i = 0; i < n; i++) {
            int bits = Math.max(minBits, 32) + rnd.nextInt(maxBits - Math.max(minBits, 32) + 1);
            byte[] a = new byte[16];
            a[0] = 0x20;
            a[1] = 0x01;
            a[2] = 0x0d;
            a[3] = (byte) 0xb8;
            a[4] = (byte) rnd.nextInt(2);
            a[7] = (byte) rnd.nextInt(4);
            a[8] = (byte) (rnd.nextInt(2) << 7);
            a[15] = (byte) rnd.nextInt(256);
            out.add(new Cidr(a, bits, true).canonical());
        }
        return out;
    }

    /** An address near the interesting edges: a prefix's first, last or a random address. */
    private static byte[] probeV4(Random rnd, List<Cidr> set, List<Cidr> cut) {
        List<Cidr> pool = rnd.nextBoolean() && !cut.isEmpty() ? cut : set;
        Cidr c = pool.get(rnd.nextInt(pool.size()));
        return edge(rnd, c, 32);
    }

    /** IPv6 version of probeV4. */
    private static byte[] probeV6(Random rnd, List<Cidr> set, List<Cidr> cut) {
        List<Cidr> pool = rnd.nextBoolean() && !cut.isEmpty() ? cut : set;
        Cidr c = pool.get(rnd.nextInt(pool.size()));
        return edge(rnd, c, 128);
    }

    /** First address, last address, one past either end, or a random address of the prefix. */
    private static byte[] edge(Random rnd, Cidr c, int width) {
        byte[] a = c.addr.clone();
        int pick = rnd.nextInt(5);
        for (int bit = c.bits; bit < width; bit++) {
            boolean one = pick == 1 || pick == 3 || (pick == 4 && rnd.nextBoolean());
            int idx = bit / 8;
            int m = 0x80 >> (bit % 8);
            a[idx] = (byte) (one ? (a[idx] | m) : (a[idx] & ~m));
        }
        if (pick == 2) {
            addOne(a, -1);
        } else if (pick == 3) {
            addOne(a, 1);
        }
        return a;
    }

    /** Adds +1 or -1 to a big-endian address in place (wrapping). */
    private static void addOne(byte[] a, int d) {
        for (int i = a.length - 1; i >= 0; i--) {
            int v = (a[i] & 0xff) + d;
            a[i] = (byte) v;
            if (v >= 0 && v <= 255) {
                return;
            }
        }
    }

    /** Brute force: does any prefix of the list hold the address? */
    private static boolean any(List<Cidr> list, byte[] a, boolean v6) {
        Cidr host = new Cidr(a, v6 ? 128 : 32, v6);
        for (Cidr c : list) {
            if (c.contains(host)) {
                return true;
            }
        }
        return false;
    }

    /** Dotted form of a 4-byte address. */
    private static String ip4(byte[] a) {
        return (a[0] & 0xff) + "." + (a[1] & 0xff) + "." + (a[2] & 0xff) + "." + (a[3] & 0xff);
    }
}
