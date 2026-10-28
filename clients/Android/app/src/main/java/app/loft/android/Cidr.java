/*
 * Cidr: a small IPv4/IPv6 prefix type (address + prefix length) with the
 * set arithmetic needed to build VpnService route lists. RoutePlan uses it to
 * compute which ranges go into the tunnel; Bat uses it to read and write
 * Windows-style "route add ... MASK ..." lines. It also hosts the shared
 * byte-rate formatter used by the traffic UI. Shared code (phone and TV).
 */
package app.loft.android;

import java.net.InetAddress;
import java.net.UnknownHostException;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

/**
 * IPv4/IPv6 prefixes for VpnService positive routes.
 * LAN bypass is 0.0.0.0/0 minus RFC1918 (overlay CIDR added back).
 *
 * <p>A CIDR prefix such as 10.0.0.0/8 names a block of addresses: the first
 * {@code bits} bits are fixed, the rest vary. Instances are immutable (the
 * address array is copied in and never changed), so they can be shared
 * between the VPN service and screens without locking.
 */
final class Cidr {
    /** Raw address bytes: 4 for IPv4, 16 for IPv6. Host bits may be non-zero (see canonical()). */
    final byte[] addr;
    /** Prefix length: how many leading bits form the network part. */
    final int bits;
    /** True for an IPv6 prefix. */
    final boolean v6;

    /** Creates a prefix; the address array is copied so callers can reuse theirs. */
    Cidr(byte[] addr, int bits, boolean v6) {
        this.addr = addr.clone();
        this.bits = bits;
        this.v6 = v6;
    }

    /**
     * Parses "addr" or "addr/bits". A bare address becomes a single-host prefix
     * (/32 or /128). Returns null for empty input, host names, bad numbers or a
     * prefix length too large for the family.
     */
    static Cidr parse(String s) {
        if (s == null) {
            return null;
        }
        s = s.trim();
        if (s.isEmpty()) {
            return null;
        }
        int slash = s.indexOf('/');
        String ip = slash < 0 ? s : s.substring(0, slash);
        int bits = -1;
        if (slash >= 0) {
            try {
                bits = Integer.parseInt(s.substring(slash + 1).trim());
            } catch (NumberFormatException e) {
                return null;
            }
        }
        // Numeric only. getByName does DNS and, after a VPN teardown, can
        // block the UI thread inside netd (ANR / crash on the lists screen).
        InetAddress a = numeric(ip);
        if (a == null) {
            return null;
        }
        byte[] raw = a.getAddress();
        boolean v6 = raw.length == 16;
        int max = v6 ? 128 : 32;
        if (bits < 0) {
            bits = max;
        }
        if (bits > max) {
            return null;
        }
        return new Cidr(raw, bits, v6);
    }

    /** Single-host prefix (/32 or /128) for {@code a}; null when {@code a} is null. */
    static Cidr host(InetAddress a) {
        if (a == null) {
            return null;
        }
        byte[] raw = a.getAddress();
        boolean v6 = raw.length == 16;
        return new Cidr(raw, v6 ? 128 : 32, v6);
    }

    /** Prefix length (same as bits); the name used by the route-file code. */
    int familyPrefix() {
        return bits;
    }

    /**
     * Address part as text, without the prefix length. getByAddress only
     * wraps the bytes and never does a DNS lookup.
     */
    String address() {
        try {
            return InetAddress.getByAddress(addr).getHostAddress();
        } catch (UnknownHostException e) {
            return "";
        }
    }

    /** "address/bits" form, as shown in the UI and logs. */
    String display() {
        return address() + "/" + bits;
    }

    /** Network form: host bits zeroed. addRoute and excludeRoute need this. */
    Cidr canonical() {
        byte[] n = addr.clone();
        zeroAfter(n, bits);
        return new Cidr(n, bits, v6);
    }

    /** True when every address of {@code o} lies inside this prefix (same family only). */
    boolean contains(Cidr o) {
        if (o == null || o.v6 != v6 || o.bits < bits) {
            return false;
        }
        return prefixEqual(addr, o.addr, bits);
    }

    /** True when the two prefixes share any address (one contains the other). */
    boolean overlaps(Cidr o) {
        if (o == null || o.v6 != v6) {
            return false;
        }
        return prefixEqual(addr, o.addr, Math.min(bits, o.bits));
    }

    /** Parses each string and returns the valid ones; bad entries are silently skipped. */
    static List<Cidr> of(String... cidrs) {
        List<Cidr> out = new ArrayList<>();
        for (String s : cidrs) {
            Cidr c = parse(s);
            if (c != null) {
                out.add(c);
            }
        }
        return out;
    }

    /** IPv4 ranges that stay on the LAN: private, link-local and loopback. */
    static List<Cidr> rfc1918() {
        return of(
                "10.0.0.0/8",
                "172.16.0.0/12",
                "192.168.0.0/16",
                "169.254.0.0/16",
                "127.0.0.0/8");
    }

    /** IPv6 ranges that stay on the LAN: unique local, link-local and multicast. */
    static List<Cidr> rfc4193() {
        return of("fc00::/7", "fe80::/10", "ff00::/8");
    }

    /**
     * Removes every prefix in {@code cut} from every prefix in {@code set} and
     * returns the fewest prefixes that cover what is left. Works per family on
     * sorted ranges (see Ranges), so the cost grows with the list sizes added
     * together, not multiplied: thousands of bypass prefixes on Android 12 and
     * older stay fast. Overlapping or adjacent prefixes in {@code set} come
     * back merged, which also shortens the route table handed to Android.
     */
    static List<Cidr> subtractAll(List<Cidr> set, List<Cidr> cut) {
        List<Cidr> out = new ArrayList<>();
        out.addAll(Ranges.of(set, false).minus(Ranges.of(cut, false)).toCidrs());
        out.addAll(Ranges.of(set, true).minus(Ranges.of(cut, true)).toCidrs());
        return out;
    }

    /**
     * Shrinks a prefix list to at most {@code max} prefixes while covering at
     * least every address it covered before. Exact (only merged) when it
     * already fits; otherwise prefixes are widened to /L blocks, choosing the
     * longest L (the least widening) that fits. IPv4 and IPv6 share the
     * budget in proportion to their sizes.
     *
     * <p>Why: Android sends the VPN's whole route table to every app that
     * watches the network, in one IPC message limited to about 1 MB (roughly
     * 7,000 routes). Past that, the system cannot deliver it, and apps that
     * ask for it crash. See RoutePlan.MAX_ROUTES.
     */
    static List<Cidr> fit(List<Cidr> list, int max) {
        List<Cidr> v4 = new ArrayList<>();
        List<Cidr> v6 = new ArrayList<>();
        for (Cidr c : list) {
            if (c != null) {
                (c.v6 ? v6 : v4).add(c);
            }
        }
        int n = v4.size() + v6.size();
        int max4 = n == 0 ? max : Math.max(1, (int) ((long) max * v4.size() / n));
        int max6 = Math.max(1, max - max4);
        List<Cidr> out = new ArrayList<>(fitFamily(v4, false, max4));
        out.addAll(fitFamily(v6, true, max6));
        return out;
    }

    /** fit() for one family: the longest block length L whose widened list fits in {@code max}. */
    private static List<Cidr> fitFamily(List<Cidr> list, boolean v6, int max) {
        if (list.isEmpty()) {
            return list;
        }
        Ranges r = Ranges.of(list, v6);
        List<Cidr> exact = r.toCidrs();
        if (exact.size() <= max) {
            return exact;
        }
        // Fewer bits means wider blocks and fewer prefixes, so the size only
        // shrinks as L goes down: a binary search finds the longest L that fits.
        int lo = 0;
        int hi = v6 ? 128 : 32;
        List<Cidr> best = r.coarsen(0).toCidrs();
        while (lo <= hi) {
            int mid = (lo + hi) >>> 1;
            List<Cidr> c = r.coarsen(mid).toCidrs();
            if (c.size() <= max) {
                best = c;
                lo = mid + 1;
            } else {
                hi = mid - 1;
            }
        }
        return best;
    }

    /**
     * Returns {@code a} minus {@code b} as a list of prefixes. When b lies inside
     * a, walk the bits from a's length down to b's: at each step the half that
     * does not contain b is kept as its own prefix. The result covers a exactly
     * except b, with at most (b.bits - a.bits) prefixes.
     */
    static List<Cidr> subtract(Cidr a, Cidr b) {
        List<Cidr> out = new ArrayList<>();
        if (a == null) {
            return out;
        }
        // No overlap: a is unchanged. b covers a: nothing is left.
        if (b == null || a.v6 != b.v6 || !a.overlaps(b)) {
            out.add(a);
            return out;
        }
        if (b.contains(a) && b.bits <= a.bits) {
            return out;
        }
        if (!a.contains(b)) {
            out.add(a);
            return out;
        }
        // Walk down towards b: at each bit keep the sibling half (the one b is
        // not in) and continue inside the half that holds b.
        byte[] cur = a.addr.clone();
        int max = a.v6 ? 128 : 32;
        for (int bit = a.bits; bit < b.bits && bit < max; bit++) {
            int v = bitValue(b.addr, bit);
            byte[] n = cur.clone();
            setBit(n, bit, 1 - v);
            zeroAfter(n, bit + 1);
            out.add(new Cidr(n, bit + 1, a.v6));
            setBit(cur, bit, v);
        }
        return out;
    }

    /** Sets bit number {@code bit} (0 = most significant bit of byte 0) to {@code v}. */
    private static void setBit(byte[] addr, int bit, int v) {
        int i = bit / 8;
        int m = 1 << (7 - (bit % 8));
        if (v != 0) {
            addr[i] = (byte) (addr[i] | m);
        } else {
            addr[i] = (byte) (addr[i] & ~m);
        }
    }

    /** Reads bit number {@code bit}, counted from the most significant bit. */
    private static int bitValue(byte[] addr, int bit) {
        int i = bit / 8;
        int m = 1 << (7 - (bit % 8));
        return (addr[i] & m) != 0 ? 1 : 0;
    }

    /** Clears every bit from position {@code bit} to the end (zeroes the host part). */
    private static void zeroAfter(byte[] addr, int bit) {
        for (int b = bit; b < addr.length * 8; b++) {
            setBit(addr, b, 0);
        }
    }

    /** True when {@code a} and {@code b} agree on their first {@code bits} bits. */
    private static boolean prefixEqual(byte[] a, byte[] b, int bits) {
        if (bits <= 0) {
            return true;
        }
        int full = bits / 8;
        for (int i = 0; i < full; i++) {
            if (a[i] != b[i]) {
                return false;
            }
        }
        int rem = bits % 8;
        if (rem == 0) {
            return true;
        }
        int m = (0xff << (8 - rem)) & 0xff;
        return (a[full] & m) == (b[full] & m);
    }

    /** Same as display(), so a Cidr prints readably in logs and string joins. */
    @Override
    public String toString() {
        return display();
    }

    /**
     * Parses a literal IPv4 or IPv6 address without DNS. Returns null for host
     * names, so a stray name never triggers a lookup.
     */
    static InetAddress numeric(String s) {
        if (s == null) {
            return null;
        }
        s = s.trim();
        if (s.isEmpty()) {
            return null;
        }
        try {
            // IPv6: accept only hex digits, ':' and '.', so getByName gets a
            // literal it can parse without a DNS lookup.
            if (s.indexOf(':') >= 0) {
                for (int i = 0; i < s.length(); i++) {
                    char c = s.charAt(i);
                    if (c != ':' && c != '.'
                            && (c < '0' || c > '9')
                            && (c < 'a' || c > 'f')
                            && (c < 'A' || c > 'F')) {
                        return null;
                    }
                }
                return InetAddress.getByName(s);
            }
            // IPv4: exactly four decimal parts 0..255, built by hand.
            String[] p = s.split("\\.");
            if (p.length != 4) {
                return null;
            }
            byte[] b = new byte[4];
            for (int i = 0; i < 4; i++) {
                int n = Integer.parseInt(p[i]);
                if (n < 0 || n > 255) {
                    return null;
                }
                b[i] = (byte) n;
            }
            return InetAddress.getByAddress(b);
        } catch (Exception e) {
            return null;
        }
    }

    /**
     * Builds a prefix from an address and a dotted mask (255.255.255.0 → /24),
     * as found in Windows "route add" lines. The length is the count of set
     * bits, so a non-contiguous mask is not rejected. A bad mask falls back to
     * parsing ip alone (a single host).
     */
    static Cidr fromIpMask(String ip, String mask) {
        InetAddress m = numeric(mask);
        if (m == null) {
            return parse(ip);
        }
        byte[] b = m.getAddress();
        int bits = 0;
        for (byte x : b) {
            bits += Integer.bitCount(x & 0xff);
        }
        return parse(ip + "/" + bits);
    }

    /** IPv4 prefix length as a dotted netmask (/24 → 255.255.255.0), for route-batch files. */
    static String dottedMask(int bits) {
        int n = bits <= 0 ? 0 : bits >= 32 ? 0xFFFFFFFF : (0xFFFFFFFF << (32 - bits));
        return ((n >>> 24) & 255) + "." + ((n >>> 16) & 255) + "." + ((n >>> 8) & 255) + "." + (n & 255);
    }

    /**
     * Formats a byte rate as B/s or bit/s with 1000-based units and 3
     * significant digits ("1.23 MB/s", "45.6 Kbit/s"). Locale.US keeps the
     * decimal point a dot whatever the phone's language. Called by the phone
     * and TV home screens on every redraw.
     */
    static String fmt(long n, boolean bits) {
        if (n < 0) {
            n = 0;
        }
        if (bits) {
            n *= 8;
        }
        double v = n;
        String[] u = bits
                ? new String[]{"bit/s", "Kbit/s", "Mbit/s", "Gbit/s"}
                : new String[]{"B/s", "KB/s", "MB/s", "GB/s"};
        int i = 0;
        while (v >= 1000 && i < u.length - 1) {
            v /= 1000;
            i++;
        }
        if (v >= 100 || i == 0) {
            return String.format(Locale.US, "%.0f %s", v, u[i]);
        }
        if (v >= 10) {
            return String.format(Locale.US, "%.1f %s", v, u[i]);
        }
        return String.format(Locale.US, "%.2f %s", v, u[i]);
    }
}
