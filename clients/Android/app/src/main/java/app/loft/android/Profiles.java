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
    private static final String FILE = "profiles.json";
    private final Context app;
    private final List<Profile> items = new CopyOnWriteArrayList<>();
    private String active = "";

    Profiles(Context c) {
        app = c.getApplicationContext();
        load();
    }

    synchronized List<Profile> all() {
        return new ArrayList<>(items);
    }

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

    synchronized String activeId() {
        Profile p = active();
        return p == null ? "" : p.id;
    }

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

    private static void write(File f, byte[] b) throws Exception {
        java.io.FileOutputStream out = new java.io.FileOutputStream(f);
        try {
            out.write(b);
        } finally {
            out.close();
        }
    }
}
