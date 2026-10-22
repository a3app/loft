package app.loft.android;

import android.content.Context;

import java.util.ArrayList;
import java.util.List;

import app.loft.engine.Snapshot;

/** Positive TUN routes plus FrontIP excludes. */
final class RoutePlan {
    final List<Cidr> include = new ArrayList<>();
    final List<Cidr> exclude = new ArrayList<>();
    final List<Cidr> drop = new ArrayList<>();
    Cidr overlay4;
    Cidr overlay6;

    static RoutePlan of(Context ctx, Snapshot snap) {
        RoutePlan p = new RoutePlan();
        if (snap != null) {
            String ip = snap.getIP();
            if (ip != null && !ip.isEmpty()) {
                int n = snap.getPrefix() > 0 ? snap.getPrefix() : 24;
                p.overlay4 = Cidr.parse(ip + "/" + n);
            }
            String ip6 = snap.getIP6();
            if (ip6 != null && !ip6.isEmpty()) {
                int n = snap.getPrefix6() > 0 ? snap.getPrefix6() : 64;
                p.overlay6 = Cidr.parse(ip6 + "/" + n);
            }
            p.addFronts(snap.getFrontIPs());
        }
        boolean def = Prefs.defaultRoute(ctx);
        boolean lan = Prefs.lanBypass(ctx);
        boolean split = Prefs.split(ctx);
        List<Cidr> v4 = new ArrayList<>();
        List<Cidr> v6 = new ArrayList<>();
        if (def) {
            v4.add(Cidr.parse("0.0.0.0/0"));
            v6.add(Cidr.parse("::/0"));
            if (lan) {
                v4 = Cidr.subtractAll(v4, Cidr.rfc1918());
                v6 = Cidr.subtractAll(v6, Cidr.rfc4193());
            }
        }
        if (p.overlay4 != null) {
            v4.add(p.overlay4);
        }
        if (p.overlay6 != null) {
            v6.add(p.overlay6);
        }
        if (snap != null) {
            String routes = snap.getRoutes();
            if (routes != null) {
                for (String s : routes.split(",")) {
                    Cidr c = Cidr.parse(s.trim());
                    if (c != null) {
                        (c.v6 ? v6 : v4).add(c);
                    }
                }
            }
        }
        List<Cidr> extraThrough = new ArrayList<>();
        List<Cidr> extraBypass = new ArrayList<>();
        if (split) {
            for (Prefs.Route r : Prefs.routes(ctx)) {
                Cidr c = Cidr.parse(r.cidr);
                if (c == null) {
                    continue;
                }
                if ("bypass".equals(r.via)) {
                    extraBypass.add(c);
                } else if ("drop".equals(r.via)) {
                    p.drop.add(c);
                } else {
                    extraThrough.add(c);
                }
            }
        }
        v4.addAll(filter(extraThrough, false));
        v6.addAll(filter(extraThrough, true));
        if (!extraBypass.isEmpty()) {
            v4 = Cidr.subtractAll(v4, filter(extraBypass, false));
            v6 = Cidr.subtractAll(v6, filter(extraBypass, true));
        }
        if (!p.drop.isEmpty()) {
            v4 = Cidr.subtractAll(v4, filter(p.drop, false));
            v6 = Cidr.subtractAll(v6, filter(p.drop, true));
        }
        // Front IPs: exclude on API 33+; also subtract so older images do not hairpin.
        if (!p.exclude.isEmpty()) {
            v4 = Cidr.subtractAll(v4, filter(p.exclude, false));
            v6 = Cidr.subtractAll(v6, filter(p.exclude, true));
        }
        p.include.addAll(v4);
        p.include.addAll(v6);
        return p;
    }

    boolean destDropped(byte[] pkt) {
        if (pkt == null || pkt.length < 20 || drop.isEmpty()) {
            return false;
        }
        int ver = (pkt[0] >> 4) & 0xf;
        Cidr dst;
        try {
            if (ver == 4) {
                byte[] a = new byte[]{pkt[16], pkt[17], pkt[18], pkt[19]};
                dst = new Cidr(a, 32, false);
            } else if (ver == 6 && pkt.length >= 40) {
                byte[] a = new byte[16];
                System.arraycopy(pkt, 24, a, 0, 16);
                dst = new Cidr(a, 128, true);
            } else {
                return false;
            }
        } catch (Exception e) {
            return false;
        }
        for (Cidr d : drop) {
            if (d.contains(dst)) {
                return true;
            }
        }
        return false;
    }

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
