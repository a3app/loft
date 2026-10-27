/*
 * Android client, shared code: typed getters (and a few setters) for the
 * app settings. The phone and TV settings screens write most keys directly
 * through p(); LoftVpnService and RoutePlan read them when a session starts.
 */
package app.loft.android;

import android.content.Context;
import android.content.SharedPreferences;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;

/**
 * App settings in SharedPreferences "loft.prefs": UI options, routing
 * switches and the user's custom routes. Profiles live elsewhere
 * (Profiles, encrypted file).
 */
final class Prefs {
    // Service that echoes the caller's public IP; used to show the address
    // the internet sees through the tunnel.
    static final String DEFAULT_EXT_IP = "https://api.ipify.org";

    /** The settings file; always opened through the application context. */
    static SharedPreferences p(Context c) {
        Context app = c.getApplicationContext();
        return (app != null ? app : c).getSharedPreferences("loft.prefs", Context.MODE_PRIVATE);
    }

    /** Simplified home screen: hides the detail cards, speed graph and address rows. */
    static boolean simple(Context c) {
        return p(c).getBoolean("simple", false);
    }

    /** Connect the active profile automatically when the app opens. */
    static boolean autoConnect(Context c) {
        return p(c).getBoolean("auto", false);
    }

    /** Retry automatically after the session drops (LoftVpnService gives up after a few tries). */
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

    /** Show the public IP seen through the tunnel (looked up via extIPURL). */
    static boolean showExtIP(Context c) {
        return p(c).getBoolean("ext_ip", true);
    }

    /** Show speeds in bits per second rather than bytes. */
    static boolean rateBits(Context c) {
        return p(c).getBoolean("rate_bits", true);
    }

    /** The "verbose log" switch. Settings screens store it; nothing in shared code reads it yet. */
    static boolean verbose(Context c) {
        return p(c).getBoolean("verbose", false);
    }

    /** Draw the speed graph as bars instead of a line. */
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

    /** Chosen UI language tag (see Lang); "" means follow the system. */
    static String lang(Context c) {
        return p(c).getString("lang", "");
    }

    /** URL of the public-IP echo service; the default when unset or blank. */
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

    /** Saves the user routes as a JSON array of {cidr, via}. */
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

    /** sys | ip | doh. DoH is stored; VpnService.Builder still takes an address. */
    static String dnsMode(Context c) {
        String m = p(c).getString("local_dns", "sys");
        return "ip".equals(m) || "doh".equals(m) ? m : "sys";
    }

    /** Custom DNS address (or DoH URL) for the "ip" / "doh" modes; "" when unset. */
    static String dnsAddr(Context c) {
        String u = p(c).getString("local_dns_addr", "");
        return u == null ? "" : u.trim();
    }

    /** all | except | only */
    /**
     * Which apps use the VPN: "all", "except" (all but the listed apps) or
     * "only" (just the listed apps). Unknown values read as "all".
     */
    static String appMode(Context c) {
        String m = p(c).getString("app_mode", "all");
        return "except".equals(m) || "only".equals(m) ? m : "all";
    }

    /** Package names listed for the given app mode ("except" or "only"), in saved order. */
    static Set<String> appSet(Context c, String mode) {
        Set<String> out = new LinkedHashSet<>();
        String key = "except".equals(mode) ? "app_except" : "app_only";
        String raw = p(c).getString(key, "[]");
        try {
            JSONArray a = new JSONArray(raw);
            for (int i = 0; i < a.length(); i++) {
                String pkg = a.optString(i).trim();
                if (!pkg.isEmpty()) {
                    out.add(pkg);
                }
            }
        } catch (Exception ignored) {
        }
        return out;
    }

    /** Saves the package list for the given app mode. */
    static void setAppSet(Context c, String mode, Set<String> pkgs) {
        JSONArray a = new JSONArray();
        if (pkgs != null) {
            for (String p : pkgs) {
                a.put(p);
            }
        }
        String key = "except".equals(mode) ? "app_except" : "app_only";
        p(c).edit().putString(key, a.toString()).apply();
    }

    /** The language in effect: the stored choice, else the device default. Used to pick localized catalog labels. */
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
