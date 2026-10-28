/*
 * Android client, shared code. RoutePlan: decides which IP ranges
 * LoftVpnService puts into the TUN device (the virtual network interface
 * Android gives a VPN app). It combines the session Snapshot from the Go
 * engine (overlay address, node-pushed routes, front IPs, i.e. the public
 * addresses the app dials to reach the node) with the user's settings
 * (default route, LAN bypass, split lists from SplitStore). Cidr does the
 * prefix arithmetic.
 */
package app.loft.android;

import android.content.Context;
import android.os.Build;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

import app.loft.engine.mobile.Snapshot;

/**
 * Positive TUN routes plus FrontIP and split-list excludes.
 *
 * <p>Android VPN routes are "positive": you list what goes into the tunnel.
 * On API 33+ a bypass is {@code excludeRoute} (LAN, around, ignore, front IPs)
 * and {@code 0.0.0.0/0} stays one prefix. Older images subtract those prefixes
 * from the include list. Steps: default route; overlay subnet; node-pushed
 * routes; split through; then bypass. {@code include} goes to
 * Builder.addRoute, {@code exclude} to excludeRoute on API 33+, {@code drop}
 * is filtered packet by packet.
 */
final class RoutePlan {
    /** Prefixes routed into the tunnel (Builder.addRoute). */
    final List<Cidr> include = new ArrayList<>();
    /** Prefixes kept off the tunnel via excludeRoute; only used on API 33+. */
    final List<Cidr> exclude = new ArrayList<>();
    /** Blocked prefixes: routed into the tunnel, then their packets are thrown away. */
    final List<Cidr> drop = new ArrayList<>();
    /**
     * {@code drop} as sorted ranges for the per-packet check (see
     * compileDrop); null when nothing is dropped. Volatile because the TUN
     * reader thread reads them while another thread may compile a new plan.
     */
    private volatile Ranges drop4;
    private volatile Ranges drop6;
    /**
     * The overlay subnets: the private network the node hands out to its
     * members (this device's tunnel address plus prefix). Null when absent.
     */
    Cidr overlay4;
    /** IPv6 overlay subnet; when null the plan routes no IPv6 default (see build step 3). */
    Cidr overlay6;

    /**
     * Builds the route plan for a session from its Snapshot and the user's
     * routing settings. Reads prefs and, with split on, loads and parses the
     * whole SplitStore. LoftVpnService calls it from its once-a-second poll,
     * then compares key() to decide whether the TUN must be rebuilt.
     * excludeRoute exists only from Android 13 (API 33), hence the version check.
     */
    static RoutePlan of(Context ctx, Snapshot snap) {
        String ip = "", ip6 = "", routes = "", fronts = "";
        int prefix = 0, prefix6 = 0;
        if (snap != null) {
            ip = nz(snap.getIP());
            ip6 = nz(snap.getIP6());
            prefix = snap.getPrefix() > 0 ? (int) snap.getPrefix() : 0;
            prefix6 = snap.getPrefix6() > 0 ? (int) snap.getPrefix6() : 0;
            routes = snap.getRoutes();
            fronts = snap.getFrontIPs();
        }
        boolean split = Prefs.split(ctx);
        return build(
                Prefs.defaultRoute(ctx),
                Prefs.lanBypass(ctx),
                split,
                split ? SplitStore.load(ctx) : null,
                Build.VERSION.SDK_INT >= 33,
                ip, prefix, ip6, prefix6, routes, fronts);
    }

    /**
     * Same plan without Android prefs. {@code canExclude} is true on API 33+,
     * where bypass prefixes go to {@code exclude} instead of being subtracted.
     * Pure function of its inputs, which is what the unit tests call.
     * Missing prefix lengths default to /24 (IPv4) and /64 (IPv6).
     */
    static RoutePlan build(boolean def, boolean lan, boolean split, SplitStore store,
            boolean canExclude, String ip, int prefix, String ip6, int prefix6,
            String routes, String frontIPs) {
        RoutePlan p = new RoutePlan();
        // 1. Overlay subnets from the session address, and front IPs as host excludes.
        if (ip != null && !ip.isEmpty()) {
            int n = prefix > 0 ? prefix : 24;
            p.overlay4 = Cidr.parse(ip + "/" + n);
        }
        if (ip6 != null && !ip6.isEmpty()) {
            int n = prefix6 > 0 ? prefix6 : 64;
            p.overlay6 = Cidr.parse(ip6 + "/" + n);
        }
        p.addFronts(frontIPs);

        // 2. Split lists. Blocked prefixes are also routed in, so their packets
        //    reach the app and can be dropped.
        List<Cidr> extraThrough = new ArrayList<>();
        List<Cidr> extraBypass = new ArrayList<>();
        List<Cidr> extraDrop = new ArrayList<>();
        if (split && store != null) {
            extraThrough.addAll(store.through());
            extraBypass.addAll(store.around());
            extraBypass.addAll(store.ignores());
            extraDrop.addAll(store.blocked());
            extraThrough.addAll(extraDrop);
            p.drop.addAll(extraDrop);
        }

        // 3. Default route (all traffic); on old images the LAN is cut out here.
        List<Cidr> v4 = new ArrayList<>();
        List<Cidr> v6 = new ArrayList<>();
        if (def) {
            v4.add(Cidr.parse("0.0.0.0/0"));
            if (lan && !canExclude) {
                v4 = Cidr.subtractAll(v4, Cidr.rfc1918());
            }
            // gVisor (the user-space TCP/IP stack inside the engine) inbound is
            // IPv4-only. Routing ::/0 without an IPv6 overlay would swallow
            // IPv6 traffic with no way out, and browsers using Happy Eyeballs
            // (try IPv6 and IPv4 in parallel) would stall on the dead IPv6 path.
            if (p.overlay6 != null) {
                v6.add(Cidr.parse("::/0"));
                if (lan && !canExclude) {
                    v6 = Cidr.subtractAll(v6, Cidr.rfc4193());
                }
            }
        }
        // 4. Routes pushed by the node, then the user's "through" prefixes.
        if (routes != null) {
            for (String s : routes.split(",")) {
                Cidr c = Cidr.parse(s.trim());
                if (c != null) {
                    (c.v6 ? v6 : v4).add(c);
                }
            }
        }
        v4.addAll(filter(extraThrough, false));
        v6.addAll(filter(extraThrough, true));

        // 5. Bypass: excluded on API 33+, subtracted from the include list before that.
        List<Cidr> bypass = new ArrayList<>();
        if (lan && canExclude) {
            bypass.addAll(Cidr.rfc1918());
            if (p.overlay6 != null) {
                bypass.addAll(Cidr.rfc4193());
            }
        }
        bypass.addAll(extraBypass);

        if (!canExclude) {
            if (!bypass.isEmpty()) {
                v4 = Cidr.subtractAll(v4, filter(bypass, false));
                v6 = Cidr.subtractAll(v6, filter(bypass, true));
            }
            // Front IPs: subtract so older images do not hairpin.
            if (!p.exclude.isEmpty()) {
                v4 = Cidr.subtractAll(v4, filter(p.exclude, false));
                v6 = Cidr.subtractAll(v6, filter(p.exclude, true));
            }
        } else {
            p.exclude.addAll(bypass);
        }
        // Overlay often sits inside RFC1918 / Local LAN. Add it last so a
        // bypass hole does not swallow the member prefix.
        if (p.overlay4 != null) {
            v4.add(p.overlay4);
        }
        if (p.overlay6 != null) {
            v6.add(p.overlay6);
        }
        p.include.addAll(v4);
        p.include.addAll(v6);
        p.fitBudget();
        p.compileDrop();
        return p;
    }

    /**
     * Most routes (include plus exclude) handed to Android. Android sends the
     * VPN network's whole route table to every app that watches the network,
     * in one binder (IPC) message capped at about 1 MB; each route costs
     * about 140 bytes there, so roughly 7,000 routes fill it. Big split lists
     * reached that, the system could no longer deliver the table, and apps
     * that asked for it crashed while loft was connected. 1,200 routes is
     * about 170 KB, far below the cap even next to other traffic.
     */
    static final int MAX_ROUTES = 1200;

    /**
     * True when fitBudget had to widen prefixes to stay under MAX_ROUTES;
     * {@code fitted} says how many routes there were before.
     */
    boolean coarse;
    int fitted;

    /** Applies the route budget to a plan built by hand (tests); build() already does it. */
    static RoutePlan withBudget(RoutePlan p) {
        p.fitBudget();
        p.compileDrop();
        return p;
    }

    /**
     * Keeps include + exclude under MAX_ROUTES. Nothing changes when they
     * already fit. Otherwise the exclude list (the bypass lists, usually the
     * big one) is squeezed first, then the include list, each with Cidr.fit,
     * which only ever widens prefixes, so every address keeps its side or
     * moves next to a neighbour of the same list.
     *
     * <p>The default routes 0.0.0.0/0 and ::/0 are kept out of the include
     * squeeze on purpose: merged together, every narrower include route
     * would vanish into them, but those narrower routes matter. Android picks
     * the most specific matching route, so a "through" prefix inside a
     * bypassed range only works because it is more specific than the bypass.
     *
     * <p>Blocked prefixes are unaffected in effect: their packets are still
     * checked one by one against the exact {@code drop} list.
     */
    private void fitBudget() {
        int before = include.size() + exclude.size();
        if (before <= MAX_ROUTES) {
            return;
        }
        List<Cidr> defaults = new ArrayList<>();
        List<Cidr> rest = new ArrayList<>();
        for (Cidr c : include) {
            (c.bits == 0 ? defaults : rest).add(c);
        }
        int exMax = Math.max(MAX_ROUTES / 2, MAX_ROUTES - include.size());
        List<Cidr> ex = Cidr.fit(exclude, exMax);
        int inMax = Math.max(1, MAX_ROUTES - ex.size() - defaults.size());
        List<Cidr> in = rest.size() > inMax ? Cidr.fit(rest, inMax) : rest;
        exclude.clear();
        exclude.addAll(ex);
        include.clear();
        include.addAll(defaults);
        include.addAll(in);
        coarse = true;
        fitted = before;
    }

    /**
     * Fingerprint of include/exclude/drop; used to skip a TUN rebuild.
     * A 32-bit hash, so two different plans could in theory collide.
     */
    String key() {
        int h = 1;
        h = mix(h, include);
        h = mix(h, exclude);
        h = mix(h, drop);
        return Integer.toString(h);
    }

    /** Folds one prefix list into the running hash. */
    private static int mix(int h, List<Cidr> list) {
        h = 31 * h + list.size();
        for (Cidr c : list) {
            h = 31 * h + c.bits;
            h = 31 * h + (c.v6 ? 1 : 0);
            h = 31 * h + Arrays.hashCode(c.addr);
        }
        return h;
    }

    /** Null to empty string. */
    private static String nz(String s) {
        return s == null ? "" : s;
    }

    /**
     * True when an IPv4/IPv6 packet's destination falls in a drop prefix.
     * Called by LoftVpnService's TUN reader thread for every outgoing packet,
     * so it returns at once when there is nothing to drop. The destination
     * address sits at byte 16 of an IPv4 header and byte 24 of an IPv6 header.
     */
    boolean destDropped(byte[] pkt) {
        return pkt != null && destDropped(pkt, pkt.length);
    }

    /**
     * Same check for the first {@code n} bytes of {@code buf}, so the TUN
     * reader can test its reusable read buffer without copying the packet.
     * Uses the sorted drop ranges (binary search, no allocation).
     */
    boolean destDropped(byte[] buf, int n) {
        Ranges d4 = drop4;
        Ranges d6 = drop6;
        if (buf == null || n < 20 || d4 == null) {
            return false;
        }
        int ver = (buf[0] >> 4) & 0xf;
        if (ver == 4) {
            return d4.contains(buf, 16);
        }
        if (ver == 6 && n >= 40) {
            return d6.contains(buf, 24);
        }
        return false;
    }

    /**
     * Compiles {@code drop} into the sorted ranges destDropped searches. build
     * calls it once; call it again only if {@code drop} is changed by hand.
     * Leaves both null when nothing is dropped, so the per-packet check is a
     * single null test.
     */
    void compileDrop() {
        if (drop.isEmpty()) {
            drop4 = null;
            drop6 = null;
            return;
        }
        drop6 = Ranges.of(drop, true);
        drop4 = Ranges.of(drop, false);
    }

    // Front IPs become /32 or /128 excludes: traffic to the node itself must
    // go over the real network, never through its own tunnel (a "hairpin").
    private void addFronts(String csv) {
        if (csv == null || csv.isEmpty()) {
            return;
        }
        for (String s : csv.split(",")) {
            Cidr c = Cidr.host(Cidr.numeric(s.trim()));
            if (c != null) {
                exclude.add(c);
            }
        }
    }

    /** Only the IPv6 (true) or only the IPv4 (false) prefixes of {@code in}. */
    private static List<Cidr> filter(List<Cidr> in, boolean v6) {
        List<Cidr> out = new ArrayList<>();
        for (Cidr c : in) {
            if (c.v6 == v6) {
                out.add(c);
            }
        }
        return out;
    }
}
