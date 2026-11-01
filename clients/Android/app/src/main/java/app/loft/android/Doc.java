/*
 * Android client, shared code (phone and TV builds): the big JSON documents
 * of split routing, each in a file of its own with a copy in memory.
 * SplitStore keeps the active lists in "split_store"; ProfileStore keeps the
 * catalog of route profiles in "route_profiles".
 */
package app.loft.android;

import android.content.Context;
import android.content.SharedPreferences;

import java.io.File;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;

/**
 * Named text documents in files/docs/NAME.json, cached in memory.
 *
 * <p><b>Why not SharedPreferences.</b> These documents used to live in
 * loft.prefs next to every small setting, and they can be megabytes (one
 * downloaded list alone may be 2 MB). SharedPreferences keeps one XML file:
 * every apply() of any tiny setting rewrote all of it, and Android makes the
 * main thread wait for pending apply() writes whenever a screen pauses or a
 * service starts. With megabytes queued that wait became a visible freeze.
 * Here each document has its own file, writes run on loft-disk (see Disk),
 * and saving an unchanged document does nothing at all.
 *
 * <p><b>Migration.</b> On the first read, a document still in loft.prefs is
 * moved into its file and the old preference key is removed.
 *
 * <p><b>Change listeners.</b> SharedPreferences used to tell LoftVpnService
 * that "split_store" changed; {@link #watch} replaces that. Listeners run on
 * the thread that called put(), right after the memory copy changed.
 */
final class Doc {
    /** Name of the active split lists document. */
    static final String SPLIT = "split_store";
    /** Name of the route profile catalog document. */
    static final String PROFILES = "route_profiles";

    /** Told when a document changed. */
    interface Listener {
        /** Called on the thread that called put(), after the new text is visible to get(). */
        void docChanged(String name);
    }

    /** Memory copy of every document read or written in this process. Guarded by itself. */
    private static final Map<String, String> CACHE = new HashMap<>();
    /** Current listeners; copy-on-write so put() can iterate while they change. */
    private static final List<Listener> LISTENERS = new CopyOnWriteArrayList<>();

    private Doc() {}

    /**
     * The document's text, or "" when there is none. Reads the file (or
     * migrates the old preference) only on the first call per process;
     * after that it is a map lookup.
     */
    static String get(Context c, String name) {
        synchronized (CACHE) {
            String v = CACHE.get(name);
            if (v != null) {
                return v;
            }
            v = loadLocked(c, name);
            CACHE.put(name, v);
            return v;
        }
    }

    /**
     * Replaces the document. A text equal to the current one changes nothing:
     * no write and no listener call. Otherwise the memory copy changes at
     * once, the file is written in the background, and listeners are told.
     */
    static void put(Context c, String name, String text) {
        String v = text == null ? "" : text;
        File f = file(c, name);
        synchronized (CACHE) {
            String cur = CACHE.get(name);
            if (cur == null) {
                cur = loadLocked(c, name);
            }
            if (cur.equals(v)) {
                CACHE.put(name, cur);
                return;
            }
            CACHE.put(name, v);
        }
        // The newest put() wins: Disk re-reads the memory copy when it writes.
        Disk.writeLater(f, () -> {
            String now;
            synchronized (CACHE) {
                now = CACHE.get(name);
            }
            return now == null ? null : now.getBytes(StandardCharsets.UTF_8);
        });
        for (Listener l : LISTENERS) {
            l.docChanged(name);
        }
    }

    /** Subscribes a listener; adding the same one twice has no extra effect. */
    static void watch(Listener l) {
        if (l != null && !LISTENERS.contains(l)) {
            LISTENERS.add(l);
        }
    }

    /** Unsubscribes a listener. */
    static void drop(Listener l) {
        LISTENERS.remove(l);
    }

    /** files/docs/NAME.json. */
    private static File file(Context c, String name) {
        return new File(new File(c.getApplicationContext().getFilesDir(), "docs"), name + ".json");
    }

    /**
     * Reads the file; when there is none, moves the old preference value into
     * a new file (written right away, so the preference can be removed
     * safely). Caller holds the CACHE lock.
     */
    private static String loadLocked(Context c, String name) {
        File f = file(c, name);
        try {
            byte[] b = Disk.read(f);
            if (b != null) {
                return new String(b, StandardCharsets.UTF_8);
            }
        } catch (Exception e) {
            Journal.add("doc " + name + " " + e.getMessage());
            return "";
        }
        SharedPreferences sp = Prefs.p(c);
        String old = sp.getString(name, "");
        if (old == null || old.isEmpty()) {
            return "";
        }
        try {
            Disk.writeNow(f, old.getBytes(StandardCharsets.UTF_8));
            sp.edit().remove(name).apply();
        } catch (Exception e) {
            // Keep the preference: the next start tries the move again.
            Journal.add("doc move " + name + " " + e.getMessage());
        }
        return old;
    }
}
