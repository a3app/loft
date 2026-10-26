package app.loft.android;

import android.content.Context;
import android.os.Handler;
import android.os.Looper;

import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.net.URL;
import java.nio.charset.StandardCharsets;

/**
 * LAN pairing: TV listens on http://ip:port/pair?c=CODE, phone POSTs YAML.
 */
final class PairUrl {
    static final String PATH = "/pair";

    /** True when a scanned string is a TV pairing URL (path /pair with a c= code). */
    static boolean isPair(String raw) {
        URI u = parse(raw);
        if (u == null) {
            return false;
        }
        String p = u.getPath();
        return PATH.equals(p) && query(u, "c") != null;
    }

    static String code(String raw) {
        URI u = parse(raw);
        return u == null ? "" : nz(query(u, "c"));
    }

    static String build(String host, int port, String code) {
        return "http://" + host + ":" + port + PATH + "?c=" + code;
    }

    /** POST body to the TV listener. Callback on the main thread. */
    static void send(Context c, String pair, String body, Callback cb) {
        Handler h = new Handler(Looper.getMainLooper());
        new Thread(() -> {
            try {
                URL u = new URL(pair.trim());
                HttpURLConnection conn = (HttpURLConnection) u.openConnection();
                conn.setConnectTimeout(4000);
                conn.setReadTimeout(4000);
                conn.setRequestMethod("POST");
                conn.setDoOutput(true);
                conn.setRequestProperty("Content-Type", "text/plain; charset=utf-8");
                byte[] raw = body.getBytes(StandardCharsets.UTF_8);
                conn.setFixedLengthStreamingMode(raw.length);
                try (OutputStream out = conn.getOutputStream()) {
                    out.write(raw);
                }
                int code = conn.getResponseCode();
                conn.disconnect();
                if (code >= 200 && code < 300) {
                    h.post(() -> cb.done(true, null));
                } else {
                    h.post(() -> cb.done(false, "HTTP " + code));
                }
            } catch (Exception e) {
                h.post(() -> cb.done(false, e.getMessage()));
            }
        }, "loft-pair").start();
    }

    private static URI parse(String raw) {
        if (raw == null) {
            return null;
        }
        String s = raw.trim();
        if (!s.startsWith("http://") && !s.startsWith("https://")) {
            return null;
        }
        try {
            return URI.create(s);
        } catch (Exception e) {
            return null;
        }
    }

    private static String query(URI u, String key) {
        String q = u.getQuery();
        if (q == null || q.isEmpty()) {
            return null;
        }
        for (String part : q.split("&")) {
            int eq = part.indexOf('=');
            String k = eq < 0 ? part : part.substring(0, eq);
            if (key.equals(k)) {
                return eq < 0 ? "" : part.substring(eq + 1);
            }
        }
        return null;
    }

    private static String nz(String s) {
        return s == null ? "" : s;
    }

    interface Callback {
        void done(boolean ok, String err);
    }
}
