/*
 * Android client, shared code: old storage for a single connection, kept
 * only so profiles saved by earlier app versions are not lost on upgrade.
 * Profiles (the current multi-connection store) is its only user.
 */
package app.loft.android;

import android.content.Context;
import android.content.SharedPreferences;

/**
 * Legacy single-connection storage. Older builds kept one session YAML in
 * SharedPreferences. Profiles reads it once on first start, turns it into
 * profiles, and then clears it, so this class only exists for that migration.
 */
final class SessionStore {
    /** Name of the old SharedPreferences file. */
    private static final String PREF = "loft";
    /** Key under which the old builds saved the session YAML. */
    private static final String YAML = "session.yaml";

    /** The old saved session YAML, or "" when there is none (or it was already migrated). */
    static String yaml(Context ctx) {
        return prefs(ctx).getString(YAML, "");
    }

    /** Overwrites the old session YAML; Profiles passes "" to clear it after migration. */
    static void setYaml(Context ctx, String yaml) {
        prefs(ctx).edit().putString(YAML, yaml == null ? "" : yaml).apply();
    }

    /** Opens the old private preferences file. */
    private static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences(PREF, Context.MODE_PRIVATE);
    }
}
