/*
 * Android client, shared by the phone and TV builds: the data model and storage
 * for split routing (zones, services and exclusions that decide which address
 * ranges use the tunnel). Saved as JSON in Prefs, edited on ListsActivity,
 * turned into TUN routes by RoutePlan, and refreshed from the public
 * loft-routes lists (Keenetic .bat files, parsed by Bat). Same model as the
 * macOS app's lists.
 */
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
    // Built-in zones: "local" stays off the tunnel, "global" goes through it,
    // "block" drops its traffic.
    // REPO_BASE is the public loft-routes repository with ready-made lists;
    // CATALOG lists the zones offered there, with names in en / ru / zh.
    static final String LOCAL = "local";
    static final String GLOBAL = "global";
    static final String BLOCK = "block";
    static final String REPO_BASE = "https://raw.githubusercontent.com/a3app/loft-routes/main/";
    // Same window as macOS SplitStore.maybePull: skip if pulled less than 20 h ago.
    static final long AUTO_MS = 20L * 60 * 60 * 1000;
    // Largest list file pullZone accepts (2 MiB), so a bad URL cannot fill memory.
    static final int PULL_MAX = 2 * 1024 * 1024;
    static final String[][] CATALOG = {
            {"global", "World", "Мир", "全球"},
            {"ru", "Russia", "Россия", "俄罗斯"},
            {"cn", "China", "Китай", "中国"},
            {"ir", "Iran", "Иран", "伊朗"},
            {"kz", "Kazakhstan", "Казахстан", "哈萨克斯坦"},
            {"by", "Belarus", "Беларусь", "白俄罗斯"},
    };

    /** All zones in display order, built-in ones included. */
    final List<Zone> zones = new ArrayList<>();
    /** All services of all zones; each one names its zone by id. */
    final List<Svc> services = new ArrayList<>();

    /**
     * A group of services with one routing decision: through the tunnel or
     * around it. A zone may follow a list file in the repository (repo); auto
     * is the zone's "auto-update" switch on the Lists screen. A block zone
     * drops its traffic and overrides through. pulled is the time of the last
     * successful download, in ms since the epoch (0 = never).
     */
    static final class Zone {
        String id = "";
        String name = "";
        boolean builtin;
        boolean through;
        boolean block;
        String repo = "";
        boolean auto = true;
        long pulled;

        /** True when the zone's traffic goes through the tunnel. */
        boolean isThrough() {
            return !block && through;
        }

        /** True when the zone's traffic goes direct, around the tunnel. */
        boolean isAround() {
            return !block && !through;
        }

        /** Direction as the Lists screen's segment index: 0 through, 1 around, 2 block. */
        int dir() {
            return block ? 2 : through ? 0 : 1;
        }

        /** Sets the direction from a segment index (see dir). */
        void setDir(int i) {
            block = i == 2;
            through = i == 0;
        }
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
        final List<String> domains = new ArrayList<>();
    }

    /** Snapshot of zones + services for the 5 s undo snackbar. */
    static final class Snap {
        final List<Zone> zones = new ArrayList<>();
        final List<Svc> services = new ArrayList<>();
    }

    /** Result of removing services: count, and whether list nets moved to exclusions. */
    static final class Gone {
        int n;
        boolean toExcl;
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
            st.readJSON(new JSONObject(raw));
        } catch (Exception ignored) {
            st.zones.clear();
            st.services.clear();
        }
        if (st.zone(LOCAL) == null || st.zone(GLOBAL) == null) {
            st.seed();
        } else if (st.zone(BLOCK) == null) {
            st.zones.add(blockZone());
        }
        return st;
    }

    /** Writes the lists to Prefs as JSON under "split_store". */
    void save(Context c) {
        Prefs.p(c).edit().putString("split_store", writeJSON().toString()).apply();
    }

    /**
     * Operator/link SplitJSON → zones+services. Used for the lists stored
     * with a profile (ProfileStore). Missing builtins are filled; LAN is not
     * injected. Bad JSON gives just the built-in zones.
     */
    static SplitStore fromJSON(String raw) {
        SplitStore st = new SplitStore();
        if (raw == null || raw.isEmpty()) {
            st.ensureBuiltins();
            return st;
        }
        try {
            st.readJSON(new JSONObject(raw));
        } catch (Exception ignored) {
            st.zones.clear();
            st.services.clear();
        }
        st.ensureBuiltins();
        return st;
    }

    /** Deep copy, so a profile's lists can be edited without touching the original. */
    SplitStore copy() {
        SplitStore n = new SplitStore();
        n.restore(snap());
        return n;
    }

    /**
     * Appends zones and services from the stored JSON form. Zones without an
     * id are skipped; prefixes that do not parse are dropped; domains are
     * lower-cased and de-duplicated. src "ignore" also sets the ignore flag.
     */
    void readJSON(JSONObject o) throws Exception {
        // 1. Zones.
        JSONArray zs = o.optJSONArray("zones");
        if (zs != null) {
            for (int i = 0; i < zs.length(); i++) {
                JSONObject z = zs.getJSONObject(i);
                Zone zone = new Zone();
                zone.id = z.optString("id");
                zone.name = z.optString("name");
                zone.builtin = z.optBoolean("builtin");
                zone.through = z.optBoolean("through");
                zone.block = z.optBoolean("block");
                zone.repo = z.optString("repo");
                zone.auto = z.optBoolean("auto", true);
                zone.pulled = z.optLong("pulled", 0);
                if (!zone.id.isEmpty()) {
                    zones.add(zone);
                }
            }
        }
        // 2. Services with their prefixes and domains.
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
                JSONArray domains = s.optJSONArray("domains");
                if (domains != null) {
                    for (int j = 0; j < domains.length(); j++) {
                        String d = domains.optString(j).trim().toLowerCase(Locale.US);
                        if (!d.isEmpty() && !svc.domains.contains(d)) {
                            svc.domains.add(d);
                        }
                    }
                }
                services.add(svc);
            }
        }
    }

    /** The JSON form read back by readJSON. Ignore entries are written with src "ignore". */
    JSONObject writeJSON() {
        JSONObject o = new JSONObject();
        try {
            JSONArray zs = new JSONArray();
            for (Zone z : zones) {
                JSONObject j = new JSONObject();
                j.put("id", z.id);
                j.put("name", z.name);
                j.put("builtin", z.builtin);
                j.put("through", z.through);
                j.put("block", z.block);
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
                JSONArray domains = new JSONArray();
                for (String d : s.domains) {
                    domains.put(d);
                }
                j.put("domains", domains);
                ss.put(j);
            }
            o.put("zones", zs);
            o.put("services", ss);
        } catch (Exception ignored) {
        }
        return o;
    }

    /** Adds any missing built-in zone (local, global, block) without touching services. */
    void ensureBuiltins() {
        if (zone(LOCAL) == null) {
            Zone local = new Zone();
            local.id = LOCAL;
            local.name = "Local";
            local.builtin = true;
            local.through = false;
            local.auto = true;
            zones.add(0, local);
        }
        if (zone(GLOBAL) == null) {
            Zone global = new Zone();
            global.id = GLOBAL;
            global.name = "Global";
            global.builtin = true;
            global.through = true;
            global.repo = REPO_BASE + "global.bat";
            global.auto = true;
            zones.add(global);
        }
        if (zone(BLOCK) == null) {
            zones.add(blockZone());
        }
    }

    /**
     * Resets to the built-in zones (local, global, block) and a LAN service
     * with the private IPv4 ranges in the local zone.
     */
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
        zones.add(blockZone());
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

    /** A new built-in "block" zone. */
    private static Zone blockZone() {
        Zone z = new Zone();
        z.id = BLOCK;
        z.name = "Block";
        z.builtin = true;
        z.through = false;
        z.block = true;
        z.auto = true;
        return z;
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

    /** The zone with this id, or null. */
    Zone zone(String id) {
        for (Zone z : zones) {
            if (z.id.equals(id)) {
                return z;
            }
        }
        return null;
    }

    /** The service with this id, or null. */
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

    /** Non-ignore services in a zone whose name, prefixes or domains contain the search text. */
    List<Svc> in(String zoneId, String q) {
        String needle = q == null ? "" : q.trim().toLowerCase(Locale.US);
        List<Svc> out = new ArrayList<>();
        for (Svc s : services) {
            if (!zoneId.equals(s.zone) || s.ignore) {
                continue;
            }
            if (!needle.isEmpty() && !hit(s, needle)) {
                continue;
            }
            out.add(s);
        }
        return out;
    }

    /** Ignore entries of a zone. */
    List<Svc> excls(String zoneId) {
        List<Svc> out = new ArrayList<>();
        for (Svc s : services) {
            if (zoneId.equals(s.zone) && s.ignore) {
                out.add(s);
            }
        }
        return out;
    }

    /** Number of excluded prefixes (not entries) in a zone. */
    int exclCount(String zoneId) {
        int n = 0;
        for (Svc s : services) {
            if (zoneId.equals(s.zone) && s.ignore) {
                n += s.cidrs.size();
            }
        }
        return n;
    }

    /** Deep copy of the current lists, for undo. */
    Snap snap() {
        Snap s = new Snap();
        for (Zone z : zones) {
            s.zones.add(copyZone(z));
        }
        for (Svc x : services) {
            s.services.add(copySvc(x));
        }
        return s;
    }

    /** Replaces the lists with a copy of a snapshot; null does nothing. */
    void restore(Snap s) {
        if (s == null) {
            return;
        }
        zones.clear();
        services.clear();
        for (Zone z : s.zones) {
            zones.add(copyZone(z));
        }
        for (Svc x : s.services) {
            services.add(copySvc(x));
        }
    }

    /**
     * Removes the services with these ids. A removed repository service
     * leaves its prefixes behind as ignore entries, so the next list pull
     * does not bring them back.
     */
    Gone removeSvcs(List<String> ids) {
        Gone g = new Gone();
        if (ids == null || ids.isEmpty()) {
            return g;
        }
        List<Svc> extra = new ArrayList<>();
        Iterator<Svc> it = services.iterator();
        while (it.hasNext()) {
            Svc s = it.next();
            if (!ids.contains(s.id)) {
                continue;
            }
            it.remove();
            g.n++;
            if ("repo".equals(s.src) && !s.ignore) {
                g.toExcl = true;
                for (String c : s.cidrs) {
                    Svc ign = new Svc();
                    ign.name = s.name;
                    ign.zone = s.zone;
                    ign.src = "ignore";
                    ign.ignore = true;
                    ign.cidrs.add(c);
                    extra.add(ign);
                }
            }
        }
        services.addAll(extra);
        return g;
    }

    /** how: "me" own services, "all" every service and unlink the list, "excl" exclusions. */
    void clearZone(String id, String how) {
        Iterator<Svc> it = services.iterator();
        while (it.hasNext()) {
            Svc s = it.next();
            if (!id.equals(s.zone)) {
                continue;
            }
            if ("excl".equals(how)) {
                if (s.ignore) {
                    it.remove();
                }
            } else if ("me".equals(how)) {
                if (!s.ignore && "me".equals(s.src)) {
                    it.remove();
                }
            } else if (!s.ignore) {
                it.remove();
            }
        }
        if ("all".equals(how)) {
            Zone z = zone(id);
            if (z != null) {
                z.repo = "";
                z.auto = false;
            }
        }
    }

    /** Adds parsed rows that this zone does not already hold. Returns how many were new. */
    int addRows(String zoneId, List<Bat.Row> rows) {
        int n = 0;
        if (rows == null) {
            return 0;
        }
        for (Bat.Row r : rows) {
            if (r == null || r.cidr == null || r.cidr.isEmpty() || hasCidr(zoneId, r.cidr)) {
                continue;
            }
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
                n++;
                continue;
            }
            String name = r.name == null || r.name.isEmpty() ? r.cidr : r.name;
            Svc s = findRepo(zoneId, name);
            if (s == null) {
                s = new Svc();
                s.name = name;
                s.zone = zoneId;
                s.src = "me";
                services.add(s);
            }
            if (!s.cidrs.contains(r.cidr)) {
                s.cidrs.add(r.cidr);
            }
            n++;
        }
        return n;
    }

    /** True when any entry of the zone (ignore entries included) already holds this prefix. */
    private boolean hasCidr(String zoneId, String cidr) {
        for (Svc s : services) {
            if (zoneId.equals(s.zone) && s.cidrs.contains(cidr)) {
                return true;
            }
        }
        return false;
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

    /** The zone's non-ignore repository service with this name, or null. */
    private Svc findRepo(String zoneId, String name) {
        for (Svc s : services) {
            if (zoneId.equals(s.zone) && "repo".equals(s.src) && s.name.equals(name) && !s.ignore) {
                return s;
            }
        }
        return null;
    }

    /** The zone's ignore entry holding this prefix, or null. */
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

    /**
     * Prefixes of every blocking zone. They are routed into the TUN, and
     * LoftVpnService's reader then discards packets to them
     * (RoutePlan.destDropped), so they never leave the device.
     */
    List<Cidr> blocked() {
        List<Cidr> out = new ArrayList<>();
        for (Zone z : zones) {
            if (!z.block) {
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

    /** Parsed prefixes of non-block zones with the given direction, ignore entries left out. */
    private List<Cidr> cidrs(boolean through) {
        List<Cidr> out = new ArrayList<>();
        for (Zone z : zones) {
            if (z.block || z.through != through) {
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

    /** Appends the service's prefixes that parse; bad ones are skipped. */
    private static void addCidrs(Svc s, List<Cidr> out) {
        for (String x : s.cidrs) {
            Cidr c = Cidr.parse(x);
            if (c != null) {
                out.add(c);
            }
        }
    }

    /** Space-separated list, as shown in edit fields and exclusion rows. */
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

    /** Row preview for a service: its prefixes, or its domains when it has none. */
    static String preview(Svc s) {
        if (s.cidrs != null && !s.cidrs.isEmpty()) {
            return preview(s.cidrs);
        }
        if (s.domains != null && !s.domains.isEmpty()) {
            return preview(s.domains);
        }
        return "";
    }

    /** True when the lower-case search text is in the name, a prefix or a domain. */
    private static boolean hit(Svc s, String needle) {
        if (s.name.toLowerCase(Locale.US).contains(needle)) {
            return true;
        }
        for (String x : s.cidrs) {
            if (x.toLowerCase(Locale.US).contains(needle)) {
                return true;
            }
        }
        for (String d : s.domains) {
            if (d.contains(needle)) {
                return true;
            }
        }
        return false;
    }

    /** Field-by-field copy of a zone. */
    private static Zone copyZone(Zone z) {
        Zone n = new Zone();
        n.id = z.id;
        n.name = z.name;
        n.builtin = z.builtin;
        n.through = z.through;
        n.block = z.block;
        n.repo = z.repo;
        n.auto = z.auto;
        n.pulled = z.pulled;
        return n;
    }

    /** Copy of a service with its own prefix and domain lists; the id is kept. */
    private static Svc copySvc(Svc s) {
        Svc n = new Svc();
        n.id = s.id;
        n.name = s.name;
        n.zone = s.zone;
        n.src = s.src;
        n.ignore = s.ignore;
        n.cidrs.addAll(s.cidrs);
        n.domains.addAll(s.domains);
        return n;
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

    /** Reads the whole response as UTF-8; fails past PULL_MAX bytes. */
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

    /** URL of a catalog zone's list in the loft-routes repository. */
    static String catalogUrl(String id) {
        return REPO_BASE + id + ".bat";
    }

    /** Catalog zone name for a UI language ("ru", "zh", else English); the id if unknown. */
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
