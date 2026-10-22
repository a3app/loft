package app.loft.android;

/** Client YAML for Engine.Start. Shape matches clients/mobile marshalClient. */
final class YamlBuilder {
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
        b.append("outbounds:\n");
        b.append("  - name: ").append(quote(p.name.isEmpty() ? p.host() : p.name)).append('\n');
        b.append("    type: ").append(p.kind.toLowerCase()).append('\n');
        b.append("    url: ").append(quote(p.url)).append('\n');
        if (p.isLoft() && "get".equalsIgnoreCase(p.kind) && p.path != null && !p.path.isEmpty()) {
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
        } else if (!p.sni.isEmpty()) {
            b.append("    sni: ").append(quote(p.sni)).append('\n');
        }
        if (!p.isSSTP() && !p.tlsCheck) {
            b.append("    insecure_skip_verify: true\n");
        }
        return b.toString();
    }

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

    private static void line(StringBuilder b, String k, String v) {
        b.append(k).append(": ").append(quote(v == null ? "" : v)).append('\n');
    }

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
