package app.loft.android;

import android.content.Context;
import android.content.SharedPreferences;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

/**
 * App settings in SharedPreferences "loft.prefs": UI options, routing
 * switches and the user's custom routes. Profiles live elsewhere
 * (Profiles, encrypted file).
 */
final class Prefs {
    // Service that echoes the caller's public IP; used to show the address
    // the internet sees through the tunnel.
    static final String DEFAULT_EXT_IP = "https://api.ipify.org";

    static SharedPreferences p(Context c) {
        Context app = c.getApplicationContext();
        return (app != null ? app : c).getSharedPreferences("loft.prefs", Context.MODE_PRIVATE);
    }

    static boolean simple(Context c) {
        return p(c).getBoolean("simple", false);
    }

    static boolean autoConnect(Context c) {
        return p(c).getBoolean("auto", false);
    }

    static boolean reconnect(Context c) {
        return p(c).getBoolean("reconnect", true);
    }

    /** Send all traffic through the tunnel, not only the overlay network. */
    static boolean defaultRoute(Context c) {
        return p(c).getBoolean("default_route", true);
    }

    /** Keep private and link-local ranges off the tunnel so the local network stays reachable. */
    static boolean lanBypass(Context c) {
        return p(c).getBoolean("lan_bypass", true);
    }

    /** Apply the user's domain/subnet lists (SplitStore) on top of the default route. */
    static boolean split(Context c) {
        return p(c).getBoolean("split", false);
    }

    static boolean showExtIP(Context c) {
        return p(c).getBoolean("ext_ip", true);
    }

    static boolean rateBits(Context c) {
        return p(c).getBoolean("rate_bits", true);
    }

    static boolean verbose(Context c) {
        return p(c).getBoolean("verbose", false);
    }

    static boolean sparkBars(Context c) {
        return p(c).getBoolean("spark_bars", false);
    }

    /** Resets all settings and restores the built-in split lists. */
    static void defaults(Context c) {
        p(c).edit().clear().apply();
        SplitStore st = new SplitStore();
        st.seed();
        st.save(c);
    }

    static String lang(Context c) {
        return p(c).getString("lang", "");
    }

    static String extIPURL(Context c) {
        String u = p(c).getString("ext_ip_url", DEFAULT_EXT_IP);
        return u == null || u.trim().isEmpty() ? DEFAULT_EXT_IP : u.trim();
    }

    /** User routes from the Routes screen, stored as a JSON array; invalid CIDRs are skipped. */
    static List<Route> routes(Context c) {
        List<Route> out = new ArrayList<>();
        String raw = p(c).getString("routes", "[]");
        try {
            JSONArray a = new JSONArray(raw);
            for (int i = 0; i < a.length(); i++) {
                JSONObject o = a.getJSONObject(i);
                Route r = new Route();
                r.cidr = o.optString("cidr");
                r.via = o.optString("via", "through");
                if (Cidr.parse(r.cidr) != null) {
                    out.add(r);
                }
            }
        } catch (Exception ignored) {
        }
        return out;
    }

    static void setRoutes(Context c, List<Route> list) {
        JSONArray a = new JSONArray();
        try {
            for (Route r : list) {
                JSONObject o = new JSONObject();
                o.put("cidr", r.cidr);
                o.put("via", r.via);
                a.put(o);
            }
        } catch (Exception ignored) {
        }
        p(c).edit().putString("routes", a.toString()).apply();
    }

    static String applyLang(Context c) {
        String lang = lang(c);
        if (lang == null || lang.isEmpty()) {
            return Locale.getDefault().getLanguage();
        }
        return lang;
    }

    /** One user route: a prefix and what to do with it. */
    static final class Route {
        String cidr = "";
        String via = "through"; // through | bypass | drop
    }
}
