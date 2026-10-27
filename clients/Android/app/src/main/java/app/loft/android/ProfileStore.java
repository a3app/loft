/*
 * ProfileStore: saved split-tunnel route profiles. A route profile is a full
 * SplitStore (which services and zones go through the tunnel and which stay
 * local). The phone's Lists screen switches between them; LoftVpnService
 * stores the profile a node operator pushes. Not related to Profiles, which
 * holds connections.
 */
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
    /** Fixed id of the user's own profile (created from the original split_store). */
    static final String MINE = "mine";
    /** Fixed id of the single operator slot, filled by the node the user connects to. */
    static final String OP = "op";

    /** Id of the active profile. */
    String active = MINE;
    final List<Prof> profiles = new ArrayList<>();

    /** One route profile with its origin and its full split-tunnel settings. */
    static final class Prof {
        String id = "";
        /** Where it came from: "me" (user-made), "op" (pushed by the node operator) or "link" (imported). */
        String src = "me"; // me | op | link
        String name = "";
        /** For the operator profile: name of the connection it arrived over. */
        String conn = "";
        /** DNS setting carried by the profile. */
        String dns = "";
        /** Version number set by the profile's author; an operator profile is replaced only when it changes. */
        long serial;
        /** Last change time, epoch milliseconds. */
        long updated;
        SplitStore store = new SplitStore();
    }

    /**
     * Reads the catalog from prefs. On first run it creates it with a single
     * "mine" profile copied from the current split_store, so existing settings
     * are kept. Falls back to a valid active id if the saved one is gone.
     */
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

    /** Writes the catalog (not the active split_store) to prefs. */
    void save(Context c) {
        Prefs.p(c).edit().putString("route_profiles", toJSON()).apply();
    }

    /**
     * Builds a catalog from its JSON form. Profiles without an id are dropped;
     * a broken document yields an empty catalog rather than an error.
     */
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

    /** JSON form of the catalog, the inverse of parse(). */
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

    /** Profile with this id, or null. */
    Prof get(String id) {
        for (Prof p : profiles) {
            if (p.id.equals(id)) {
                return p;
            }
        }
        return null;
    }

    /** The active profile, falling back to "mine". */
    Prof current() {
        Prof p = get(active);
        return p != null ? p : get(MINE);
    }

    /** True when the operator's profile is selected; the UI then treats the lists as read-only. */
    boolean operatorActive() {
        Prof p = current();
        return p != null && "op".equals(p.src);
    }

    /**
     * Copies edits made on the Lists screen back into the active catalog entry.
     * Skipped for the operator profile, since the node owns it.
     */
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

    /**
     * Switches to profile {@code id}: first saves the current split_store into
     * the outgoing profile (unless it is the operator's), then writes the new
     * profile's settings into split_store so RoutePlan picks them up.
     */
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

    /**
     * Adds an editable "me" copy of {@code src} under a new id. Services marked
     * as coming from the operator are re-marked as the user's own, so they can
     * be edited in the copy.
     */
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

    /** copyOf() followed by activate(); how a read-only profile becomes editable. */
    void copyAndActivate(Prof src, String name, Context c) {
        Prof n = copyOf(src, name);
        activate(n.id, c);
    }

    /**
     * Creates or refreshes the operator slot from the node's profile JSON.
     * The connection name is always updated; the content is replaced only when
     * the serial differs (or is 0), so an unchanged push is a no-op.
     */
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

    /**
     * Entry point for LoftVpnService after a connection comes up: stores the
     * operator profile and, if it is the active one, also refreshes
     * split_store so the new routes take effect.
     */
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

    /** Adds a profile parsed from a shared routes link; null if the JSON is invalid. */
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

    /** Loads the catalog, adds a link profile and saves it. Does not activate it. */
    static Prof importLink(Context c, String json) {
        ProfileStore ps = load(c);
        Prof p = ps.addLink(json);
        if (p != null) {
            ps.save(c);
        }
        return p;
    }
}
