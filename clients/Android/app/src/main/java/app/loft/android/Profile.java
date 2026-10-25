package app.loft.android;

import org.json.JSONException;
import org.json.JSONObject;

import java.util.UUID;

/** One named connection. YAML is what Engine.Start consumes. */
final class Profile {
    static final String[] FRONTS = {"get", "ws", "h2", "quic", "h3", "l3"};
    static final String[] FRONT_TITLES = {"GET", "WS", "H2", "QUIC", "H3", "L3"};

    String id = UUID.randomUUID().toString();
    String name = "";
    String kind = "get";
    String token = "";
    String url = "";
    String path = "";
    String cdn = "";
    String sni = "";
    String user = "";
    String pass = "";
    String dns = "";
    String maskPub = "";
    String inner = "";
    String fp = "chrome";
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
        o.put("sni", sni);
        o.put("user", user);
        o.put("pass", pass);
        o.put("dns", dns);
        o.put("maskPub", maskPub);
        o.put("inner", inner);
        o.put("fp", fp);
        o.put("yaml", yaml);
        o.put("mux", mux);
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
        p.sni = o.optString("sni");
        p.user = o.optString("user");
        p.pass = o.optString("pass");
        p.dns = o.optString("dns");
        p.maskPub = o.optString("maskPub");
        p.inner = o.optString("inner");
        p.fp = o.optString("fp", "chrome");
        p.yaml = o.optString("yaml");
        p.mux = o.optBoolean("mux", true);
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
        p.url = Peek.outbound(yaml, "url");
        p.path = Peek.outbound(yaml, "path");
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
        return p;
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
