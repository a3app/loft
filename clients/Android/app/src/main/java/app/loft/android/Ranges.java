/*
 * Android client, shared code (phone and TV builds): a sorted set of IP
 * address ranges for one family (IPv4 or IPv6). RoutePlan uses it for two
 * hot jobs that a plain list of Cidr prefixes does badly:
 *
 *  - Subtracting thousands of bypass prefixes from the routes on Android 12
 *    and older (no excludeRoute there). Cutting prefixes one by one costs
 *    "routes x cuts" work and splits the result into ever more pieces; a
 *    single sweep over two sorted range lists is linear.
 *  - Checking every packet the TUN reader sees against the block list. A
 *    binary search over sorted ranges is about 12 compares for 4,000 entries,
 *    with no memory allocated per packet, where a linear scan was 4,000
 *    compares and two new arrays per packet.
 */
package app.loft.android;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

/**
 * Immutable set of address ranges of one family, sorted and merged so no two
 * ranges overlap or touch.
 *
 * <p>Every address is held as an unsigned 128-bit number split into two
 * longs: {@code hi} (top 64 bits) and {@code lo} (bottom 64 bits). An IPv4
 * address uses only the bottom 32 bits of {@code lo}, so both families share
 * the same code. Java has no unsigned long, so all comparisons go through
 * {@link Long#compareUnsigned}.
 *
 * <p>Range i covers [start, end] inclusive: start = (sHi[i], sLo[i]),
 * end = (eHi[i], eLo[i]).
 */
final class Ranges {
    /** True for an IPv6 set; decides the address width (128 or 32 bits). */
    final boolean v6;
    /** Range starts and ends, split into high and low 64-bit halves (see class comment). */
    private final long[] sHi;
    private final long[] sLo;
    private final long[] eHi;
    private final long[] eLo;

    /** Wraps already sorted, merged arrays; use {@link #of} to build one. */
    private Ranges(boolean v6, long[] sHi, long[] sLo, long[] eHi, long[] eLo) {
        this.v6 = v6;
        this.sHi = sHi;
        this.sLo = sLo;
        this.eHi = eHi;
        this.eLo = eLo;
    }

    /** Number of separate ranges after merging. */
    int size() {
        return sLo.length;
    }

    /**
     * Builds the set from the prefixes of one family; prefixes of the other
     * family and nulls are ignored. Overlapping or adjacent prefixes merge
     * into one range, so the result is as short as it can be.
     */
    static Ranges of(List<Cidr> list, boolean v6) {
        // 1. Turn each prefix into [first address, last address].
        List<long[]> rs = new ArrayList<>(list.size());
        for (Cidr c : list) {
            if (c == null || c.v6 != v6) {
                continue;
            }
            long hi = v6 ? word(c.addr, 0) : 0;
            long lo = v6 ? word(c.addr, 8) : (word4(c.addr) & 0xffffffffL);
            int host = (v6 ? 128 : 32) - c.bits; // bits that vary inside the prefix
            long mHi = maskHi(host);
            long mLo = maskLo(host);
            // First address: host bits cleared. Last address: host bits set.
            rs.add(new long[]{hi & ~mHi, lo & ~mLo, hi | mHi, lo | mLo});
        }
        // 2. Sort by start and merge ranges that overlap or touch.
        rs.sort((a, b) -> cmp(a[0], a[1], b[0], b[1]));
        List<long[]> out = new ArrayList<>(rs.size());
        for (long[] r : rs) {
            long[] last = out.isEmpty() ? null : out.get(out.size() - 1);
            if (last != null && touches(last[2], last[3], r[0], r[1])) {
                if (cmp(r[2], r[3], last[2], last[3]) > 0) {
                    last[2] = r[2];
                    last[3] = r[3];
                }
            } else {
                out.add(r.clone());
            }
        }
        return fromList(v6, out);
    }

    /**
     * Addresses in this set that are not in {@code cut}. Both sets are sorted,
     * so one pass over each is enough: for every range here, walk the cut
     * ranges that overlap it and keep the gaps between them.
     */
    Ranges minus(Ranges cut) {
        if (cut == null || cut.v6 != v6 || cut.size() == 0 || size() == 0) {
            return this;
        }
        List<long[]> out = new ArrayList<>();
        int j = 0;
        for (int i = 0; i < size(); i++) {
            // Current piece of range i still to be kept: [curHi:curLo, eHi:eLo].
            long curHi = sHi[i];
            long curLo = sLo[i];
            boolean alive = true;
            // Skip cuts that end before this range starts; they cannot touch
            // this range or any later one.
            while (j < cut.size() && cmp(cut.eHi[j], cut.eLo[j], curHi, curLo) < 0) {
                j++;
            }
            int k = j;
            while (alive && k < cut.size() && cmp(cut.sHi[k], cut.sLo[k], eHi[i], eLo[i]) <= 0) {
                // The part before this cut survives.
                if (cmp(cut.sHi[k], cut.sLo[k], curHi, curLo) > 0) {
                    long[] before = dec(cut.sHi[k], cut.sLo[k]);
                    out.add(new long[]{curHi, curLo, before[0], before[1]});
                }
                // Continue after the cut, unless it runs past the range end.
                if (cmp(cut.eHi[k], cut.eLo[k], eHi[i], eLo[i]) >= 0) {
                    alive = false;
                } else {
                    long[] after = inc(cut.eHi[k], cut.eLo[k]);
                    curHi = after[0];
                    curLo = after[1];
                    k++;
                }
            }
            if (alive) {
                out.add(new long[]{curHi, curLo, eHi[i], eLo[i]});
            }
        }
        return fromList(v6, out);
    }

    /**
     * True when the address at {@code buf[off..]} (4 bytes for IPv4, 16 for
     * IPv6) lies in the set. Binary search, no allocation: safe to call for
     * every packet on the TUN reader thread.
     */
    boolean contains(byte[] buf, int off) {
        int n = size();
        if (n == 0) {
            return false;
        }
        long hi = v6 ? word(buf, off) : 0;
        long lo = v6 ? word(buf, off + 8) : (word4(buf, off) & 0xffffffffL);
        // Find the last range whose start is <= the address...
        int a = 0;
        int b = n - 1;
        int found = -1;
        while (a <= b) {
            int mid = (a + b) >>> 1;
            if (cmp(sHi[mid], sLo[mid], hi, lo) <= 0) {
                found = mid;
                a = mid + 1;
            } else {
                b = mid - 1;
            }
        }
        // ...and check that the address is not past its end.
        return found >= 0 && cmp(hi, lo, eHi[found], eLo[found]) <= 0;
    }

    /**
     * Turns the ranges back into the fewest CIDR prefixes that cover exactly
     * the same addresses. Each step takes the largest aligned block that
     * starts at the current address and does not run past the range end.
     */
    List<Cidr> toCidrs() {
        List<Cidr> out = new ArrayList<>();
        int width = v6 ? 128 : 32;
        for (int i = 0; i < size(); i++) {
            long hi = sHi[i];
            long lo = sLo[i];
            while (true) {
                // Largest block allowed by alignment: as many host bits as the
                // start address has trailing zero bits.
                int host = Math.min(width, trailingZeros(hi, lo));
                // Shrink it until the block's last address fits in the range.
                while (host > 0 && cmp(hi | maskHi(host), lo | maskLo(host), eHi[i], eLo[i]) > 0) {
                    host--;
                }
                out.add(new Cidr(bytes(hi, lo), width - host, v6));
                long lastHi = hi | maskHi(host);
                long lastLo = lo | maskLo(host);
                if (cmp(lastHi, lastLo, eHi[i], eLo[i]) >= 0) {
                    break;
                }
                long[] next = inc(lastHi, lastLo);
                hi = next[0];
                lo = next[1];
            }
        }
        return out;
    }

    // ---- 128-bit helpers -------------------------------------------------

    /** Copies a list of {startHi, startLo, endHi, endLo} rows into the four arrays. */
    private static Ranges fromList(boolean v6, List<long[]> rows) {
        int n = rows.size();
        long[] sh = new long[n];
        long[] sl = new long[n];
        long[] eh = new long[n];
        long[] el = new long[n];
        for (int i = 0; i < n; i++) {
            long[] r = rows.get(i);
            sh[i] = r[0];
            sl[i] = r[1];
            eh[i] = r[2];
            el[i] = r[3];
        }
        return new Ranges(v6, sh, sl, eh, el);
    }

    /** Unsigned 128-bit compare: negative, zero or positive like Comparator. */
    static int cmp(long aHi, long aLo, long bHi, long bLo) {
        int c = Long.compareUnsigned(aHi, bHi);
        return c != 0 ? c : Long.compareUnsigned(aLo, bLo);
    }

    /**
     * True when the range ending at aEnd and the one starting at bStart
     * overlap or are adjacent (aEnd + 1 >= bStart), so they can be merged.
     * aEnd + 1 is computed only when aEnd is not the largest address.
     */
    private static boolean touches(long aEndHi, long aEndLo, long bStartHi, long bStartLo) {
        if (aEndHi == -1L && aEndLo == -1L) {
            return true;
        }
        long[] next = inc(aEndHi, aEndLo);
        return cmp(next[0], next[1], bStartHi, bStartLo) >= 0;
    }

    /** Address + 1 (wraps to zero past the largest address; callers avoid that). */
    private static long[] inc(long hi, long lo) {
        return lo == -1L ? new long[]{hi + 1, 0} : new long[]{hi, lo + 1};
    }

    /** Address - 1 (callers never pass zero). */
    private static long[] dec(long hi, long lo) {
        return lo == 0 ? new long[]{hi - 1, -1L} : new long[]{hi, lo - 1};
    }

    /** High half of a mask with the lowest {@code bits} bits set (0..128). */
    private static long maskHi(int bits) {
        if (bits <= 64) {
            return 0;
        }
        return bits >= 128 ? -1L : (1L << (bits - 64)) - 1;
    }

    /** Low half of a mask with the lowest {@code bits} bits set (0..128). */
    private static long maskLo(int bits) {
        if (bits >= 64) {
            return -1L;
        }
        return bits <= 0 ? 0 : (1L << bits) - 1;
    }

    /** Count of zero bits at the low end of the 128-bit number (128 for zero). */
    private static int trailingZeros(long hi, long lo) {
        if (lo != 0) {
            return Long.numberOfTrailingZeros(lo);
        }
        return hi != 0 ? 64 + Long.numberOfTrailingZeros(hi) : 128;
    }

    /** Big-endian 8 bytes at {@code off} as a long. */
    private static long word(byte[] b, int off) {
        long v = 0;
        for (int i = 0; i < 8; i++) {
            v = (v << 8) | (b[off + i] & 0xff);
        }
        return v;
    }

    /** Big-endian IPv4 address (the first 4 bytes) as an int. */
    private static int word4(byte[] b) {
        return word4(b, 0);
    }

    /** Big-endian 4 bytes at {@code off} as an int. */
    private static int word4(byte[] b, int off) {
        return ((b[off] & 0xff) << 24) | ((b[off + 1] & 0xff) << 16)
                | ((b[off + 2] & 0xff) << 8) | (b[off + 3] & 0xff);
    }

    /** The address as 16 bytes (IPv6) or 4 bytes (IPv4, from the low 32 bits). */
    private byte[] bytes(long hi, long lo) {
        if (!v6) {
            return new byte[]{(byte) (lo >>> 24), (byte) (lo >>> 16), (byte) (lo >>> 8), (byte) lo};
        }
        byte[] out = new byte[16];
        for (int i = 0; i < 8; i++) {
            out[i] = (byte) (hi >>> (56 - 8 * i));
            out[8 + i] = (byte) (lo >>> (56 - 8 * i));
        }
        return out;
    }

    /** Readable form for test failures, e.g. "[10.0.0.0/8, 192.168.0.0/16]". */
    @Override
    public String toString() {
        return Arrays.toString(toCidrs().toArray());
    }
}
