/*
 * Profiles: the store of saved connections ("profiles", one per loft node or
 * other server the user added) and the active choice. Profile is the single
 * entry, YamlBuilder turns it into the session YAML the Go engine reads, and
 * Box encrypts the file at rest. Not to be confused with ProfileStore, which
 * holds split-tunnel route profiles.
 */
package app.loft.android;

import android.content.Context;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

/**
 * The saved connections and which one is active. Kept in files/profiles.json,
 * encrypted with a key from the Android keystore (see Box), because profiles
 * hold member keys and passwords. One instance per process (LoftApp).
 * Every change rebuilds the profile's YAML and saves the whole list.
 */
final class Profiles {
    /** File name under the app's private files dir. */
    private static final String FILE = "profiles.json";
    private final Context app;
    /** Saved connections in list order (the order shown on screen). */
    private final List<Profile> items = new CopyOnWriteArrayList<>();
    /** Id of the selected profile; empty when none has been chosen yet. */
    private String active = "";

    /** Loads the saved list right away; uses the application context so no Activity leaks. */
    Profiles(Context c) {
        app = c.getApplicationContext();
        load();
    }

    /** A copy of the list, safe to iterate while the store changes. */
    synchronized List<Profile> all() {
        return new ArrayList<>(items);
    }

    /** Profile with this id, or null. */
    synchronized Profile get(String id) {
        for (Profile p : items) {
            if (p.id.equals(id)) {
                return p;
            }
        }
        return null;
    }

    /** The active profile, or the first one when the active id is gone; null when there are none. */
    synchronized Profile active() {
        Profile p = get(active);
        if (p != null) {
            return p;
        }
        return items.isEmpty() ? null : items.get(0);
    }

    /** Id of active(), or "" when there are no profiles. */
    synchronized String activeId() {
        Profile p = active();
        return p == null ? "" : p.id;
    }

    /** Makes {@code id} the active profile and saves; unknown ids are ignored. */
    synchronized void setActive(String id) {
        if (get(id) != null) {
            active = id;
            save();
        }
    }

    /** Adds or replaces a profile by id. The first profile ever added becomes active. */
    synchronized void put(Profile p) {
        if (p == null) {
            return;
        }
        p.yaml = YamlBuilder.from(p);
        for (int i = 0; i < items.size(); i++) {
            if (items.get(i).id.equals(p.id)) {
                items.set(i, p);
                save();
                return;
            }
        }
        items.add(p);
        if (active.isEmpty()) {
            active = p.id;
        }
        save();
    }

    /**
     * Appends imported profiles (from a file, QR or TV pairing) and saves once.
     * Unlike put(), it does not replace existing ids. If nothing was active,
     * the first added profile becomes active.
     */
    synchronized void addAll(List<Profile> list) {
        for (Profile p : list) {
            if (p == null) {
                continue;
            }
            p.yaml = YamlBuilder.from(p);
            items.add(p);
            if (active.isEmpty()) {
                active = p.id;
            }
        }
        save();
    }

    /** Merges Status fronts= into the saved loft profile after handshake. */
    synchronized void applyLearnedFronts(String id, String raw) {
        Profile p = get(id);
        if (p == null || !p.applyLearnedFronts(raw)) {
            return;
        }
        p.yaml = YamlBuilder.from(p);
        save();
    }

    /** Stores the node mask_pub learned after handshake so the next Start passes mask_pub. */
    synchronized void applyLearnedMask(String id, String hex) {
        if (hex == null) {
            return;
        }
        String h = hex.trim().toLowerCase();
        if (h.length() != 64) {
            return;
        }
        for (int i = 0; i < 64; i++) {
            char c = h.charAt(i);
            if ((c < '0' || c > '9') && (c < 'a' || c > 'f')) {
                return;
            }
        }
        Profile p = get(id);
        if (p == null || !p.isLoft()) {
            return;
        }
        String cur = p.maskPub == null ? "" : p.maskPub.toLowerCase();
        if (h.equals(cur)) {
            return;
        }
        p.maskPub = h;
        p.yaml = YamlBuilder.from(p);
        save();
    }

    /** Removes a profile; if it was active, the first remaining one takes over. */
    synchronized void delete(String id) {
        for (int i = 0; i < items.size(); i++) {
            if (items.get(i).id.equals(id)) {
                items.remove(i);
                break;
            }
        }
        if (id.equals(active)) {
            active = items.isEmpty() ? "" : items.get(0).id;
        }
        save();
    }

    /** loft:// / YAML docs joined for TV pairing. */
    synchronized String shareAll() {
        StringBuilder b = new StringBuilder();
        for (Profile p : items) {
            String y = YamlBuilder.emit(p);
            if (y == null || y.trim().isEmpty()) {
                y = p.sessionYaml();
            }
            if (y == null || y.trim().isEmpty()) {
                continue;
            }
            if (b.length() > 0) {
                b.append("\n---\n");
            }
            b.append(y.trim());
        }
        return b.toString();
    }

    /** One profile as its session YAML, for sharing a single connection. */
    synchronized String exportOne(Profile p) {
        if (p == null) {
            return "";
        }
        return p.sessionYaml();
    }

    /** All profiles as one YAML file with a "connections:" list, for backup. */
    synchronized String exportAll() {
        StringBuilder b = new StringBuilder("connections:\n");
        for (Profile p : items) {
            String y = p.sessionYaml();
            boolean first = true;
            for (String line : y.split("\n", -1)) {
                if (first) {
                    if (line.isEmpty()) {
                        continue;
                    }
                    b.append("  - ").append(line).append('\n');
                    first = false;
                } else if (line.isEmpty()) {
                    b.append('\n');
                } else {
                    b.append("    ").append(line).append('\n');
                }
            }
            b.append('\n');
        }
        return b.toString();
    }

    /** Reads and decrypts profiles.json; on first run migrates the legacy single YAML instead. */
    private void load() {
        items.clear();
        active = "";
        File f = new File(app.getFilesDir(), FILE);
        if (!f.exists()) {
            migrateYaml();
            return;
        }
        try {
            byte[] raw = read(f);
            String json = Box.open(new String(raw, StandardCharsets.UTF_8));
            JSONObject o = new JSONObject(json);
            active = o.optString("active");
            JSONArray a = o.optJSONArray("items");
            if (a != null) {
                for (int i = 0; i < a.length(); i++) {
                    items.add(Profile.fromJson(a.getJSONObject(i)));
                }
            }
        } catch (Exception ignored) {
        }
        if (active.isEmpty() && !items.isEmpty()) {
            active = items.get(0).id;
        }
    }

    /** One-time move of the old single-session YAML (SessionStore) into the profile list. */
    private void migrateYaml() {
        String y = SessionStore.yaml(app);
        if (y == null || y.trim().isEmpty()) {
            return;
        }
        try {
            List<Profile> got = Importer.parse(y);
            if (!got.isEmpty()) {
                items.addAll(got);
                active = items.get(0).id;
                save();
                SessionStore.setYaml(app, "");
            }
        } catch (Exception ignored) {
        }
    }

    /** Encrypts the whole list with Box and rewrites profiles.json. Errors are ignored. */
    private void save() {
        try {
            JSONObject o = new JSONObject();
            o.put("active", active);
            JSONArray a = new JSONArray();
            for (Profile p : items) {
                a.put(p.toJson());
            }
            o.put("items", a);
            write(new File(app.getFilesDir(), FILE), Box.seal(o.toString()).getBytes(StandardCharsets.UTF_8));
        } catch (Exception ignored) {
        }
    }

    /** Reads a whole file into memory. */
    private static byte[] read(File f) throws Exception {
        java.io.FileInputStream in = new java.io.FileInputStream(f);
        try {
            byte[] b = new byte[(int) f.length()];
            int n = 0;
            while (n < b.length) {
                int r = in.read(b, n, b.length - n);
                if (r < 0) {
                    break;
                }
                n += r;
            }
            return b;
        } finally {
            in.close();
        }
    }

    /** Replaces the file's content with {@code b}. */
    private static void write(File f, byte[] b) throws Exception {
        java.io.FileOutputStream out = new java.io.FileOutputStream(f);
        try {
            out.write(b);
        } finally {
            out.close();
        }
    }
}
