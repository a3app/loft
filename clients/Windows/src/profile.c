/* Windows client, portable core (also built into the host tests): connection
   profiles and app settings. Parses profile YAML, writes canonical YAML, builds
   the loft-front command line for a profile, loads and saves prefs, and parses
   the STATUS lines loft-front prints. share.c handles share links; win/store.c
   and win/session.c use the results. */
#include "profile.h"

#include "util.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* loft_profile_clear resets p to the defaults a new profile starts with:
   TUN and mux on, server certificate checked, SOCKS port picked automatically,
   and the GET front with its usual path. (A front is one way of carrying the
   session over HTTPS: get, ws, h2, quic, h3 or l3; mux is the stream
   multiplexer inside the session.) */
void loft_profile_clear(LoftProfile *p) {
    if (!p) return;
    memset(p, 0, sizeof *p);
    p->tun = 1;
    p->mux = 1;
    p->tls_check = 1;
    loft_strlcpy(p->socks, sizeof p->socks, "auto");
    loft_strlcpy(p->kind, sizeof p->kind, "get");
    loft_strlcpy(p->path, sizeof p->path, "/static/v3/build");
}

/* known_kind reports whether k is a connection kind the client understands:
   the loft fronts, the foreign protocols loft-front can also speak (sstp,
   vless, trojan), or "loft" as an alias of get. */
static int known_kind(const char *k) {
    return loft_strieq(k, "get") || loft_strieq(k, "ws") || loft_strieq(k, "h2") ||
           loft_strieq(k, "quic") || loft_strieq(k, "h3") || loft_strieq(k, "l3") ||
           loft_strieq(k, "sstp") || loft_strieq(k, "vless") || loft_strieq(k, "trojan") ||
           loft_strieq(k, "loft");
}

/* add_front appends name to the comma-separated front list dst unless it is
   already there. */
static void add_front(char *dst, size_t n, const char *name) {
    if (!name || !name[0]) return;
    if (dst[0] && !strstr(dst, name)) {
        size_t u = strlen(dst);
        if (u + 1 < n) dst[u++] = ',';
        loft_strlcpy(dst + u, n - u, name);
    } else if (!dst[0]) {
        loft_strlcpy(dst, n, name);
    }
}

/* split_kv cuts a "key: value" line at the first colon, trims both halves and
   unquotes the value. Returns 0 when there is no colon or the key is empty. */
static int split_kv(char *line, char **k, char **v) {
    char *c = strchr(line, ':');
    if (!c) return 0;
    *c = 0;
    *k = line;
    *v = c + 1;
    loft_trim(*k);
    loft_trim(*v);
    loft_unquote(*v);
    return (*k)[0] != 0;
}

/* apply_head stores one top-level profile field k = v into p. The secret
   (token, uuid or password, depending on the kind) goes to keybuf instead,
   because the key never lives inside LoftProfile. "fronts" can be an inline
   list [get, ws] or the start of a block list; *in_fronts tells the caller
   that "- name" lines that follow are fronts. */
static void apply_head(LoftProfile *p, char *keybuf, const char *k, const char *v, int *in_fronts) {
    if (loft_streq(k, "name")) loft_strlcpy(p->name, sizeof p->name, v);
    else if (loft_streq(k, "kind") || loft_streq(k, "type")) {
        if (known_kind(v)) loft_strlcpy(p->kind, sizeof p->kind, v);
    } else if (loft_streq(k, "token") || loft_streq(k, "uuid") || loft_streq(k, "password")) {
        loft_strlcpy(keybuf, LOFT_KEY, v);
    } else if (loft_streq(k, "instance_id")) loft_strlcpy(p->instance, sizeof p->instance, v);
    else if (loft_streq(k, "url")) loft_strlcpy(p->url, sizeof p->url, v);
    else if (loft_streq(k, "path")) loft_strlcpy(p->path, sizeof p->path, v);
    else if (loft_streq(k, "username") || loft_streq(k, "user")) loft_strlcpy(p->user, sizeof p->user, v);
    else if (loft_streq(k, "sni") || loft_streq(k, "hostname")) loft_strlcpy(p->sni, sizeof p->sni, v);
    else if (loft_streq(k, "dns")) loft_strlcpy(p->dns, sizeof p->dns, v);
    else if (loft_streq(k, "dns_replace")) p->dns_replace = loft_bool(v, 0);
    else if (loft_streq(k, "dns_system")) p->dns_system = loft_bool(v, 0);
    else if (loft_streq(k, "tls_check")) p->tls_check = loft_bool(v, 1);
    else if (loft_streq(k, "pap_chap") || loft_streq(k, "pap")) p->pap = loft_bool(v, 0);
    else if (loft_streq(k, "socks5")) {
        if (!v[0] || !loft_bool(v, 1)) p->socks[0] = 0;
        else loft_strlcpy(p->socks, sizeof p->socks, v);
    } else if (loft_streq(k, "tun")) p->tun = loft_bool(v, 1);
    else if (loft_streq(k, "mux")) p->mux = loft_bool(v, 1);
    else if (loft_streq(k, "edge")) loft_strlcpy(p->edge, sizeof p->edge, v);
    else if (loft_streq(k, "cdn")) loft_strlcpy(p->cdn, sizeof p->cdn, v);
    else if (loft_streq(k, "inner")) loft_strlcpy(p->inner, sizeof p->inner, v);
    else if (loft_streq(k, "mask_pub") || loft_streq(k, "pbk")) loft_strlcpy(p->mask_pub, sizeof p->mask_pub, v);
    else if (loft_streq(k, "fronts")) {
        if (!v[0] || loft_streq(v, "[") || loft_streq(v, "[]")) {
            *in_fronts = v[0] != '[' || strchr(v, ']') == 0;
            if (strchr(v, ']')) {
                /* inline [get, ws] */
                char tmp[80];
                loft_strlcpy(tmp, sizeof tmp, v);
                *in_fronts = 0;
                p->fronts[0] = 0;
            }
        }
        if (v[0] == '[') {
            char tmp[80], *tok;
            loft_strlcpy(tmp, sizeof tmp, v);
            for (tok = tmp; *tok; tok++)
                if (*tok == '[' || *tok == ']' || *tok == ',') *tok = ' ';
            p->fronts[0] = 0;
            tok = tmp;
            while (*tok) {
                char *w = tok;
                while (*w == ' ' || *w == '\t' || *w == '"' || *w == '\'') w++;
                tok = w;
                while (*tok && *tok != ' ' && *tok != '\t') tok++;
                if (*tok) *tok++ = 0;
                loft_unquote(w);
                add_front(p->fronts, sizeof p->fronts, w);
            }
            *in_fronts = 0;
        }
    }
}

/* finish_one turns the fields gathered so far into one finished profile in
   dst: fills a missing name (from the file name, else "Loft"), normalises the
   kind, makes an id from the name, copies the key and sets has_key. Returns 0
   when there is neither a URL nor a key, so empty sections are dropped. */
static int finish_one(LoftParsed *dst, const LoftProfile *head, const char *key, const char *file_name) {
    if (!head->url[0] && !key[0]) return 0;
    dst->p = *head;
    if (!dst->p.name[0]) loft_strlcpy(dst->p.name, sizeof dst->p.name, file_name && file_name[0] ? file_name : "Loft");
    if (!dst->p.kind[0]) loft_strlcpy(dst->p.kind, sizeof dst->p.kind, "get");
    loft_lower(dst->p.kind);
    if (loft_streq(dst->p.kind, "loft")) loft_strlcpy(dst->p.kind, sizeof dst->p.kind, "get");
    if (!dst->p.id[0]) loft_slug(dst->p.name, dst->p.id, sizeof dst->p.id);
    loft_strlcpy(dst->key, sizeof dst->key, key);
    dst->p.has_key = key[0] != 0;
    if (loft_streq(dst->p.kind, "get") && !dst->p.path[0])
        loft_strlcpy(dst->p.path, sizeof dst->p.path, "/static/v3/build");
    if (strstr(dst->p.fronts, "quic")) dst->p.quic = 1;
    return 1;
}

/* loft_parse_blob reads profile YAML (a .loft file or pasted text) into up to
   max profiles in out and returns how many it found. It is a small line-based
   reader, not a full YAML parser. It understands three shapes: one flat
   profile, a "connections:" list of several profiles, and the loft CLI config
   with an "outbounds:" list, whose first entries fill the URL, kind, paths and
   key and whose types become the front list. file_name names a profile that
   has no name of its own. */
int loft_parse_blob(const char *text, const char *file_name, LoftParsed *out, int max) {
    char line[1024];
    const char *s;
    int n = 0;
    int in_out = 0, in_fronts = 0, in_conn = 0;
    LoftProfile head;
    char key[LOFT_KEY];
    char ob_name[LOFT_NAME], ob_type[16], ob_url[LOFT_URL], ob_path[LOFT_PATH];
    char ob_cdn[32], ob_user[LOFT_NAME], ob_mask[80];
    int saw_ob = 0;

    if (!text || !out || max < 1) return 0;
    /* A share link is not YAML. share.c handles those before this is called
       for mixed blobs; a single-line URI still lands here from the file importer. */
    loft_profile_clear(&head);
    memset(key, 0, sizeof key);
    memset(ob_name, 0, sizeof ob_name);
    memset(ob_type, 0, sizeof ob_type);
    memset(ob_url, 0, sizeof ob_url);
    memset(ob_path, 0, sizeof ob_path);
    memset(ob_cdn, 0, sizeof ob_cdn);
    memset(ob_user, 0, sizeof ob_user);
    memset(ob_mask, 0, sizeof ob_mask);
    s = text;
    while (*s && n < max) {
        size_t ln = 0;
        char *k, *v;
        int dashed;
        while (*s && *s != '\n' && ln + 1 < sizeof line) line[ln++] = *s++;
        line[ln] = 0;
        if (*s == '\n') s++;
        loft_trim(line);
        if (!line[0] || line[0] == '#') continue;
        /* Section headers switch the reader's mode and carry no fields. */
        if (loft_streq(line, "connections:")) {
            in_conn = 1;
            in_out = 0;
            in_fronts = 0;
            continue;
        }
        if (loft_streq(line, "outbounds:")) {
            in_out = 1;
            in_fronts = 0;
            continue;
        }
        /* A "- " prefix starts a list item. In a connections list it closes the
           previous profile. */
        dashed = loft_starts(line, "- ");
        if (dashed) memmove(line, line + 2, strlen(line + 2) + 1);
        loft_trim(line);
        if (in_conn && dashed && !in_out) {
            if (n < max && finish_one(&out[n], &head, key, file_name)) n++;
            loft_profile_clear(&head);
            loft_wipe(key, sizeof key);
            in_out = 0;
        }
        /* Lines of a block-style fronts list. */
        if (in_fronts && !in_out) {
            if (dashed) {
                add_front(head.fronts, sizeof head.fronts, line);
                continue;
            }
            in_fronts = 0;
        }
        if (!split_kv(line, &k, &v)) continue;
        /* Inside outbounds: a new "- " item starts a fresh outbound; the first
           value seen for each field wins, except that each type's path goes to its
           own slot (ws_path, h2_path, ...). */
        if (in_out) {
            if (dashed && saw_ob) {
                memset(ob_name, 0, sizeof ob_name);
                memset(ob_type, 0, sizeof ob_type);
                memset(ob_url, 0, sizeof ob_url);
                memset(ob_path, 0, sizeof ob_path);
                memset(ob_cdn, 0, sizeof ob_cdn);
                memset(ob_user, 0, sizeof ob_user);
                memset(ob_mask, 0, sizeof ob_mask);
            }
            if (loft_streq(k, "name")) loft_strlcpy(ob_name, sizeof ob_name, v);
            else if (loft_streq(k, "type")) loft_strlcpy(ob_type, sizeof ob_type, v);
            else if (loft_streq(k, "url")) loft_strlcpy(ob_url, sizeof ob_url, v);
            else if (loft_streq(k, "path")) loft_strlcpy(ob_path, sizeof ob_path, v);
            else if (loft_streq(k, "cdn")) loft_strlcpy(ob_cdn, sizeof ob_cdn, v);
            else if (loft_streq(k, "mask_pub") || loft_streq(k, "pbk")) loft_strlcpy(ob_mask, sizeof ob_mask, v);
            else if (loft_streq(k, "username") || loft_streq(k, "user")) loft_strlcpy(ob_user, sizeof ob_user, v);
            else if ((loft_streq(k, "password") || loft_streq(k, "uuid")) && !key[0]) loft_strlcpy(key, sizeof key, v);
            saw_ob = 1;
            if (ob_type[0] && !head.kind[0]) loft_strlcpy(head.kind, sizeof head.kind, ob_type);
            if (ob_url[0] && !head.url[0]) loft_strlcpy(head.url, sizeof head.url, ob_url);
            if (ob_path[0]) {
                if (loft_strieq(ob_type, "ws")) loft_strlcpy(head.ws_path, sizeof head.ws_path, ob_path);
                else if (loft_strieq(ob_type, "h2")) loft_strlcpy(head.h2_path, sizeof head.h2_path, ob_path);
                else if (loft_strieq(ob_type, "h3")) loft_strlcpy(head.h3_path, sizeof head.h3_path, ob_path);
                else if (!head.path[0] || loft_strieq(ob_type, "get") || !ob_type[0])
                    loft_strlcpy(head.path, sizeof head.path, ob_path);
            }
            if (ob_cdn[0] && !head.cdn[0]) loft_strlcpy(head.cdn, sizeof head.cdn, ob_cdn);
            if (ob_mask[0] && !head.mask_pub[0]) loft_strlcpy(head.mask_pub, sizeof head.mask_pub, ob_mask);
            if (ob_user[0] && !head.user[0]) loft_strlcpy(head.user, sizeof head.user, ob_user);
            if (ob_name[0] && !head.name[0]) loft_strlcpy(head.name, sizeof head.name, ob_name);
            if (ob_type[0]) add_front(head.fronts, sizeof head.fronts, ob_type);
            continue;
        }
        apply_head(&head, key, k, v, &in_fronts);
    }

    /* The last (or only) profile has no following item to close it. */
    if (n < max && (head.url[0] || key[0])) {
        if (finish_one(&out[n], &head, key, file_name)) n++;
    }
    loft_wipe(key, sizeof key);
    return n;
}

/* append adds s to the string in dst; returns 0 if it would not fit. */
static int append(char *dst, size_t n, const char *s) {
    size_t u = strlen(dst), m = strlen(s);
    if (u + m + 1 > n) return 0;
    memcpy(dst + u, s, m + 1);
    return 1;
}

/* loft_profile_yaml writes p as canonical profile YAML into dst. key is
   written only when non-empty (export to a .loft file); the profile file
   saved on disk passes no key, because the key is kept apart, encrypted with
   DPAPI. The layout is a flat head plus a one-item outbounds list, so the same
   text also reads as a loft CLI config. Returns 0 if dst is too small. */
int loft_profile_yaml(const LoftProfile *p, const char *key, char *dst, size_t n) {
    char q[LOFT_URL * 2];
    const char *kind;
    if (!p || !dst || n == 0) return 0;
    dst[0] = 0;
    kind = p->kind[0] ? p->kind : "get";
    loft_quote(p->name, q, sizeof q);
    if (!append(dst, n, "name: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    if (loft_strieq(kind, "vless") || loft_strieq(kind, "trojan") || loft_strieq(kind, "sstp")) {
        if (!append(dst, n, "kind: ") || !append(dst, n, kind) || !append(dst, n, "\n")) return 0;
    }
    /* The secret's field name depends on the kind. */
    if (key && key[0]) {
        const char *field = "token";
        if (loft_strieq(kind, "vless")) field = "uuid";
        else if (loft_strieq(kind, "trojan") || loft_strieq(kind, "sstp")) field = "password";
        loft_quote(key, q, sizeof q);
        if (!append(dst, n, field) || !append(dst, n, ": ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (p->user[0]) {
        loft_quote(p->user, q, sizeof q);
        if (!append(dst, n, "username: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (p->instance[0]) {
        loft_quote(p->instance, q, sizeof q);
        if (!append(dst, n, "instance_id: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (p->edge[0]) {
        loft_quote(p->edge, q, sizeof q);
        if (!append(dst, n, "edge: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (p->inner[0]) {
        loft_quote(p->inner, q, sizeof q);
        if (!append(dst, n, "inner: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (p->sni[0]) {
        loft_quote(p->sni, q, sizeof q);
        if (!append(dst, n, "sni: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (p->dns[0]) {
        loft_quote(p->dns, q, sizeof q);
        if (!append(dst, n, "dns: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (p->dns_replace && !append(dst, n, "dns_replace: true\n")) return 0;
    if (p->dns_system && !append(dst, n, "dns_system: true\n")) return 0;
    if (!p->tls_check && !append(dst, n, "tls_check: false\n")) return 0;
    if (p->pap && !append(dst, n, "pap_chap: true\n")) return 0;
    if (p->fronts[0]) {
        if (!append(dst, n, "fronts: [") || !append(dst, n, p->fronts) || !append(dst, n, "]\n")) return 0;
    }
    /* Local listeners: the SOCKS5 port and whether TUN is used. */
    if (!append(dst, n, "listen:\n  socks5: ")) return 0;
    if (!p->socks[0]) {
        if (!append(dst, n, "false\n")) return 0;
    } else {
        loft_quote(p->socks, q, sizeof q);
        if (!append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (!append(dst, n, p->tun ? "  tun: auto\n" : "  tun: false\n")) return 0;
    if (!append(dst, n, p->mux ? "mux: true\n" : "mux: false\n")) return 0;
    loft_quote(p->name, q, sizeof q);
    /* The single outbound: where and how to reach the server. */
    if (!append(dst, n, "outbounds:\n  - name: ") || !append(dst, n, q) || !append(dst, n, "\n    type: ")) return 0;
    if (!append(dst, n, kind) || !append(dst, n, "\n    url: ")) return 0;
    loft_quote(p->url, q, sizeof q);
    if (!append(dst, n, q) || !append(dst, n, "\n")) return 0;
    if (p->path[0] && !loft_strieq(kind, "quic")) {
        loft_quote(p->path, q, sizeof q);
        if (!append(dst, n, "    path: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (p->cdn[0]) {
        loft_quote(p->cdn, q, sizeof q);
        if (!append(dst, n, "    cdn: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    if (p->mask_pub[0]) {
        loft_quote(p->mask_pub, q, sizeof q);
        if (!append(dst, n, "    mask_pub: ") || !append(dst, n, q) || !append(dst, n, "\n")) return 0;
    }
    return 1;
}

/* arg appends one argument to v; extra arguments past LOFT_ARGS are dropped. */
static void arg(LoftArgv *v, const char *s) {
    if (!v || v->n >= LOFT_ARGS) return;
    loft_strlcpy(v->store[v->n], LOFT_ARGN, s ? s : "");
    v->n++;
}

/* arg2 appends a flag and its value. */
static void arg2(LoftArgv *v, const char *a, const char *b) {
    arg(v, a);
    arg(v, b);
}

/* loft_build_argv builds the command line that starts loft-front (the Go
   session engine) for profile p: bin first, then the flags. socks is the
   local SOCKS5 address or "off"; tun_pipe is the named pipe to the loft
   service when the tunnel is used; instance_file is the per-profile file
   where loft-front keeps its overlay instance_id between runs. Flags that make no sense for the kind are
   left out (for example -fronts for sstp/vless/trojan/l3). Returns the
   argument count. */
int loft_build_argv(LoftArgv *v, const char *bin, const LoftProfile *p, const char *key,
                    const char *socks, const char *tun_pipe, const char *instance_file) {
    const char *kind;
    if (!v || !p) return 0;
    memset(v, 0, sizeof *v);
    kind = p->kind[0] ? p->kind : "get";
    arg(v, bin && bin[0] ? bin : "loft-front");
    if (key && key[0]) arg2(v, "-token", key);
    if (p->url[0]) arg2(v, "-url", p->url);
    arg2(v, "-type", kind);
    if (p->fronts[0] && !loft_strieq(kind, "sstp") && !loft_strieq(kind, "vless") && !loft_strieq(kind, "trojan") &&
        !loft_strieq(kind, "l3"))
        arg2(v, "-fronts", p->fronts);
    if (p->path[0] && (loft_strieq(kind, "get") || loft_strieq(kind, "auto"))) arg2(v, "-path", p->path);
    if (p->ws_path[0]) arg2(v, "-ws-path", p->ws_path);
    if (p->h2_path[0]) arg2(v, "-h2-path", p->h2_path);
    if (p->h3_path[0]) arg2(v, "-h3-path", p->h3_path);
    arg2(v, "-socks", socks && socks[0] ? socks : "off");
    if (!p->mux || loft_strieq(kind, "l3")) arg(v, "-mux=0");
    if (p->user[0] && loft_strieq(kind, "sstp")) arg2(v, "-user", p->user);
    if ((loft_strieq(kind, "sstp") || loft_strieq(kind, "vless") || loft_strieq(kind, "trojan")) && !p->tls_check)
        arg(v, "-insecure");
    if (p->sni[0]) arg2(v, "-sni", p->sni);
    if (p->pap && loft_strieq(kind, "sstp")) arg(v, "-pap");
    if (p->dns[0]) arg2(v, "-dns", p->dns);
    if (p->dns_replace) arg(v, "-dns-replace");
    if (p->edge[0]) arg2(v, "-edge", p->edge);
    if (p->inner[0]) arg2(v, "-inner", p->inner);
    if (p->cdn[0]) arg2(v, "-cdn", p->cdn);
    if (p->mask_pub[0]) arg2(v, "-pbk", p->mask_pub);
    if (instance_file && instance_file[0]) arg2(v, "-instance-file", instance_file);
    if (tun_pipe && tun_pipe[0]) arg2(v, "-tun-pipe", tun_pipe);
    return v->n;
}

/* loft_prefs_default fills p with the settings of a fresh install: Russian
   UI, file and link association on, failover and reconnect on, external IP
   shown, update check on, SOCKS share on port 1080, rates in bits. */
void loft_prefs_default(LoftPrefs *p) {
    if (!p) return;
    memset(p, 0, sizeof *p);
    loft_strlcpy(p->lang, sizeof p->lang, "ru");
    loft_strlcpy(p->ext_ip_url, sizeof p->ext_ip_url, "https://api.ipify.org");
    loft_strlcpy(p->taskbar, sizeof p->taskbar, "window");
    p->assoc_file = 1;
    p->assoc_link = 1;
    p->failover = 1;
    p->defroute = 1;
    p->reconnect = 1;
    p->socks_share = 1;
    p->ext_ip = 1;
    p->update_check = 1;
    p->socks_port = 1080;
    p->rate_bits = 1;
}

/* pref_val finds the line "key: value" in the prefs text (leading spaces
   allowed) and copies the unquoted value into buf. buf is "" when the key is
   absent, so callers keep their default. */
static const char *pref_val(const char *yaml, const char *key, char *buf, size_t n) {
    const char *s = yaml;
    size_t kn = strlen(key);
    buf[0] = 0;
    if (!s) return buf;
    while (*s) {
        const char *e = strchr(s, '\n');
        size_t ln = e ? (size_t)(e - s) : strlen(s);
        while (ln && (*s == ' ' || *s == '\t')) {
            s++;
            ln--;
        }
        if (ln > kn && strncmp(s, key, kn) == 0 && s[kn] == ':') {
            const char *v = s + kn + 1;
            size_t vn = ln - kn - 1;
            while (vn && (*v == ' ' || *v == '\t')) {
                v++;
                vn--;
            }
            if (vn >= n) vn = n - 1;
            memcpy(buf, v, vn);
            buf[vn] = 0;
            loft_unquote(buf);
            return buf;
        }
        if (!e) break;
        s = e + 1;
    }
    return buf;
}

/* loft_prefs_load starts from the defaults and overrides each setting found
   in yaml. Unknown or invalid values (taskbar mode, port out of range) leave
   the default. A NULL yaml just gives the defaults. */
void loft_prefs_load(LoftPrefs *p, const char *yaml) {
    char buf[LOFT_URL];
    loft_prefs_default(p);
    if (!yaml) return;
    pref_val(yaml, "lang", buf, sizeof buf);
    if (buf[0]) loft_strlcpy(p->lang, sizeof p->lang, buf);
    pref_val(yaml, "active", buf, sizeof buf);
    if (buf[0]) loft_strlcpy(p->active, sizeof p->active, buf);
    pref_val(yaml, "ext_ip_url", buf, sizeof buf);
    if (buf[0]) loft_strlcpy(p->ext_ip_url, sizeof p->ext_ip_url, buf);
    pref_val(yaml, "taskbar", buf, sizeof buf);
    if (loft_streq(buf, "always") || loft_streq(buf, "window") || loft_streq(buf, "never"))
        loft_strlcpy(p->taskbar, sizeof p->taskbar, buf);
    pref_val(yaml, "multi", buf, sizeof buf);
    if (buf[0]) p->multi = loft_bool(buf, 0);
    pref_val(yaml, "split", buf, sizeof buf);
    if (buf[0]) p->split = loft_bool(buf, 0);
    pref_val(yaml, "simple", buf, sizeof buf);
    if (buf[0]) p->simple = loft_bool(buf, 0);
    pref_val(yaml, "login", buf, sizeof buf);
    if (buf[0]) p->login = loft_bool(buf, 0);
    pref_val(yaml, "assoc_file", buf, sizeof buf);
    if (buf[0]) p->assoc_file = loft_bool(buf, 1);
    pref_val(yaml, "assoc_link", buf, sizeof buf);
    if (buf[0]) p->assoc_link = loft_bool(buf, 1);
    pref_val(yaml, "hold", buf, sizeof buf);
    if (buf[0]) p->hold = loft_bool(buf, 0);
    pref_val(yaml, "auto", buf, sizeof buf);
    if (buf[0]) p->auto_connect = loft_bool(buf, 0);
    pref_val(yaml, "failover", buf, sizeof buf);
    if (buf[0]) p->failover = loft_bool(buf, 1);
    pref_val(yaml, "default_route", buf, sizeof buf);
    if (buf[0]) p->defroute = loft_bool(buf, 1);
    pref_val(yaml, "reconnect", buf, sizeof buf);
    if (buf[0]) p->reconnect = loft_bool(buf, 1);
    pref_val(yaml, "socks_share", buf, sizeof buf);
    if (buf[0]) p->socks_share = loft_bool(buf, 1);
    pref_val(yaml, "ext_ip", buf, sizeof buf);
    if (buf[0]) p->ext_ip = loft_bool(buf, 1);
    pref_val(yaml, "diag", buf, sizeof buf);
    if (buf[0]) p->diag = loft_bool(buf, 0);
    pref_val(yaml, "update_check", buf, sizeof buf);
    if (buf[0]) p->update_check = loft_bool(buf, 1);
    pref_val(yaml, "first_done", buf, sizeof buf);
    if (buf[0]) p->first_done = loft_bool(buf, 0);
    pref_val(yaml, "socks_port", buf, sizeof buf);
    if (buf[0]) {
        int port = 0;
        sscanf(buf, "%d", &port);
        if (port >= 1 && port <= 65535) p->socks_port = port;
    }
    pref_val(yaml, "rate_bits", buf, sizeof buf);
    if (buf[0]) p->rate_bits = loft_bool(buf, 1);
}

/* loft_prefs_save writes every setting to dst in the format
   loft_prefs_load reads. Returns 0 if dst was too small. */
int loft_prefs_save(const LoftPrefs *p, char *dst, size_t n) {
    if (!p || !dst) return 0;
    return snprintf(dst, n,
                    "lang: %s\nactive: %s\next_ip_url: %s\ntaskbar: %s\n"
                    "multi: %s\nsplit: %s\nsimple: %s\nlogin: %s\n"
                    "assoc_file: %s\nassoc_link: %s\nhold: %s\nauto: %s\n"
                    "failover: %s\ndefault_route: %s\nreconnect: %s\n"
                    "socks_share: %s\next_ip: %s\ndiag: %s\nupdate_check: %s\n"
                    "first_done: %s\nsocks_port: %d\nrate_bits: %s\n",
                    p->lang, p->active, p->ext_ip_url, p->taskbar,
                    p->multi ? "true" : "false", p->split ? "true" : "false",
                    p->simple ? "true" : "false", p->login ? "true" : "false",
                    p->assoc_file ? "true" : "false", p->assoc_link ? "true" : "false",
                    p->hold ? "true" : "false", p->auto_connect ? "true" : "false",
                    p->failover ? "true" : "false", p->defroute ? "true" : "false",
                    p->reconnect ? "true" : "false", p->socks_share ? "true" : "false",
                    p->ext_ip ? "true" : "false", p->diag ? "true" : "false",
                    p->update_check ? "true" : "false", p->first_done ? "true" : "false",
                    p->socks_port, p->rate_bits ? "true" : "false") < (int)n;
}

/* loft_parse_status parses one status line printed by loft-front, such as
   "STATUS state=on ip=10.0.0.2 tun=1 up=123 down=456 msg=free text". Fields are
   key=value separated by spaces; msg= is always last and keeps its spaces, so
   it is cut off first. Unknown keys are ignored. Returns 1 when a state was
   present. */
int loft_parse_status(const char *line, LoftStatus *st) {
    const char *s, *msg;
    char head[768];
    if (!line || !st) return 0;
    memset(st, 0, sizeof *st);
    st->mux = -1;
    s = line;
    if (loft_starts(s, "STATUS ")) s += 7;
    /* Split off the free-text msg tail before reading the key=value head. */
    msg = strstr(s, " msg=");
    if (!msg && loft_starts(s, "msg=")) msg = s - 1;
    if (msg) {
        loft_strlcpy(st->msg, sizeof st->msg, msg + 5);
        if ((size_t)(msg - s) >= sizeof head) return 0;
        memcpy(head, s, (size_t)(msg - s));
        head[msg - s] = 0;
    } else {
        loft_strlcpy(head, sizeof head, s);
    }
    s = head;
    while (*s) {
        char key[32], val[256];
        size_t k = 0, v = 0;
        while (*s == ' ') s++;
        if (!*s) break;
        while (*s && *s != '=' && *s != ' ' && k + 1 < sizeof key) key[k++] = *s++;
        key[k] = 0;
        if (*s != '=') {
            while (*s && *s != ' ') s++;
            continue;
        }
        s++;
        while (*s && *s != ' ' && v + 1 < sizeof val) val[v++] = *s++;
        val[v] = 0;
        if (loft_streq(key, "state")) loft_strlcpy(st->state, sizeof st->state, val);
        else if (loft_streq(key, "ip")) loft_strlcpy(st->ip, sizeof st->ip, val);
        else if (loft_streq(key, "gw")) loft_strlcpy(st->gw, sizeof st->gw, val);
        else if (loft_streq(key, "dns")) loft_strlcpy(st->dns, sizeof st->dns, val);
        else if (loft_streq(key, "tun")) loft_strlcpy(st->tun, sizeof st->tun, val);
        else if (loft_streq(key, "front")) loft_strlcpy(st->front, sizeof st->front, val);
        else if (loft_streq(key, "fronts")) loft_strlcpy(st->fronts, sizeof st->fronts, val);
        else if (loft_streq(key, "routes")) loft_strlcpy(st->routes, sizeof st->routes, val);
        else if (loft_streq(key, "mtu")) st->mtu = atoi(val);
        else if (loft_streq(key, "mux")) st->mux = atoi(val);
        else if (loft_streq(key, "up")) {
            st->up = atoll(val);
            st->has_up = 1;
        } else if (loft_streq(key, "down")) {
            st->down = atoll(val);
            st->has_down = 1;
        }
    }
    return st->state[0] != 0;
}

