package app.loft.android;

import java.net.InetAddress;
import java.net.UnknownHostException;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

/**
 * IPv4/IPv6 prefixes for VpnService positive routes.
 * LAN bypass is 0.0.0.0/0 minus RFC1918 (overlay CIDR added back).
 */
final class Cidr {
    final byte[] addr;
    final int bits;
    final boolean v6;

    Cidr(byte[] addr, int bits, boolean v6) {
        this.addr = addr.clone();
        this.bits = bits;
        this.v6 = v6;
    }

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
        InetAddress a;
        try {
            a = InetAddress.getByName(ip);
        } catch (UnknownHostException e) {
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

    static Cidr host(InetAddress a) {
        if (a == null) {
            return null;
        }
        byte[] raw = a.getAddress();
        boolean v6 = raw.length == 16;
        return new Cidr(raw, v6 ? 128 : 32, v6);
    }

    // Prefix length (same as bits).
    int familyPrefix() {
        return bits;
    }

    String address() {
        try {
            return InetAddress.getByAddress(addr).getHostAddress();
        } catch (UnknownHostException e) {
            return "";
        }
    }

    String display() {
        return address() + "/" + bits;
    }

    /** Network form: host bits zeroed. addRoute and excludeRoute need this. */
    Cidr canonical() {
        byte[] n = addr.clone();
        zeroAfter(n, bits);
        return new Cidr(n, bits, v6);
    }

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

    /** Removes every prefix in {@code cut} from every prefix in {@code set}. */
    static List<Cidr> subtractAll(List<Cidr> set, List<Cidr> cut) {
        List<Cidr> cur = new ArrayList<>(set);
        for (Cidr c : cut) {
            List<Cidr> next = new ArrayList<>();
            for (Cidr x : cur) {
                next.addAll(subtract(x, c));
            }
            cur = next;
        }
        return cur;
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

    private static void setBit(byte[] addr, int bit, int v) {
        int i = bit / 8;
        int m = 1 << (7 - (bit % 8));
        if (v != 0) {
            addr[i] = (byte) (addr[i] | m);
        } else {
            addr[i] = (byte) (addr[i] & ~m);
        }
    }

    private static int bitValue(byte[] addr, int bit) {
        int i = bit / 8;
        int m = 1 << (7 - (bit % 8));
        return (addr[i] & m) != 0 ? 1 : 0;
    }

    private static void zeroAfter(byte[] addr, int bit) {
        for (int b = bit; b < addr.length * 8; b++) {
            setBit(addr, b, 0);
        }
    }

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
            if (s.indexOf(':') >= 0) {
                return InetAddress.getByName(s);
            }
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

    /** Builds a prefix from an address and a dotted mask (255.255.255.0 → /24). */
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

    static String dottedMask(int bits) {
        int n = bits <= 0 ? 0 : bits >= 32 ? 0xFFFFFFFF : (0xFFFFFFFF << (32 - bits));
        return ((n >>> 24) & 255) + "." + ((n >>> 16) & 255) + "." + ((n >>> 8) & 255) + "." + (n & 255);
    }

    /** Formats a byte rate as B/s or bit/s with 1000-based units and 3 significant digits. */
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
