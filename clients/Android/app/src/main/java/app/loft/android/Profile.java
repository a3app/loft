package app.loft.android;

import org.json.JSONException;
import org.json.JSONObject;

import java.util.UUID;

/** One named connection. YAML is what Engine.Start consumes. */
final class Profile {
    static final String[] FRONTS = {"get", "ws", "h2", "quic", "h3", "l3"};
    static final String[] FRONT_TITLES = {"GET", "WS", "H2", "QUIC", "H3", "L3"};
    static final String[] MUX_ORDER = {"get", "ws", "h2", "quic", "h3"};
    static final String DEF_GET = "/static/v3/build";
    static final String DEF_WS = "/api/v1/sync";
    static final String DEF_H2 = "/api/v1/tunnel";

    String id = UUID.randomUUID().toString();
    String name = "";
    String kind = "get";
    String token = "";
    String url = "";
    String path = "";
    String cdn = "";
    String edge = "";
    String sni = "";
    String user = "";
    String pass = "";
    String dns = "";
    String maskPub = "";
    String inner = "";
    String wsPath = "";
    String h2Path = "";
    String h3Path = "";
    String listedFronts = "";
    String fp = "chrome";
    boolean quic;
    String yaml = "";
    boolean mux = true;
    boolean tlsCheck = true;
    boolean pap;
    boolean dnsSystem = true;

    boolean isLoft() {
        return indexOf(kind) >= 0;
    }

    boolean isL3() {
        return "l3".equalsIgnoreCase(kind);
    }

    boolean isSSTP() {
        return "sstp".equalsIgnoreCase(kind);
    }

    boolean isVLESS() {
        return "vless".equalsIgnoreCase(kind);
    }

    boolean isTrojan() {
        return "trojan".equalsIgnoreCase(kind);
    }

    /** Cover host without a separate edge: extra mux fronts stay off. */
    boolean isCDN() {
        if (edge != null && !edge.trim().isEmpty()) {
            return false;
        }
        if (cdn != null && !cdn.trim().isEmpty()) {
            return true;
        }
        String h = host().toLowerCase();
        return h.contains("cdn.");
    }

    boolean hasMuxExtras() {
        return !nz(wsPath).isEmpty() || !nz(h2Path).isEmpty() || !nz(h3Path).isEmpty() || quic;
    }

    /** Hello list from Edit extras. GET-only stays empty so Status can still teach fronts. */
    void syncListedFronts() {
        String k = kind == null ? "get" : kind.toLowerCase();
        if (isSSTP() || isVLESS() || isTrojan() || "l3".equals(k)) {
            listedFronts = "";
            return;
        }
        java.util.ArrayList<String> listed = new java.util.ArrayList<>();
        if (k.isEmpty() || "get".equals(k) || "auto".equals(k)) {
            listed.add("get");
            if (!nz(wsPath).isEmpty()) {
                listed.add("ws");
            }
            if (!nz(h2Path).isEmpty()) {
                listed.add("h2");
            }
            if (quic) {
                listed.add("quic");
            }
            if (!nz(h3Path).isEmpty()) {
                listed.add("h3");
            }
            kind = "get";
        } else if (muxName(k)) {
            listed.add(k);
        }
        if (listed.size() <= 1) {
            listedFronts = "";
            return;
        }
        StringBuilder b = new StringBuilder();
        for (int i = 0; i < listed.size(); i++) {
            if (i > 0) {
                b.append(',');
            }
            b.append(listed.get(i));
        }
        listedFronts = b.toString();
    }

    String[] muxFronts() {
        String k = kind == null ? "get" : kind.toLowerCase();
        if (isSSTP() || isVLESS() || isTrojan() || "l3".equals(k)) {
            return new String[]{k};
        }
        if (listedFronts != null && !listedFronts.trim().isEmpty()) {
            java.util.ArrayList<String> out = new java.util.ArrayList<>();
            java.util.HashSet<String> seen = new java.util.HashSet<>();
            for (String f : listedFronts.split(",")) {
                String t = f.trim().toLowerCase();
                if (muxName(t) && seen.add(t)) {
                    out.add(t);
                }
            }
            if (!out.isEmpty()) {
                return out.toArray(new String[0]);
            }
        }
        if (isCDN()) {
            return new String[]{"get"};
        }
        if (hasMuxExtras()) {
            java.util.ArrayList<String> out = new java.util.ArrayList<>();
            out.add("get");
            if (!nz(wsPath).isEmpty()) {
                out.add("ws");
            }
            if (!nz(h2Path).isEmpty()) {
                out.add("h2");
            }
            if (quic) {
                out.add("quic");
            }
            if (!nz(h3Path).isEmpty()) {
                out.add("h3");
            }
            return out.toArray(new String[0]);
        }
        if (k.isEmpty() || "get".equals(k)) {
            return new String[]{"get"};
        }
        return new String[]{k};
    }

    void applyFrontList(String raw) {
        if (raw == null) {
            return;
        }
        java.util.ArrayList<String> listed = new java.util.ArrayList<>();
        java.util.HashSet<String> seen = new java.util.HashSet<>();
        String s = raw.trim();
        if (s.startsWith("[") && s.endsWith("]")) {
            s = s.substring(1, s.length() - 1);
        }
        for (String part : s.split(",")) {
            String t = part.trim().replace("\"", "").replace("'", "").toLowerCase();
            if (!muxName(t) || !seen.add(t)) {
                continue;
            }
            listed.add(t);
            switch (t) {
                case "get":
                    if (!"get".equalsIgnoreCase(kind)) {
                        kind = "get";
                    }
                    break;
                case "ws":
                    if (nz(wsPath).isEmpty()) {
                        wsPath = DEF_WS;
                    }
                    break;
                case "h2":
                    if (nz(h2Path).isEmpty()) {
                        h2Path = DEF_H2;
                    }
                    break;
                case "h3":
                    if (nz(h3Path).isEmpty()) {
                        h3Path = DEF_H2;
                    }
                    break;
                case "quic":
                    quic = true;
                    break;
                default:
                    break;
            }
        }
        if (listed.isEmpty()) {
            listedFronts = "";
        } else {
            StringBuilder b = new StringBuilder();
            for (int i = 0; i < listed.size(); i++) {
                if (i > 0) {
                    b.append(',');
                }
                b.append(listed.get(i));
            }
            listedFronts = b.toString();
        }
    }

    boolean applyLearnedFronts(String raw) {
        if (raw == null || isSSTP() || isVLESS() || isTrojan() || isL3() || isCDN()) {
            return false;
        }
        boolean changed = false;
        for (String part : raw.split(",")) {
            String s = part.trim();
            if (s.isEmpty()) {
                continue;
            }
            String name;
            String path;
            int eq = s.indexOf('=');
            if (eq >= 0) {
                name = s.substring(0, eq);
                path = s.substring(eq + 1);
            } else {
                name = s;
                path = "";
            }
            switch (name) {
                case "get":
                    if (!path.isEmpty() && !path.equals(this.path)) {
                        this.path = path;
                        changed = true;
                    }
                    break;
                case "ws":
                    if (!path.equals(nz(wsPath))) {
                        wsPath = path;
                        changed = true;
                    }
                    break;
                case "h2":
                    if (!path.equals(nz(h2Path))) {
                        h2Path = path;
                        changed = true;
                    }
                    break;
                case "h3":
                    if (!path.equals(nz(h3Path))) {
                        h3Path = path;
                        changed = true;
                    }
                    break;
                case "quic":
                    if (!quic) {
                        quic = true;
                        changed = true;
                    }
                    break;
                default:
                    break;
            }
        }
        return changed;
    }

    private static boolean muxName(String t) {
        for (String x : MUX_ORDER) {
            if (x.equals(t)) {
                return true;
            }
        }
        return false;
    }

    static String nz(String s) {
        return s == null ? "" : s.trim();
    }

    String family() {
        if (isSSTP()) {
            return "sstp";
        }
        if (isVLESS()) {
            return "vless";
        }
        if (isTrojan()) {
            return "trojan";
        }
        return "loft";
    }

    String frontTitle() {
        int i = indexOf(kind);
        return i >= 0 ? FRONT_TITLES[i] : kind.toUpperCase();
    }

    String side() {
        if (isLoft()) {
            return "loft · " + kind.toLowerCase();
        }
        return family();
    }

    String host() {
        String u = url;
        if (u == null) {
            return "";
        }
        int at = u.indexOf('@');
        if (at >= 0 && (u.startsWith("loft://") || u.startsWith("vless://") || u.startsWith("trojan://")
                || u.startsWith("sstp://"))) {
            u = u.substring(at + 1);
        }
        u = u.replaceFirst("^[a-zA-Z][a-zA-Z0-9+.-]*://", "");
        int slash = u.indexOf('/');
        if (slash >= 0) {
            u = u.substring(0, slash);
        }
        int q = u.indexOf('?');
        if (q >= 0) {
            u = u.substring(0, q);
        }
        if (u.startsWith("[")) {
            int end = u.indexOf(']');
            if (end > 0) {
                return u.substring(1, end);
            }
        }
        int colon = u.lastIndexOf(':');
        if (colon > 0 && u.indexOf(':') == colon) {
            return u.substring(0, colon);
        }
        return u;
    }

    String sessionYaml() {
        if (yaml != null && !yaml.trim().isEmpty()) {
            return yaml;
        }
        return YamlBuilder.from(this);
    }

    JSONObject toJson() throws JSONException {
        JSONObject o = new JSONObject();
        o.put("id", id);
        o.put("name", name);
        o.put("kind", kind);
        o.put("token", token);
        o.put("url", url);
        o.put("path", path);
        o.put("cdn", cdn);
        o.put("edge", edge);
        o.put("sni", sni);
        o.put("user", user);
        o.put("pass", pass);
        o.put("dns", dns);
        o.put("maskPub", maskPub);
        o.put("inner", inner);
        o.put("wsPath", wsPath);
        o.put("h2Path", h2Path);
        o.put("h3Path", h3Path);
        o.put("listedFronts", listedFronts);
        o.put("fp", fp);
        o.put("yaml", yaml);
        o.put("mux", mux);
        o.put("quic", quic);
        o.put("tlsCheck", tlsCheck);
        o.put("pap", pap);
        o.put("dnsSystem", dnsSystem);
        return o;
    }

    static Profile fromJson(JSONObject o) {
        Profile p = new Profile();
        p.id = o.optString("id", p.id);
        p.name = o.optString("name");
        p.kind = o.optString("kind", "get");
        p.token = o.optString("token");
        p.url = o.optString("url");
        p.path = o.optString("path");
        p.cdn = o.optString("cdn");
        p.edge = o.optString("edge");
        p.sni = o.optString("sni");
        p.user = o.optString("user");
        p.pass = o.optString("pass");
        p.dns = o.optString("dns");
        p.maskPub = o.optString("maskPub");
        p.inner = o.optString("inner");
        p.wsPath = o.optString("wsPath");
        p.h2Path = o.optString("h2Path");
        p.h3Path = o.optString("h3Path");
        p.listedFronts = o.optString("listedFronts");
        p.fp = o.optString("fp", "chrome");
        p.yaml = o.optString("yaml");
        p.mux = o.optBoolean("mux", true);
        p.quic = o.optBoolean("quic", false);
        p.tlsCheck = o.optBoolean("tlsCheck", true);
        p.pap = o.optBoolean("pap", false);
        p.dnsSystem = o.optBoolean("dnsSystem", true);
        return p;
    }

    static Profile fromYaml(String yaml) {
        Profile p = new Profile();
        p.yaml = yaml == null ? "" : yaml;
        String fam = Peek.get(yaml, "kind");
        if (fam.isEmpty()) {
            fam = "loft";
        }
        p.kind = Peek.outbound(yaml, "type");
        if (p.kind.isEmpty()) {
            p.kind = fam.equals("loft") ? "get" : fam;
        }
        p.name = Peek.get(yaml, "name");
        if (p.name.isEmpty()) {
            p.name = Peek.outbound(yaml, "name");
        }
        p.token = Peek.get(yaml, "token");
        if (p.token.isEmpty()) {
            p.token = Peek.get(yaml, "password");
        }
        p.user = Peek.get(yaml, "username");
        p.pass = Peek.get(yaml, "password");
        p.edge = Peek.get(yaml, "edge");
        p.url = Peek.outbound(yaml, "url");
        p.path = Peek.outbound(yaml, "path");
        mergeOutbounds(p, yaml);
        p.cdn = Peek.outbound(yaml, "cdn");
        p.sni = Peek.outbound(yaml, "sni");
        if (p.sni.isEmpty()) {
            p.sni = Peek.get(yaml, "sni");
        }
        p.maskPub = Peek.outbound(yaml, "mask_pub");
        p.inner = Peek.get(yaml, "inner");
        p.fp = Peek.outbound(yaml, "fingerprint");
        if (p.fp.isEmpty()) {
            p.fp = "chrome";
        }
        p.mux = Peek.outboundBool(yaml, "mux", !p.isL3());
        if (p.isL3()) {
            p.mux = false;
        }
        String tls = Peek.get(yaml, "tls_check");
        if (!tls.isEmpty()) {
            p.tlsCheck = tls.equals("true") || tls.equals("yes");
        }
        String pap = Peek.get(yaml, "pap_chap");
        p.pap = pap.equals("true") || pap.equals("yes") || pap.equals("1");
        if (p.name.isEmpty()) {
            p.name = p.host();
        }
        String listed = Peek.get(yaml, "fronts");
        if (!listed.isEmpty()) {
            p.applyFrontList(listed);
        }
        return p;
    }

    /** One loft connection may list get/ws/h2/h3/quic outbounds. Merge them. */
    private static void mergeOutbounds(Profile p, String yaml) {
        String eh = hostOf(p.edge).toLowerCase();
        for (Peek.Ob o : Peek.outbounds(yaml)) {
            String t = o.type == null ? "" : o.type.toLowerCase();
            if (!eh.isEmpty() && hostOf(o.url).toLowerCase().equals(eh)) {
                continue;
            }
            if (p.maskPub.isEmpty() && o.maskPub != null && !o.maskPub.isEmpty()) {
                p.maskPub = o.maskPub;
            }
            if (p.cdn.isEmpty() && o.cdn != null && !o.cdn.isEmpty()) {
                p.cdn = o.cdn;
            }
            if ((p.fp == null || p.fp.isEmpty() || "chrome".equals(p.fp)) && o.fp != null && !o.fp.isEmpty()) {
                p.fp = o.fp;
            }
            switch (t) {
                case "get":
                    p.kind = "get";
                    if (!o.url.isEmpty()) {
                        p.url = o.url;
                    }
                    if (!o.path.isEmpty()) {
                        p.path = o.path;
                    }
                    break;
                case "ws":
                    if (nz(p.wsPath).isEmpty()) {
                        String up = YamlBuilder.pathOf(o.url);
                        p.wsPath = o.path.isEmpty() ? ("/".equals(up) || up.isEmpty() ? DEF_WS : up) : o.path;
                    }
                    if (p.url.isEmpty()) {
                        p.url = stripPath(o.url);
                    }
                    if (!"l3".equalsIgnoreCase(p.kind)) {
                        p.kind = "get";
                    }
                    break;
                case "h2":
                    if (nz(p.h2Path).isEmpty()) {
                        p.h2Path = o.path.isEmpty() ? DEF_H2 : o.path;
                    }
                    if (p.url.isEmpty()) {
                        p.url = stripPath(o.url);
                    }
                    if (!"l3".equalsIgnoreCase(p.kind)) {
                        p.kind = "get";
                    }
                    break;
                case "h3":
                    if (nz(p.h3Path).isEmpty()) {
                        p.h3Path = o.path.isEmpty() ? DEF_H2 : o.path;
                    }
                    if (p.url.isEmpty()) {
                        p.url = stripPath(o.url);
                    }
                    if (!"l3".equalsIgnoreCase(p.kind)) {
                        p.kind = "get";
                    }
                    break;
                case "quic":
                    p.quic = true;
                    if (p.url.isEmpty()) {
                        p.url = stripPath(o.url);
                    }
                    if (!"l3".equalsIgnoreCase(p.kind)) {
                        p.kind = "get";
                    }
                    break;
                default:
                    if (p.url.isEmpty() && !o.url.isEmpty()) {
                        p.url = o.url;
                    }
                    if (p.path.isEmpty() && !o.path.isEmpty()) {
                        p.path = o.path;
                    }
                    break;
            }
        }
    }

    private static String stripPath(String url) {
        if (url == null || url.isEmpty()) {
            return "";
        }
        String pth = YamlBuilder.pathOf(url);
        if (pth.isEmpty()) {
            return url;
        }
        int i = url.indexOf(pth);
        return i > 0 ? url.substring(0, i) : url;
    }

    static String hostOf(String url) {
        Profile x = new Profile();
        x.url = url == null ? "" : url;
        return x.host();
    }

    static int indexOf(String kind) {
        if (kind == null) {
            return -1;
        }
        String k = kind.toLowerCase();
        for (int i = 0; i < FRONTS.length; i++) {
            if (FRONTS[i].equals(k)) {
                return i;
            }
        }
        return -1;
    }

    static boolean known(String kind) {
        if (kind == null) {
            return false;
        }
        String k = kind.toLowerCase();
        return indexOf(k) >= 0 || k.equals("sstp") || k.equals("vless") || k.equals("trojan");
    }
}
