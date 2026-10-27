/* Windows client, portable core (also built into the host tests): turns share
   links (loft://, sstp://, vless://, trojan://, or one link per line) into
   profiles. Text that is not a link falls through to the YAML parser in
   profile.c. */
#include "share.h"

#include "util.h"

#include <ctype.h>
#include <stdio.h>
#include <string.h>

static void add_copy(char *dst, size_t n, const char *name) {
    size_t u = strlen(dst);
    if (u && u + 1 < n) dst[u++] = ',';
    if (u < n) loft_strlcpy(dst + u, n - u, name);
}

static int hexval(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

static void urldec(const char *s, size_t n, char *dst, size_t dn) {
    size_t w = 0, i;
    for (i = 0; i < n && w + 1 < dn; i++) {
        if (s[i] == '%' && i + 2 < n) {
            int a = hexval(s[i + 1]), b = hexval(s[i + 2]);
            if (a >= 0 && b >= 0) {
                dst[w++] = (char)((a << 4) | b);
                i += 2;
                continue;
            }
        }
        dst[w++] = s[i] == '+' ? ' ' : s[i];
    }
    dst[w] = 0;
}

static const char *qget(const char *q, const char *key, char *dst, size_t n) {
    size_t kn = strlen(key);
    const char *s = q ? q : "";
    dst[0] = 0;
    while (*s && *s != '#') {
        if ((s == q || s[-1] == '&' || s[-1] == '?') && strncmp(s, key, kn) == 0 && (s[kn] == '=' || s[kn] == '&' || s[kn] == '#' || s[kn] == 0)) {
            if (s[kn] != '=') return dst;
            s += kn + 1;
            {
                const char *e = s;
                while (*e && *e != '&' && *e != '#') e++;
                urldec(s, (size_t)(e - s), dst, n);
            }
            return dst;
        }
        s++;
    }
    return dst;
}

static int qhas(const char *q, const char *key) {
    size_t kn = strlen(key);
    const char *s = q ? q : "";
    while (*s && *s != '#') {
        if ((s == q || s[-1] == '&' || s[-1] == '?') && strncmp(s, key, kn) == 0 &&
            (s[kn] == '=' || s[kn] == '&' || s[kn] == '#' || s[kn] == 0))
            return 1;
        s++;
    }
    return 0;
}

static int parse_loft(const char *uri, LoftParsed *d) {
    const char *s, *at, *host, *path, *query, *frag;
    char token[LOFT_KEY], hostbuf[256], port[8], qv[LOFT_PATH];
    char listed[80];
    if (!loft_starts(uri, "loft://")) return 0;
    s = uri + 7;
    at = strchr(s, '@');
    if (!at) return 0;
    urldec(s, (size_t)(at - s), token, sizeof token);
    /* user:token is not the loft shape; the user is the key. */
    {
        char *colon = strchr(token, ':');
        if (colon) memmove(token, colon + 1, strlen(colon + 1) + 1);
    }
    host = at + 1;
    path = host;
    while (*path && *path != '/' && *path != '?' && *path != '#') path++;
    if (path == host) return 0;
    if ((size_t)(path - host) >= sizeof hostbuf) return 0;
    memcpy(hostbuf, host, (size_t)(path - host));
    hostbuf[path - host] = 0;
    {
        char *c = strrchr(hostbuf, ':');
        /* Leave an IPv6 host alone; loft links use a hostname. */
        if (c && strchr(hostbuf, ':') == c) {
            loft_strlcpy(port, sizeof port, c + 1);
            *c = 0;
        } else {
            loft_strlcpy(port, sizeof port, "443");
        }
    }
    query = "";
    frag = "";
    if (*path == '/') {
        query = strchr(path, '?');
        frag = strchr(path, '#');
    } else if (*path == '?') {
        query = path;
        frag = strchr(path, '#');
        path = "";
    } else if (*path == '#') {
        frag = path;
        path = "";
    }
    loft_profile_clear(&d->p);
    loft_strlcpy(d->key, sizeof d->key, token);
    d->p.has_key = token[0] != 0;
    if (frag && *frag == '#') {
        urldec(frag + 1, strlen(frag + 1), d->p.name, sizeof d->p.name);
    }
    if (!d->p.name[0]) loft_strlcpy(d->p.name, sizeof d->p.name, hostbuf);
    loft_slug(d->p.name, d->p.id, sizeof d->p.id);
    if (query && *query == '?') {
        char scheme[16];
        qget(query + 1, "scheme", scheme, sizeof scheme);
        if (!scheme[0]) loft_strlcpy(scheme, sizeof scheme, "https");
        snprintf(d->p.url, sizeof d->p.url, "%s://%s%s%s", scheme, hostbuf,
                 loft_streq(port, "443") || loft_streq(port, "80") ? "" : ":",
                 loft_streq(port, "443") || loft_streq(port, "80") ? "" : port);
        qget(query + 1, "cdn", d->p.cdn, sizeof d->p.cdn);
        qget(query + 1, "pbk", d->p.mask_pub, sizeof d->p.mask_pub);
        qget(query + 1, "edge", d->p.edge, sizeof d->p.edge);
        listed[0] = 0;
        if (qhas(query + 1, "get")) add_copy(listed, sizeof listed, "get");
        if (qhas(query + 1, "ws")) add_copy(listed, sizeof listed, "ws");
        if (qhas(query + 1, "h2")) add_copy(listed, sizeof listed, "h2");
        if (qhas(query + 1, "h3")) add_copy(listed, sizeof listed, "h3");
        if (qhas(query + 1, "quic")) add_copy(listed, sizeof listed, "quic");
        if (listed[0]) {
            loft_strlcpy(d->p.fronts, sizeof d->p.fronts, listed);
            loft_strlcpy(d->p.kind, sizeof d->p.kind, "get");
            qget(query + 1, "get", d->p.path, sizeof d->p.path);
            qget(query + 1, "ws", d->p.ws_path, sizeof d->p.ws_path);
            qget(query + 1, "h2", d->p.h2_path, sizeof d->p.h2_path);
            qget(query + 1, "h3", d->p.h3_path, sizeof d->p.h3_path);
            if (strstr(listed, "quic")) d->p.quic = 1;
            if (!d->p.path[0]) loft_strlcpy(d->p.path, sizeof d->p.path, "/static/v3/build");
        } else {
            qget(query + 1, "type", d->p.kind, sizeof d->p.kind);
            if (!d->p.kind[0]) loft_strlcpy(d->p.kind, sizeof d->p.kind, "get");
            loft_lower(d->p.kind);
            if (*path == '/') urldec(path, query ? (size_t)(query - path) : strlen(path), d->p.path, sizeof d->p.path);
            if (loft_streq(d->p.path, "/")) d->p.path[0] = 0;
            if (loft_streq(d->p.kind, "get") && !d->p.path[0])
                loft_strlcpy(d->p.path, sizeof d->p.path, "/static/v3/build");
        }
    } else {
        snprintf(d->p.url, sizeof d->p.url, "https://%s%s%s", hostbuf,
                 loft_streq(port, "443") ? "" : ":", loft_streq(port, "443") ? "" : port);
        loft_strlcpy(d->p.kind, sizeof d->p.kind, "get");
        if (*path == '/') urldec(path, strlen(path), d->p.path, sizeof d->p.path);
        if (!d->p.path[0] || loft_streq(d->p.path, "/"))
            loft_strlcpy(d->p.path, sizeof d->p.path, "/static/v3/build");
    }
    (void)qv;
    return token[0] && hostbuf[0];
}

static int parse_userinfo(const char *scheme, const char *uri, LoftParsed *d) {
    const char *s, *at, *host;
    char info[LOFT_KEY];
    if (!loft_starts(uri, scheme)) return 0;
    s = uri + strlen(scheme);
    at = strrchr(s, '@');
    if (!at) return 0;
    urldec(s, (size_t)(at - s), info, sizeof info);
    host = at + 1;
    loft_profile_clear(&d->p);
    loft_strlcpy(d->p.url, sizeof d->p.url, uri);
    {
        char *hash = strchr(d->p.url, '#');
        if (hash) {
            urldec(hash + 1, strlen(hash + 1), d->p.name, sizeof d->p.name);
            *hash = 0;
        }
    }
    if (loft_starts(scheme, "sstp")) {
        char *colon = strchr(info, ':');
        loft_strlcpy(d->p.kind, sizeof d->p.kind, "sstp");
        if (colon) {
            *colon = 0;
            loft_strlcpy(d->p.user, sizeof d->p.user, info);
            loft_strlcpy(d->key, sizeof d->key, colon + 1);
        } else {
            loft_strlcpy(d->key, sizeof d->key, info);
        }
    } else if (loft_starts(scheme, "vless")) {
        loft_strlcpy(d->p.kind, sizeof d->p.kind, "vless");
        loft_strlcpy(d->key, sizeof d->key, info);
    } else {
        char *colon = strchr(info, ':');
        loft_strlcpy(d->p.kind, sizeof d->p.kind, "trojan");
        if (colon) *colon = 0;
        loft_strlcpy(d->key, sizeof d->key, info);
    }
    d->p.has_key = d->key[0] != 0;
    if (!d->p.name[0]) {
        char hostbuf[128];
        size_t i = 0;
        while (host[i] && host[i] != ':' && host[i] != '/' && host[i] != '?' && i + 1 < sizeof hostbuf) {
            hostbuf[i] = host[i];
            i++;
        }
        hostbuf[i] = 0;
        loft_strlcpy(d->p.name, sizeof d->p.name, hostbuf[0] ? hostbuf : d->p.kind);
    }
    loft_slug(d->p.name, d->p.id, sizeof d->p.id);
    d->p.path[0] = 0;
    return d->p.url[0] != 0;
}

static int parse_one(const char *line, LoftParsed *d) {
    char tmp[2048];
    loft_strlcpy(tmp, sizeof tmp, line);
    loft_trim(tmp);
    if (loft_starts(tmp, "loft://")) return parse_loft(tmp, d);
    if (loft_starts(tmp, "sstp://")) return parse_userinfo("sstp://", tmp, d);
    if (loft_starts(tmp, "vless://")) return parse_userinfo("vless://", tmp, d);
    if (loft_starts(tmp, "trojan://")) return parse_userinfo("trojan://", tmp, d);
    return 0;
}

int loft_parse_share(const char *text, const char *file_name, LoftParsed *out, int max) {
    const char *s;
    int n;
    if (!text || !out || max < 1) return 0;
    s = text;
    while (*s == ' ' || *s == '\t' || *s == '\r' || *s == '\n') s++;
    if ((unsigned char)s[0] == 0xEF) s += 3; /* UTF-8 BOM, if the caller left it. */
    n = 0;
    if (parse_one(s, &out[0])) {
        /* A single link, or the first of several lines. */
        n = 1;
    }
    if (strchr(s, '\n')) {
        char line[2048];
        n = 0;
        while (*s && n < max) {
            size_t ln = 0;
            while (*s && *s != '\n' && ln + 1 < sizeof line) line[ln++] = *s++;
            line[ln] = 0;
            if (*s == '\n') s++;
            if (parse_one(line, &out[n])) n++;
        }
        if (n > 0) return n;
    } else if (n == 1) {
        return 1;
    }
    return loft_parse_blob(text, file_name, out, max);
}

