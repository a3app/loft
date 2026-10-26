package app.loft.android;

/** Client YAML for Engine.Start. Shape matches clients/mobile marshalClient. */
final class YamlBuilder {
    /**
     * Builds the engine config for a profile: top-level identity (member key
     * for loft/VLESS/Trojan, user and password for SSTP), an optional edge
     * (direct node address for the extra fronts), an optional inner cipher
     * (extra encryption inside TLS), the "fronts" list when more than one
     * front is used, and the outbounds (the servers to dial). For loft the
     * first outbound is GET whenever GET is among the fronts.
     */
    static String from(Profile p) {
        if (p == null) {
            return "";
        }
        StringBuilder b = new StringBuilder();
        String fam = p.family();
        line(b, "kind", fam.equals("loft") ? "loft" : fam);
        if (!p.name.isEmpty()) {
            line(b, "name", p.name);
        }
        if (p.isSSTP()) {
            line(b, "username", p.user);
            line(b, "password", p.pass.isEmpty() ? p.token : p.pass);
            line(b, "pap_chap", p.pap ? "true" : "false");
            line(b, "tls_check", p.tlsCheck ? "true" : "false");
            if (!p.sni.isEmpty()) {
                line(b, "sni", p.sni);
            }
        } else {
            line(b, "token", p.token);
        }
        b.append("select: auto\n");
        if (p.isLoft() && p.edge != null && !p.edge.isEmpty()) {
            line(b, "edge", p.edge);
        }
        if (p.isLoft() && p.inner != null && !p.inner.isEmpty()) {
            line(b, "inner", p.inner);
        }
        String[] mux = p.isLoft() ? p.muxFronts() : new String[0];
        if (p.isLoft() && !p.isL3() && (mux.length > 1 || !Profile.nz(p.listedFronts).isEmpty())) {
            b.append("fronts: [");
            for (int i = 0; i < mux.length; i++) {
                if (i > 0) {
                    b.append(", ");
                }
                b.append(mux[i]);
            }
            b.append("]\n");
        }
        b.append("outbounds:\n");
        String obType = p.kind.toLowerCase();
        if (p.isLoft() && contains(mux, "get")) {
            obType = "get";
        }
        b.append("  - name: ").append(quote(p.name.isEmpty() ? p.host() : p.name)).append('\n');
        b.append("    type: ").append(obType).append('\n');
        String url = p.url;
        if (p.isLoft()) {
            // GET/H2/H3 speak HTTP. A stored wss:// URL is the WS front, not GET.
            url = "ws".equals(obType) ? wsURL(p) : originURL(p);
        }
        b.append("    url: ").append(quote(url)).append('\n');
        if (p.isLoft() && "get".equals(obType) && p.path != null && !p.path.isEmpty()) {
            b.append("    path: ").append(quote(p.path)).append('\n');
        }
        if (p.isLoft()) {
            if (!p.cdn.isEmpty()) {
                b.append("    cdn: ").append(quote(p.cdn)).append('\n');
            }
            if (!p.maskPub.isEmpty()) {
                b.append("    mask_pub: ").append(quote(p.maskPub)).append('\n');
            }
            String fp = p.fp == null || p.fp.isEmpty() ? "chrome" : p.fp;
            b.append("    fingerprint: ").append(fp).append('\n');
            if (p.isL3() || !p.mux) {
                b.append("    mux: false\n");
            }
            appendExtraFronts(b, p);
        } else if (!p.sni.isEmpty()) {
            b.append("    sni: ").append(quote(p.sni)).append('\n');
        }
        if (!p.isSSTP() && !p.tlsCheck) {
            b.append("    insecure_skip_verify: true\n");
        }
        return b.toString();
    }

    /**
     * The share form of a profile: a loft:// or sstp:// link built from its
     * fields, or the original link for VLESS and Trojan. Used for QR sharing
     * and TV pairing.
     */
    static String emit(Profile p) {
        if (p == null) {
            return "";
        }
        if (p.isVLESS() || p.isTrojan()) {
            return p.url;
        }
        if (p.isSSTP()) {
            return emitSstp(p);
        }
        return emitLoft(p);
    }

    /**
     * Builds a loft:// link: key@host[:port][/path]?type=&amp;scheme=&amp;cdn=&amp;edge=&amp;
     * extra fronts&amp;pbk=#name. Defaults (GET, https/wss, port 443/80) are left
     * out to keep the link and its QR short. pbk is the node's mask key.
     */
    static String emitLoft(Profile p) {
        String token = p.token == null ? "" : p.token.trim();
        String host = p.host();
        if (token.isEmpty() && host.isEmpty()) {
            return "";
        }
        String type = p.kind == null ? "get" : p.kind.toLowerCase();
        if (Profile.indexOf(type) < 0) {
            type = "get";
        }
        String scheme = schemeOf(p.url, type);
        int port = portOf(p.url, scheme);
        StringBuilder b = new StringBuilder("loft://");
        b.append(enc(token)).append('@');
        if (host.indexOf(':') >= 0) {
            b.append('[').append(host).append(']');
        } else {
            b.append(host);
        }
        int def = ("http".equals(scheme) || "ws".equals(scheme)) ? 80 : 443;
        if (port > 0 && port != def) {
            b.append(':').append(port);
        }
        String path = "get".equals(type) ? nz(p.path) : pathOf(p.url);
        if (!path.isEmpty()) {
            if (!path.startsWith("/")) {
                b.append('/');
            }
            b.append(path);
        }
        StringBuilder q = new StringBuilder();
        if (!"get".equals(type)) {
            q.append("type=").append(type);
        }
        String defSch = "ws".equals(type) ? "wss" : "https";
        if (!scheme.isEmpty() && !scheme.equals(defSch)) {
            if (q.length() > 0) {
                q.append('&');
            }
            q.append("scheme=").append(scheme);
        }
        if (!nz(p.cdn).isEmpty()) {
            if (q.length() > 0) {
                q.append('&');
            }
            q.append("cdn=").append(enc(p.cdn));
        }
        if (!nz(p.edge).isEmpty()) {
            if (q.length() > 0) {
                q.append('&');
            }
            q.append("edge=").append(enc(p.edge));
        }
        appendFrontQuery(q, p);
        if (!nz(p.maskPub).isEmpty()) {
            if (q.length() > 0) {
                q.append('&');
            }
            q.append("pbk=").append(enc(p.maskPub));
        }
        if (q.length() > 0) {
            b.append('?').append(q);
        }
        if (!nz(p.name).isEmpty()) {
            b.append('#').append(enc(p.name).replace("+", "%20"));
        }
        return b.toString();
    }

    private static String emitSstp(Profile p) {
        String user = nz(p.user);
        String pass = p.pass.isEmpty() ? nz(p.token) : p.pass;
        String host = p.host();
        int port = portOf(p.url, "https");
        StringBuilder b = new StringBuilder("sstp://");
        if (!user.isEmpty()) {
            b.append(enc(user));
        }
        b.append(':').append(enc(pass)).append('@').append(host);
        if (port > 0 && port != 443) {
            b.append(':').append(port);
        }
        String path = pathOf(p.url);
        if (!path.isEmpty()) {
            if (!path.startsWith("/")) {
                b.append('/');
            }
            b.append(path);
        }
        StringBuilder q = new StringBuilder();
        if (!nz(p.sni).isEmpty()) {
            q.append("sni=").append(enc(p.sni));
        }
        if (p.pap) {
            if (q.length() > 0) {
                q.append('&');
            }
            q.append("pap=1");
        }
        if (q.length() > 0) {
            b.append('?').append(q);
        }
        if (!nz(p.name).isEmpty()) {
            b.append('#').append(enc(p.name).replace("+", "%20"));
        }
        return b.toString();
    }

    /**
     * Writes one outbound per extra mux front (WS, H2, H3) that has a custom
     * path, on the same origin as the main URL. A front on its default path
     * gets no outbound of its own here. Nothing is written for CDN profiles,
     * where only GET is used, or for L3.
     */
    private static void appendExtraFronts(StringBuilder b, Profile p) {
        if (p.isCDN() || p.isL3()) {
            return;
        }
        String origin = originURL(p);
        String n = p.name.isEmpty() ? p.host() : p.name;
        boolean wsFirst = "ws".equalsIgnoreCase(p.kind);
        if (!wsFirst && (contains(p.muxFronts(), "ws") || !nz(p.wsPath).isEmpty()
                || "ws".equals(schemeOf(p.url, p.kind)) || "wss".equals(schemeOf(p.url, p.kind)))) {
            b.append("  - name: ").append(quote(n + "-ws")).append('\n');
            b.append("    type: ws\n");
            b.append("    url: ").append(quote(wsURL(p))).append('\n');
        }
        if (!nz(p.h2Path).isEmpty() && !Profile.DEF_H2.equals(p.h2Path)) {
            b.append("  - name: ").append(quote(n + "-h2")).append('\n');
            b.append("    type: h2\n");
            b.append("    url: ").append(quote(origin)).append('\n');
            b.append("    path: ").append(quote(p.h2Path)).append('\n');
        }
        if (!nz(p.h3Path).isEmpty() && !Profile.DEF_H2.equals(p.h3Path)) {
            b.append("  - name: ").append(quote(n + "-h3")).append('\n');
            b.append("    type: h3\n");
            b.append("    url: ").append(quote(origin)).append('\n');
            b.append("    path: ").append(quote(p.h3Path)).append('\n');
        }
    }

    // Adds ws= / h2= / h3= / quic to a loft:// link when the profile uses extra fronts.
    private static void appendFrontQuery(StringBuilder q, Profile p) {
        if (p == null || !p.isLoft() || p.isL3()) {
            return;
        }
        String[] mux = p.muxFronts();
        boolean extra = mux.length > 1 || p.hasMuxExtras();
        if (!extra) {
            return;
        }
        if (contains(mux, "ws") || !nz(p.wsPath).isEmpty()) {
            if (q.length() > 0) {
                q.append('&');
            }
            q.append("ws=").append(enc(nz(p.wsPath).isEmpty() ? Profile.DEF_WS : p.wsPath));
        }
        if (contains(mux, "h2") || !nz(p.h2Path).isEmpty()) {
            if (q.length() > 0) {
                q.append('&');
            }
            q.append("h2=").append(enc(nz(p.h2Path).isEmpty() ? Profile.DEF_H2 : p.h2Path));
        }
        if (contains(mux, "h3") || !nz(p.h3Path).isEmpty()) {
            if (q.length() > 0) {
                q.append('&');
            }
            q.append("h3=").append(enc(nz(p.h3Path).isEmpty() ? Profile.DEF_H2 : p.h3Path));
        }
        if (contains(mux, "quic") || p.quic) {
            if (q.length() > 0) {
                q.append('&');
            }
            q.append("quic");
        }
    }

    /** wss://host[:port]/wsPath. GET must never see this URL. */
    static String wsURL(Profile p) {
        String origin = originURL(p);
        String wss = origin.replace("https://", "wss://").replace("http://", "ws://");
        String path = nz(p.wsPath);
        if (path.isEmpty()) {
            String sch = schemeOf(p.url, p.kind);
            if ("ws".equals(sch) || "wss".equals(sch)) {
                path = pathOf(p.url);
            }
        }
        if (path.isEmpty()) {
            path = Profile.DEF_WS;
        }
        if (!path.startsWith("/")) {
            path = "/" + path;
        }
        return wss + path;
    }

    // scheme://host[:port] of the main URL, with ws/wss mapped to http/https.
    private static String originURL(Profile p) {
        String sch = schemeOf(p.url, p.kind);
        if ("ws".equals(sch) || "wss".equals(sch)) {
            sch = "ws".equals(sch) ? "http" : "https";
        }
        if (!"http".equals(sch) && !"https".equals(sch)) {
            sch = "https";
        }
        String host = p.host();
        int port = portOf(p.url, sch);
        int def = "http".equals(sch) ? 80 : 443;
        if (host.indexOf(':') >= 0) {
            host = "[" + host + "]";
        }
        return sch + "://" + host + (port > 0 && port != def ? ":" + port : "");
    }

    private static boolean contains(String[] a, String v) {
        if (a == null) {
            return false;
        }
        for (String x : a) {
            if (v.equals(x)) {
                return true;
            }
        }
        return false;
    }

    private static void line(StringBuilder b, String k, String v) {
        b.append(k).append(": ").append(quote(v == null ? "" : v)).append('\n');
    }

    // Quotes a YAML value when it could be misread: contains ':', '#', a space
    // or a quote, or is a member key (lt_...), which must stay a string.
    private static String quote(String s) {
        if (s.isEmpty()) {
            return "\"\"";
        }
        if (s.indexOf(':') >= 0 || s.indexOf('#') >= 0 || s.indexOf(' ') >= 0
                || s.indexOf('"') >= 0 || s.startsWith("lt_")) {
            return '"' + s.replace("\\", "\\\\").replace("\"", "\\\"") + '"';
        }
        return s;
    }

    private static String nz(String s) {
        return s == null ? "" : s.trim();
    }

    private static String enc(String s) {
        try {
            return java.net.URLEncoder.encode(s, "UTF-8").replace("+", "%20");
        } catch (Exception e) {
            return s;
        }
    }

    static String schemeOf(String url, String type) {
        if (url == null) {
            return "ws".equals(type) ? "wss" : "https";
        }
        int i = url.indexOf("://");
        if (i <= 0) {
            return "ws".equals(type) ? "wss" : "https";
        }
        return url.substring(0, i).toLowerCase();
    }

    /** Port from a URL, or the scheme's default (80 for http/ws, 443 otherwise). */
    static int portOf(String url, String scheme) {
        int def = ("http".equals(scheme) || "ws".equals(scheme)) ? 80 : 443;
        String hostport = hostPort(url);
        if (hostport.startsWith("[")) {
            int end = hostport.indexOf(']');
            if (end >= 0 && end + 2 < hostport.length() && hostport.charAt(end + 1) == ':') {
                try {
                    return Integer.parseInt(hostport.substring(end + 2));
                } catch (NumberFormatException e) {
                    return def;
                }
            }
            return def;
        }
        int c = hostport.lastIndexOf(':');
        if (c > 0) {
            try {
                return Integer.parseInt(hostport.substring(c + 1));
            } catch (NumberFormatException e) {
                return def;
            }
        }
        return def;
    }

    /** Path part of a URL without the query; "" for no path or a bare "/". */
    static String pathOf(String url) {
        if (url == null) {
            return "";
        }
        int scheme = url.indexOf("://");
        int start = scheme >= 0 ? scheme + 3 : 0;
        int slash = url.indexOf('/', start);
        if (slash < 0) {
            return "";
        }
        int q = url.indexOf('?', slash);
        String p = q < 0 ? url.substring(slash) : url.substring(slash, q);
        return "/".equals(p) ? "" : p;
    }

    private static String hostPort(String url) {
        if (url == null) {
            return "";
        }
        int scheme = url.indexOf("://");
        int start = scheme >= 0 ? scheme + 3 : 0;
        int at = url.indexOf('@', start);
        if (at >= 0) {
            start = at + 1;
        }
        int slash = url.indexOf('/', start);
        int q = url.indexOf('?', start);
        int end = url.length();
        if (slash >= 0) {
            end = slash;
        }
        if (q >= 0 && q < end) {
            end = q;
        }
        return url.substring(start, end);
    }
}
