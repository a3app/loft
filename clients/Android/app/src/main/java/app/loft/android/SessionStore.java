package app.loft.android;

import android.content.Context;
import android.content.SharedPreferences;

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
