package app.loft.android;

import android.content.Context;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.ArrayList;
import java.util.Iterator;
import java.util.List;
import java.util.Locale;
import java.util.UUID;

/**
 * Zones + services + exclusions; the same model as the macOS app's lists.
 * These are the user's split-routing lists: which address ranges go
 * through the tunnel and which go direct. Stored as JSON in Prefs under
 * "split_store". RoutePlan applies them when split routing is on.
 */
final class SplitStore {
    // Built-in zones: "local" stays off the tunnel, "global" goes through it.
    // REPO_BASE is the public loft-routes repository with ready-made lists;
    // CATALOG lists the zones offered there, with names in en / ru / zh.
    static final String LOCAL = "local";
    static final String GLOBAL = "global";
    static final String REPO_BASE = "https://raw.githubusercontent.com/a3app/loft-routes/main/";
    // Same window as macOS SplitStore.maybePull: skip if pulled less than 20 h ago.
    static final long AUTO_MS = 20L * 60 * 60 * 1000;
    static final int PULL_MAX = 2 * 1024 * 1024;
    static final String[][] CATALOG = {
            {"global", "World", "Мир", "全球"},
            {"ru", "Russia", "Россия", "俄罗斯"},
            {"cn", "China", "Китай", "中国"},
            {"ir", "Iran", "Иран", "伊朗"},
            {"kz", "Kazakhstan", "Казахстан", "哈萨克斯坦"},
            {"by", "Belarus", "Беларусь", "白俄罗斯"},
    };

    final List<Zone> zones = new ArrayList<>();
    final List<Svc> services = new ArrayList<>();

    /**
     * A group of services with one routing decision: through the tunnel or
     * around it. A zone may follow a list file in the repository (repo); auto
     * is the zone's "auto-update" switch on the Lists screen.
     */
    static final class Zone {
        String id = "";
        String name = "";
        boolean builtin;
        boolean through;
        String repo = "";
        boolean auto = true;
        long pulled;
    }

    /**
     * A named set of prefixes in a zone. src says where it came from: added by
     * the user ("me"), loaded from the zone's repository list ("repo"), or an
     * "ignore" exclusion. Ignore prefixes stay off the tunnel whatever their
     * zone says.
     */
    static final class Svc {
        String id = UUID.randomUUID().toString();
        String name = "";
        String zone = GLOBAL;
        String src = "me"; // me | repo | ignore
        boolean ignore;
        final List<String> cidrs = new ArrayList<>();
    }

    /**
     * Loads the lists from Prefs. On first use seeds the built-in zones and
     * moves the old per-route settings in. A corrupt store, or one missing a
     * built-in zone, is reseeded.
     */
    static SplitStore load(Context c) {
        SplitStore st = new SplitStore();
        String raw = Prefs.p(c).getString("split_store", "");
        if (raw == null || raw.isEmpty()) {
            st.seed();
            st.migrateRoutes(c);
            st.save(c);
            return st;
        }
        try {
            JSONObject o = new JSONObject(raw);
            JSONArray zs = o.optJSONArray("zones");
            if (zs != null) {
                for (int i = 0; i < zs.length(); i++) {
                    JSONObject z = zs.getJSONObject(i);
                    Zone zone = new Zone();
                    zone.id = z.optString("id");
                    zone.name = z.optString("name");
                    zone.builtin = z.optBoolean("builtin");
                    zone.through = z.optBoolean("through");
                    zone.repo = z.optString("repo");
                    zone.auto = z.optBoolean("auto", true);
                    zone.pulled = z.optLong("pulled", 0);
                    if (!zone.id.isEmpty()) {
                        st.zones.add(zone);
                    }
                }
            }
            JSONArray ss = o.optJSONArray("services");
            if (ss != null) {
                for (int i = 0; i < ss.length(); i++) {
                    JSONObject s = ss.getJSONObject(i);
                    Svc svc = new Svc();
                    svc.id = s.optString("id", svc.id);
                    svc.name = s.optString("name");
                    svc.zone = s.optString("zone", GLOBAL);
                    svc.src = s.optString("src", "me");
                    svc.ignore = s.optBoolean("ignore") || "ignore".equals(svc.src);
                    JSONArray cidrs = s.optJSONArray("cidrs");
                    if (cidrs != null) {
                        for (int j = 0; j < cidrs.length(); j++) {
                            String x = cidrs.optString(j).trim();
                            if (Cidr.parse(x) != null) {
                                svc.cidrs.add(x);
                            }
                        }
                    }
                    st.services.add(svc);
                }
            }
        } catch (Exception ignored) {
            st.zones.clear();
            st.services.clear();
        }
        if (st.zone(LOCAL) == null || st.zone(GLOBAL) == null) {
            st.seed();
        }
        return st;
    }

    void save(Context c) {
        JSONObject o = new JSONObject();
        try {
            JSONArray zs = new JSONArray();
            for (Zone z : zones) {
                JSONObject j = new JSONObject();
                j.put("id", z.id);
                j.put("name", z.name);
                j.put("builtin", z.builtin);
                j.put("through", z.through);
                j.put("repo", z.repo);
                j.put("auto", z.auto);
                j.put("pulled", z.pulled);
                zs.put(j);
            }
            JSONArray ss = new JSONArray();
            for (Svc s : services) {
                JSONObject j = new JSONObject();
                j.put("id", s.id);
                j.put("name", s.name);
                j.put("zone", s.zone);
                j.put("src", s.ignore ? "ignore" : s.src);
                j.put("ignore", s.ignore);
                JSONArray cidrs = new JSONArray();
                for (String x : s.cidrs) {
                    cidrs.put(x);
                }
                j.put("cidrs", cidrs);
                ss.put(j);
            }
            o.put("zones", zs);
            o.put("services", ss);
        } catch (Exception ignored) {
        }
        Prefs.p(c).edit().putString("split_store", o.toString()).apply();
    }

    /** Resets to the two built-in zones and a LAN service with the private IPv4 ranges. */
    void seed() {
        zones.clear();
        services.clear();
        Zone local = new Zone();
        local.id = LOCAL;
        local.name = "Local";
        local.builtin = true;
        local.through = false;
        local.auto = true;
        Zone global = new Zone();
        global.id = GLOBAL;
        global.name = "Global";
        global.builtin = true;
        global.through = true;
        global.repo = REPO_BASE + "global.bat";
        global.auto = true;
        zones.add(local);
        zones.add(global);
        Svc lan = new Svc();
        lan.id = "lan";
        lan.name = "LAN";
        lan.zone = LOCAL;
        lan.src = "me";
        lan.cidrs.add("10.0.0.0/8");
        lan.cidrs.add("172.16.0.0/12");
        lan.cidrs.add("192.168.0.0/16");
        services.add(lan);
    }

    /**
     * Moves routes from the older Routes screen (Prefs.routes) into services:
     * "bypass" goes to the local zone, "drop" becomes an ignore entry, and
     * everything else goes to the global zone.
     */
    void migrateRoutes(Context c) {
        for (Prefs.Route r : Prefs.routes(c)) {
            Cidr cidr = Cidr.parse(r.cidr);
            if (cidr == null) {
                continue;
            }
            Svc s = new Svc();
            s.name = cidr.display();
            s.cidrs.add(cidr.display());
            if ("bypass".equals(r.via)) {
                s.zone = LOCAL;
            } else if ("drop".equals(r.via)) {
                s.zone = LOCAL;
                s.src = "ignore";
                s.ignore = true;
                s.name = "ignore";
            } else {
                s.zone = GLOBAL;
            }
            services.add(s);
        }
    }

    Zone zone(String id) {
        for (Zone z : zones) {
            if (z.id.equals(id)) {
                return z;
            }
        }
        return null;
    }

    Svc svc(String id) {
        for (Svc s : services) {
            if (s.id.equals(id)) {
                return s;
            }
        }
        return null;
    }

    /** Number of non-ignore services in a zone. */
    int count(String zoneId) {
        int n = 0;
        for (Svc s : services) {
            if (zoneId.equals(s.zone) && !s.ignore) {
                n++;
            }
        }
        return n;
    }

    /** Services in a zone whose name or prefixes contain the search text. */
    List<Svc> in(String zoneId, String q) {
        String needle = q == null ? "" : q.trim().toLowerCase(Locale.US);
        List<Svc> out = new ArrayList<>();
        for (Svc s : services) {
            if (!zoneId.equals(s.zone)) {
                continue;
            }
            if (!needle.isEmpty() && !hit(s, needle)) {
                continue;
            }
            out.add(s);
        }
        return out;
    }

    /** Deletes a user zone; its services move to the global zone. Built-in zones cannot be deleted. */
    void deleteZone(String id) {
        Zone z = zone(id);
        if (z == null || z.builtin) {
            return;
        }
        for (Svc s : services) {
            if (id.equals(s.zone)) {
                s.zone = GLOBAL;
            }
        }
        Iterator<Zone> it = zones.iterator();
        while (it.hasNext()) {
            if (it.next().id.equals(id)) {
                it.remove();
            }
        }
    }

    /**
     * Replaces a zone's repository services with freshly downloaded rows.
     * Rows with the same label are merged into one service; ignore rows are
     * added to ignore entries. User-added services are kept.
     */
    void replaceRepo(String zoneId, List<Bat.Row> rows) {
        Iterator<Svc> it = services.iterator();
        while (it.hasNext()) {
            Svc s = it.next();
            if (zoneId.equals(s.zone) && "repo".equals(s.src) && !s.ignore) {
                it.remove();
            }
        }
        // group by name
        for (Bat.Row r : rows) {
            if (r.ignore) {
                Svc ign = findIgnore(zoneId, r.cidr);
                if (ign == null) {
                    ign = new Svc();
                    ign.name = "ignore";
                    ign.zone = zoneId;
                    ign.src = "ignore";
                    ign.ignore = true;
                    ign.cidrs.add(r.cidr);
                    services.add(ign);
                } else if (!ign.cidrs.contains(r.cidr)) {
                    ign.cidrs.add(r.cidr);
                }
                continue;
            }
            Svc s = findRepo(zoneId, r.name);
            if (s == null) {
                s = new Svc();
                s.name = r.name;
                s.zone = zoneId;
                s.src = "repo";
                services.add(s);
            }
            if (!s.cidrs.contains(r.cidr)) {
                s.cidrs.add(r.cidr);
            }
        }
    }

    private Svc findRepo(String zoneId, String name) {
        for (Svc s : services) {
            if (zoneId.equals(s.zone) && "repo".equals(s.src) && s.name.equals(name) && !s.ignore) {
                return s;
            }
        }
        return null;
    }

    private Svc findIgnore(String zoneId, String cidr) {
        for (Svc s : services) {
            if (zoneId.equals(s.zone) && s.ignore && s.cidrs.contains(cidr)) {
                return s;
            }
        }
        return null;
    }

    /** Prefixes of every zone that goes through the tunnel (ignore entries excluded). */
    List<Cidr> through() {
        return cidrs(true);
    }

    /** Prefixes of every zone that bypasses the tunnel (ignore entries excluded). */
    List<Cidr> around() {
        return cidrs(false);
    }

    /** Prefixes of all ignore entries, in any zone. */
    List<Cidr> ignores() {
        List<Cidr> out = new ArrayList<>();
        for (Svc s : services) {
            if (!s.ignore) {
                continue;
            }
            addCidrs(s, out);
        }
        return out;
    }

    private List<Cidr> cidrs(boolean through) {
        List<Cidr> out = new ArrayList<>();
        for (Zone z : zones) {
            if (z.through != through) {
                continue;
            }
            for (Svc s : services) {
                if (!z.id.equals(s.zone) || s.ignore) {
                    continue;
                }
                addCidrs(s, out);
            }
        }
        return out;
    }

    private static void addCidrs(Svc s, List<Cidr> out) {
        for (String x : s.cidrs) {
            Cidr c = Cidr.parse(x);
            if (c != null) {
                out.add(c);
            }
        }
    }

    static String join(List<String> cidrs) {
        StringBuilder b = new StringBuilder();
        for (String x : cidrs) {
            if (b.length() > 0) {
                b.append(' ');
            }
            b.append(x);
        }
        return b.toString();
    }

    /** First prefix plus remaining count. List rows must not join every CIDR. */
    static String preview(List<String> cidrs) {
        if (cidrs == null || cidrs.isEmpty()) {
            return "";
        }
        if (cidrs.size() == 1) {
            return cidrs.get(0);
        }
        return cidrs.get(0) + " +" + (cidrs.size() - 1);
    }

    private static boolean hit(Svc s, String needle) {
        if (s.name.toLowerCase(Locale.US).contains(needle)) {
            return true;
        }
        for (String x : s.cidrs) {
            if (x.toLowerCase(Locale.US).contains(needle)) {
                return true;
            }
        }
        return false;
    }

    /** True when the zone has auto-update on, a list URL, and no pull in AUTO_MS. */
    boolean due(Zone z) {
        if (z == null || !z.auto) {
            return false;
        }
        if (z.repo == null || z.repo.trim().isEmpty()) {
            return false;
        }
        return z.pulled <= 0 || System.currentTimeMillis() - z.pulled >= AUTO_MS;
    }

    /**
     * Downloads the zone's .bat and replaces repository services. User-added
     * rows stay. Throws on a bad URL or a failed HTTP GET.
     */
    int pullZone(Zone z) throws Exception {
        if (z == null || z.repo == null || z.repo.trim().isEmpty()) {
            throw new IOException("no url");
        }
        URL u = new URL(z.repo.trim());
        String sch = u.getProtocol();
        if (!"http".equalsIgnoreCase(sch) && !"https".equalsIgnoreCase(sch)) {
            throw new IOException("no url");
        }
        HttpURLConnection c = (HttpURLConnection) u.openConnection();
        c.setConnectTimeout(15000);
        c.setReadTimeout(15000);
        c.setInstanceFollowRedirects(true);
        try (InputStream in = c.getInputStream()) {
            List<Bat.Row> rows = Bat.parse(read(in));
            if (rows.isEmpty()) {
                throw new IOException("empty list");
            }
            replaceRepo(z.id, rows);
            z.pulled = System.currentTimeMillis();
            return rows.size();
        } finally {
            c.disconnect();
        }
    }

    /**
     * Pulls every due auto zone. Returns true when at least one list changed;
     * the caller must save.
     */
    boolean pullDue() {
        boolean changed = false;
        for (Zone z : zones) {
            if (!due(z)) {
                continue;
            }
            try {
                pullZone(z);
                changed = true;
            } catch (Exception e) {
                Journal.add("routes " + z.id + " " + e.getMessage());
            }
        }
        return changed;
    }

    private static String read(InputStream in) throws Exception {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        byte[] buf = new byte[4096];
        int n;
        int total = 0;
        while ((n = in.read(buf)) > 0) {
            total += n;
            if (total > PULL_MAX) {
                throw new IOException("list too large");
            }
            out.write(buf, 0, n);
        }
        return out.toString("UTF-8");
    }

    static String catalogUrl(String id) {
        return REPO_BASE + id + ".bat";
    }

    static String catalogLabel(String id, String lang) {
        for (String[] row : CATALOG) {
            if (row[0].equals(id)) {
                if ("ru".equals(lang)) {
                    return row[2];
                }
                if ("zh".equals(lang)) {
                    return row[3];
                }
                return row[1];
            }
        }
        return id;
    }
}
