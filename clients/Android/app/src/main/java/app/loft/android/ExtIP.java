package app.loft.android;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.URL;
import java.nio.charset.StandardCharsets;

/** Echo-API lookup through the VPN, same parse as macOS ExtIP. */
final class ExtIP {
    private static final String[] JSON_KEYS = {
            "ip", "query", "origin", "address", "ip_addr", "externalIp", "external_ip"
    };

    /**
     * Asks an echo service ("what is my IP") for the public address and returns
     * it, or "" on any failure. The request is bound to the VPN network when one
     * is up, so the answer is the address the internet sees through the tunnel,
     * not the phone's own. Blocking; call it off the main thread.
     */
    static String lookup(Context ctx, String raw) {
        if (raw == null) {
            return "";
        }
        String src = raw.trim();
        if (src.isEmpty() || (!src.startsWith("http://") && !src.startsWith("https://"))) {
            return "";
        }
        HttpURLConnection c = null;
        try {
            URL u = new URL(src);
            Network vpn = vpn(ctx);
            c = (HttpURLConnection) (vpn != null ? vpn.openConnection(u) : u.openConnection());
            c.setConnectTimeout(8000);
            c.setReadTimeout(8000);
            c.setInstanceFollowRedirects(true);
            c.setRequestProperty("User-Agent", "loft");
            c.setRequestProperty("Connection", "close");
            c.setUseCaches(false);
            int code = c.getResponseCode();
            if (code < 200 || code >= 300) {
                return "";
            }
            byte[] body = read(c.getInputStream(), 4096);
            return parse(body);
        } catch (Exception e) {
            return "";
        } finally {
            if (c != null) {
                c.disconnect();
            }
        }
    }

    /**
     * Extracts an IP from an echo service reply. Services differ, so it tries a
     * bare address, then common JSON keys (the first item of a comma list, as
     * in X-Forwarded-For style values), then the first IPv4-looking token.
     */
    static String parse(byte[] data) {
        if (data == null || data.length == 0) {
            return "";
        }
        String s;
        try {
            s = new String(data, StandardCharsets.UTF_8);
        } catch (Exception e) {
            s = new String(data, StandardCharsets.US_ASCII);
        }
        String t = s.trim();
        String ip = asIP(t);
        if (ip != null) {
            return ip;
        }
        if (t.startsWith("{")) {
            try {
                JSONObject o = new JSONObject(t);
                for (String k : JSON_KEYS) {
                    String v = o.optString(k, "");
                    int comma = v.indexOf(',');
                    if (comma >= 0) {
                        v = v.substring(0, comma);
                    }
                    ip = asIP(v.trim());
                    if (ip != null) {
                        return ip;
                    }
                }
            } catch (Exception ignored) {
            }
        }
        java.util.regex.Matcher m = java.util.regex.Pattern.compile("\\b\\d{1,3}(?:\\.\\d{1,3}){3}\\b").matcher(t);
        if (m.find()) {
            ip = asIP(m.group());
            if (ip != null) {
                return ip;
            }
        }
        return "";
    }

    static String parse(String body) {
        return parse(body == null ? new byte[0] : body.getBytes(StandardCharsets.UTF_8));
    }

    /** Returns {@code s} when it is a literal IPv4 or IPv6 address, else null. */
    static String asIP(String s) {
        if (s == null) {
            return null;
        }
        String t = s.trim();
        if (t.isEmpty()) {
            return null;
        }
        if (v4(t)) {
            return t;
        }
        if (t.indexOf(':') < 0) {
            return null;
        }
        try {
            byte[] a = InetAddress.getByName(t).getAddress();
            return a.length == 16 ? t : null;
        } catch (Exception e) {
            return null;
        }
    }

    private static boolean v4(String t) {
        String[] p = t.split("\\.", -1);
        if (p.length != 4) {
            return false;
        }
        for (String x : p) {
            if (x.isEmpty() || x.length() > 3) {
                return false;
            }
            int n = 0;
            for (int i = 0; i < x.length(); i++) {
                char c = x.charAt(i);
                if (c < '0' || c > '9') {
                    return false;
                }
                n = n * 10 + (c - '0');
            }
            if (n > 255) {
                return false;
            }
        }
        return true;
    }

    // The first network with VPN transport, i.e. our tunnel; null when none is up.
    private static Network vpn(Context ctx) {
        if (ctx == null) {
            return null;
        }
        ConnectivityManager cm = (ConnectivityManager) ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
        if (cm == null) {
            return null;
        }
        Network[] nets = cm.getAllNetworks();
        if (nets == null) {
            return null;
        }
        for (Network n : nets) {
            NetworkCapabilities cap = cm.getNetworkCapabilities(n);
            if (cap != null && cap.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) {
                return n;
            }
        }
        return null;
    }

    private static byte[] read(InputStream in, int cap) throws Exception {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        byte[] b = new byte[512];
        int n;
        int total = 0;
        while (total < cap && (n = in.read(b, 0, Math.min(b.length, cap - total))) > 0) {
            o.write(b, 0, n);
            total += n;
        }
        return o.toByteArray();
    }
}
