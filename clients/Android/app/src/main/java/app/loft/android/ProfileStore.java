package app.loft.android;

import android.content.Context;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;
import java.util.UUID;

/**
 * Catalog of route profiles. Prefs "split_store" is the ACTIVE document
 * RoutePlan reads. Prefs "route_profiles" holds every profile: mine
 * (migrated from split_store), op (from the node), link (imported).
 * One is active. Operator slot is read-only and overwritten on serial change.
 */
final class ProfileStore {
    static final String MINE = "mine";
    static final String OP = "op";

    String active = MINE;
    final List<Prof> profiles = new ArrayList<>();

    static final class Prof {
        String id = "";
        String src = "me"; // me | op | link
        String name = "";
        String conn = "";
        String dns = "";
        long serial;
        long updated;
        SplitStore store = new SplitStore();
    }

    static ProfileStore load(Context c) {
        String raw = Prefs.p(c).getString("route_profiles", "");
        if (raw == null || raw.isEmpty()) {
            ProfileStore ps = new ProfileStore();
            Prof mine = new Prof();
            mine.id = MINE;
            mine.src = "me";
            mine.store = SplitStore.load(c);
            mine.updated = System.currentTimeMillis();
            ps.profiles.add(mine);
            ps.active = MINE;
            ps.save(c);
            return ps;
        }
        ProfileStore ps = parse(raw);
        if (ps.get(ps.active) == null) {
            ps.active = ps.profiles.isEmpty() ? MINE : ps.profiles.get(0).id;
        }
        return ps;
    }

    void save(Context c) {
        Prefs.p(c).edit().putString("route_profiles", toJSON()).apply();
    }

    static ProfileStore parse(String raw) {
        ProfileStore ps = new ProfileStore();
        try {
            JSONObject o = new JSONObject(raw);
            ps.active = o.optString("active", MINE);
            JSONArray arr = o.optJSONArray("profiles");
            if (arr != null) {
                for (int i = 0; i < arr.length(); i++) {
                    JSONObject j = arr.getJSONObject(i);
                    Prof p = new Prof();
                    p.id = j.optString("id");
                    p.src = j.optString("src", "me");
                    p.name = j.optString("name");
                    p.conn = j.optString("conn");
                    p.dns = j.optString("dns");
                    p.serial = j.optLong("serial");
                    p.updated = j.optLong("updated");
                    JSONObject st = j.optJSONObject("store");
                    if (st != null) {
                        p.store.readJSON(st);
                        p.store.ensureBuiltins();
                    } else {
                        p.store.ensureBuiltins();
                    }
                    if (!p.id.isEmpty()) {
                        ps.profiles.add(p);
                    }
                }
            }
        } catch (Exception ignored) {
        }
        return ps;
    }

    String toJSON() {
        JSONObject o = new JSONObject();
        try {
            o.put("active", active);
            JSONArray arr = new JSONArray();
            for (Prof p : profiles) {
                JSONObject j = new JSONObject();
                j.put("id", p.id);
                j.put("src", p.src);
                j.put("name", p.name);
                j.put("conn", p.conn);
                j.put("dns", p.dns);
                j.put("serial", p.serial);
                j.put("updated", p.updated);
                j.put("store", p.store.writeJSON());
                arr.put(j);
            }
            o.put("profiles", arr);
        } catch (Exception ignored) {
        }
        return o.toString();
    }

    Prof get(String id) {
        for (Prof p : profiles) {
            if (p.id.equals(id)) {
                return p;
            }
        }
        return null;
    }

    Prof current() {
        Prof p = get(active);
        return p != null ? p : get(MINE);
    }

    boolean operatorActive() {
        Prof p = current();
        return p != null && "op".equals(p.src);
    }

    static void syncActive(Context c, SplitStore store) {
        ProfileStore ps = load(c);
        Prof p = ps.current();
        if (p == null || "op".equals(p.src)) {
            return;
        }
        p.store = store.copy();
        p.updated = System.currentTimeMillis();
        ps.save(c);
    }

    void activate(String id, Context c) {
        Prof p = get(id);
        if (p == null) {
            return;
        }
        Prof cur = current();
        if (cur != null && !"op".equals(cur.src)) {
            cur.store = SplitStore.load(c).copy();
        }
        active = id;
        p.store.save(c);
        save(c);
    }

    Prof copyOf(Prof src, String name) {
        Prof n = new Prof();
        n.id = UUID.randomUUID().toString();
        n.src = "me";
        n.name = name;
        n.dns = src.dns;
        n.updated = System.currentTimeMillis();
        n.store = src.store.copy();
        for (SplitStore.Svc s : n.store.services) {
            if ("op".equals(s.src)) {
                s.src = "me";
            }
        }
        profiles.add(n);
        return n;
    }

    void copyAndActivate(Prof src, String name, Context c) {
        Prof n = copyOf(src, name);
        activate(n.id, c);
    }

    void putOperator(String json, String conn) {
        JSONObject o;
        try {
            o = new JSONObject(json);
        } catch (Exception e) {
            return;
        }
        Prof p = get(OP);
        if (p == null) {
            p = new Prof();
            p.id = OP;
            p.src = "op";
            profiles.add(p);
        }
        long serial = o.optLong("serial");
        p.conn = conn == null ? "" : conn;
        if (serial != 0 && p.serial == serial) {
            return;
        }
        p.name = o.optString("name");
        p.dns = o.optString("dns");
        p.serial = serial;
        p.updated = System.currentTimeMillis();
        p.store = SplitStore.fromJSON(json);
        p.src = "op";
    }

    static void applyOperator(Context c, String json, String conn) {
        if (json == null || json.isEmpty()) {
            return;
        }
        ProfileStore ps = load(c);
        ps.putOperator(json, conn);
        ps.save(c);
        if (OP.equals(ps.active)) {
            Prof p = ps.get(OP);
            if (p != null) {
                p.store.save(c);
            }
        }
    }

    Prof addLink(String json) {
        JSONObject o;
        try {
            o = new JSONObject(json);
        } catch (Exception e) {
            return null;
        }
        Prof p = new Prof();
        p.id = UUID.randomUUID().toString();
        p.src = "link";
        p.name = o.optString("name");
        p.dns = o.optString("dns");
        p.serial = o.optLong("serial");
        p.updated = System.currentTimeMillis();
        p.store = SplitStore.fromJSON(json);
        profiles.add(p);
        return p;
    }

    static Prof importLink(Context c, String json) {
        ProfileStore ps = load(c);
        Prof p = ps.addLink(json);
        if (p != null) {
            ps.save(c);
        }
        return p;
    }
}
