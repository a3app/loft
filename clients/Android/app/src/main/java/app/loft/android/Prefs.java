package app.loft.android;

import android.content.Context;
import android.content.SharedPreferences;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

final class Prefs {
    static final String DEFAULT_EXT_IP = "https://api.ipify.org";

    static SharedPreferences p(Context c) {
        return c.getApplicationContext().getSharedPreferences("loft.prefs", Context.MODE_PRIVATE);
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

    static boolean defaultRoute(Context c) {
        return p(c).getBoolean("default_route", true);
    }

    static boolean lanBypass(Context c) {
        return p(c).getBoolean("lan_bypass", true);
    }

    static boolean split(Context c) {
        return p(c).getBoolean("split", false);
    }

    static boolean showExtIP(Context c) {
        return p(c).getBoolean("ext_ip", true);
    }

    static boolean rateBits(Context c) {
        return p(c).getBoolean("rate_bits", true);
    }

    static String lang(Context c) {
        return p(c).getString("lang", "");
    }

    static String extIPURL(Context c) {
        String u = p(c).getString("ext_ip_url", DEFAULT_EXT_IP);
        return u == null || u.trim().isEmpty() ? DEFAULT_EXT_IP : u.trim();
    }

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

    static final class Route {
        String cidr = "";
        String via = "through"; // through | bypass | drop
    }
}
