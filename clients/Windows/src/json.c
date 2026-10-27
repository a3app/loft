/* Windows client, portable core (also built into the host tests): a minimal
   JSON reader for the language packs. It only reads objects of string values,
   and can find one pack inside a packs["code"] = { ... } script file. Used
   by l10n.c. */
#include "json.h"

#include "util.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* loft_map_free releases every key and value and leaves m empty and reusable. */
void loft_map_free(LoftStrMap *m) {
    int i;
    if (!m) return;
    for (i = 0; i < m->n; i++) {
        free(m->keys[i]);
        free(m->vals[i]);
    }
    free(m->keys);
    free(m->vals);
    memset(m, 0, sizeof *m);
}

/* loft_map_get returns the value stored under key, or NULL. A linear scan:
   a pack holds a few hundred strings and lookups are not hot. */
const char *loft_map_get(const LoftStrMap *m, const char *key) {
    int i;
    if (!m || !key) return 0;
    for (i = 0; i < m->n; i++) {
        if (strcmp(m->keys[i], key) == 0) return m->vals[i];
    }
    return 0;
}

/* map_put appends a copy of key k and value v (lengths kn and vn), growing the
   arrays by doubling. Returns 0 when memory runs out. */
static int map_put(LoftStrMap *m, const char *k, size_t kn, const char *v, size_t vn) {
    char *ks, *vs;
    if (m->n == m->cap) {
        int cap = m->cap ? m->cap * 2 : 32;
        char **nk = realloc(m->keys, (size_t)cap * sizeof *nk);
        char **nv;
        if (!nk) return 0;
        m->keys = nk;
        nv = realloc(m->vals, (size_t)cap * sizeof *nv);
        if (!nv) return 0;
        m->vals = nv;
        m->cap = cap;
    }
    ks = malloc(kn + 1);
    vs = malloc(vn + 1);
    if (!ks || !vs) {
        free(ks);
        free(vs);
        return 0;
    }
    memcpy(ks, k, kn);
    ks[kn] = 0;
    memcpy(vs, v, vn);
    vs[vn] = 0;
    m->keys[m->n] = ks;
    m->vals[m->n] = vs;
    m->n++;
    return 1;
}

/* Decode a JSON string starting at the opening quote. *end is the char after it. */
static int parse_string(const char *s, char *buf, size_t bufn, const char **end) {
    size_t w = 0;
    if (*s != '"') return 0;
    s++;
    while (*s && *s != '"') {
        unsigned char c = (unsigned char)*s++;
        if (c == '\\') {
            char e = *s++;
            if (!e) return 0;
            switch (e) {
            case 'n': c = '\n'; break;
            case 'r': c = '\r'; break;
            case 't': c = '\t'; break;
            case 'u': {
                int i, cp = 0;
                for (i = 0; i < 4 && *s; i++, s++) {
                    char h = *s;
                    cp <<= 4;
                    if (h >= '0' && h <= '9') cp += h - '0';
                    else if (h >= 'a' && h <= 'f') cp += h - 'a' + 10;
                    else if (h >= 'A' && h <= 'F') cp += h - 'A' + 10;
                    else return 0;
                }
                /* BMP only. The packs use this for a few punctuation marks. */
                if (cp < 0x80) c = (unsigned char)cp;
                else if (cp < 0x800) {
                    if (w + 2 >= bufn) return 0;
                    buf[w++] = (char)(0xC0 | (cp >> 6));
                    buf[w++] = (char)(0x80 | (cp & 0x3F));
                    continue;
                } else {
                    if (w + 3 >= bufn) return 0;
                    buf[w++] = (char)(0xE0 | (cp >> 12));
                    buf[w++] = (char)(0x80 | ((cp >> 6) & 0x3F));
                    buf[w++] = (char)(0x80 | (cp & 0x3F));
                    continue;
                }
                break;
            }
            default: c = (unsigned char)e; break;
            }
        }
        if (w + 1 >= bufn) return 0;
        buf[w++] = (char)c;
    }
    if (*s != '"') return 0;
    buf[w] = 0;
    *end = s + 1;
    return 1;
}

/* loft_map_parse reads the first {...} object in obj and stores each
   "key": "string" pair in m. Values that are objects, arrays, numbers or
   literals are skipped, not stored. Returns 1 when the closing brace was
   reached, 0 on malformed input (pairs read before the error stay in m). */
int loft_map_parse(LoftStrMap *m, const char *obj) {
    const char *s = obj;
    char key[256], val[4096];
    if (!m || !s) return 0;
    while (*s && *s != '{') s++;
    if (*s != '{') return 0;
    s++;
    while (*s) {
        while (*s == ' ' || *s == '\t' || *s == '\r' || *s == '\n' || *s == ',') s++;
        if (*s == '}') return 1;
        if (*s != '"') return 0;
        if (!parse_string(s, key, sizeof key, &s)) return 0;
        while (*s == ' ' || *s == '\t') s++;
        if (*s != ':') return 0;
        s++;
        while (*s == ' ' || *s == '\t' || *s == '\n' || *s == '\r') s++;
        if (*s == '"') {
            if (!parse_string(s, val, sizeof val, &s)) return 0;
            if (!map_put(m, key, strlen(key), val, strlen(val))) return 0;
        } else {
            /* Skip a nested value. Packs only look up strings. */
            int depth = 0;
            if (*s == '{' || *s == '[') {
                char open = *s, close = open == '{' ? '}' : ']';
                depth = 1;
                s++;
                while (*s && depth) {
                    if (*s == '"') {
                        const char *e;
                        if (!parse_string(s, val, sizeof val, &e)) return 0;
                        s = e;
                        continue;
                    }
                    if (*s == open) depth++;
                    else if (*s == close) depth--;
                    s++;
                }
            } else {
                while (*s && *s != ',' && *s != '}') s++;
            }
        }
    }
    return 0;
}

/* loft_map_pack finds pack_name["code"] (or with single quotes) in a lang-pack
   script, takes the {...} object that follows it and parses it into m. The
   design lang files are JavaScript that assign one object per language into a
   table, so this avoids running any script. */
int loft_map_pack(LoftStrMap *m, const char *text, const char *pack_name, const char *code) {
    char needle[64];
    const char *p, *s;
    int depth;
    if (!text || !pack_name || !code) return 0;
    snprintf(needle, sizeof needle, "%s[\"%s\"]", pack_name, code);
    p = strstr(text, needle);
    if (!p) {
        snprintf(needle, sizeof needle, "%s['%s']", pack_name, code);
        p = strstr(text, needle);
    }
    if (!p) return 0;
    s = strchr(p, '{');
    if (!s) return 0;
    /* Brace match so a later pack is not swallowed. Strings can hold braces. */
    depth = 0;
    for (p = s; *p; p++) {
        if (*p == '"') {
            p++;
            while (*p && *p != '"') {
                if (*p == '\\' && p[1]) p += 2;
                else p++;
            }
            if (!*p) break;
            continue;
        }
        if (*p == '{') depth++;
        else if (*p == '}') {
            depth--;
            if (depth == 0) {
                size_t n = (size_t)(p - s + 1);
                char *tmp = malloc(n + 1);
                int ok;
                if (!tmp) return 0;
                memcpy(tmp, s, n);
                tmp[n] = 0;
                ok = loft_map_parse(m, tmp);
                free(tmp);
                return ok;
            }
        }
    }
    return 0;
}

