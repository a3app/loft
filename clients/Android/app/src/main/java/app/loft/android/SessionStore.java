package app.loft.android;

import android.content.Context;
import android.content.SharedPreferences;

/**
 * Legacy single-connection storage. Older builds kept one session YAML in
 * SharedPreferences. Profiles reads it once on first start, turns it into
 * profiles, and then clears it, so this class only exists for that migration.
 */
final class SessionStore {
    private static final String PREF = "loft";
    private static final String YAML = "session.yaml";

    static String yaml(Context ctx) {
        return prefs(ctx).getString(YAML, "");
    }

    static void setYaml(Context ctx, String yaml) {
        prefs(ctx).edit().putString(YAML, yaml == null ? "" : yaml).apply();
    }

    private static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences(PREF, Context.MODE_PRIVATE);
    }
}
