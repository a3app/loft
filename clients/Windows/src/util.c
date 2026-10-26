#include "util.h"

#include <ctype.h>
#include <string.h>

void loft_strlcpy(char *dst, size_t n, const char *src) {
    size_t i;
    if (n == 0) return;
    if (!src) src = "";
    for (i = 0; i + 1 < n && src[i]; i++) dst[i] = src[i];
    dst[i] = 0;
}

int loft_streq(const char *a, const char *b) {
    if (!a || !b) return a == b;
    return strcmp(a, b) == 0;
}

int loft_strieq(const char *a, const char *b) {
    if (!a || !b) return a == b;
    while (*a && *b) {
        if (tolower((unsigned char)*a) != tolower((unsigned char)*b)) return 0;
        a++;
        b++;
    }
    return *a == 0 && *b == 0;
}

void loft_trim(char *s) {
    char *a;
    size_t n;
    if (!s) return;
    a = s;
    while (*a == ' ' || *a == '\t' || *a == '\r' || *a == '\n') a++;
    if (a != s) memmove(s, a, strlen(a) + 1);
    n = strlen(s);
    while (n > 0 && (s[n - 1] == ' ' || s[n - 1] == '\t' || s[n - 1] == '\r' || s[n - 1] == '\n')) {
        s[--n] = 0;
    }
}

void loft_lower(char *s) {
    for (; s && *s; s++) *s = (char)tolower((unsigned char)*s);
}

int loft_starts(const char *s, const char *pfx) {
    size_t n;
    if (!s || !pfx) return 0;
    n = strlen(pfx);
    return strncmp(s, pfx, n) == 0;
}

int loft_bool(const char *v, int fallback) {
    if (!v || !v[0]) return fallback;
    if (loft_strieq(v, "true") || loft_strieq(v, "on") || loft_strieq(v, "1") || loft_strieq(v, "yes") || loft_strieq(v, "auto"))
        return 1;
    if (loft_strieq(v, "false") || loft_strieq(v, "off") || loft_strieq(v, "0") || loft_strieq(v, "no") || loft_strieq(v, "-"))
        return 0;
    return fallback;
}

void loft_quote(const char *s, char *dst, size_t n) {
    size_t i = 0;
    if (n == 0) return;
    if (i + 1 < n) dst[i++] = '"';
    for (; s && *s && i + 2 < n; s++) {
        if (*s == '"' || *s == '\\') {
            if (i + 3 >= n) break;
            dst[i++] = '\\';
        }
        if (*s == '\n') {
            if (i + 3 >= n) break;
            dst[i++] = '\\';
            dst[i++] = 'n';
            continue;
        }
        dst[i++] = *s;
    }
    if (i + 1 < n) dst[i++] = '"';
    dst[i] = 0;
}

void loft_unquote(char *s) {
    size_t n, r, w;
    if (!s) return;
    loft_trim(s);
    n = strlen(s);
    if (n >= 2 && ((s[0] == '"' && s[n - 1] == '"') || (s[0] == '\'' && s[n - 1] == '\''))) {
        s[n - 1] = 0;
        memmove(s, s + 1, n - 1);
    }
    /* Collapse the escapes the writer emits. Other backslashes stay. */
    for (r = 0, w = 0; s[r]; r++) {
        if (s[r] == '\\' && s[r + 1]) {
            char c = s[++r];
            if (c == 'n') c = '\n';
            s[w++] = c;
        } else {
            s[w++] = s[r];
        }
    }
    s[w] = 0;
}

static int semver_part(const char **p) {
    int n = 0;
    if (**p == 'v' || **p == 'V') (*p)++;
    while (**p >= '0' && **p <= '9') {
        n = n * 10 + (**p - '0');
        (*p)++;
    }
    if (**p == '.') (*p)++;
    return n;
}

int loft_newer(const char *remote, const char *local) {
    const char *a = remote ? remote : "";
    const char *b = local ? local : "";
    int i;
    while (*a == ' ' || *a == '\t') a++;
    while (*b == ' ' || *b == '\t') b++;
    for (i = 0; i < 3; i++) {
        int x = semver_part(&a);
        int y = semver_part(&b);
        if (x != y) return x > y;
    }
    return 0;
}

void loft_wipe(void *p, size_t n) {
    volatile unsigned char *b = p;
    size_t i;
    if (!b) return;
    for (i = 0; i < n; i++) b[i] = 0;
}

int loft_slug(const char *name, char *dst, size_t n) {
    size_t i = 0;
    int dash = 0;
    if (!dst || n == 0) return 0;
    if (!name) name = "";
    for (; *name && i + 1 < n; name++) {
        unsigned char c = (unsigned char)*name;
        if (c >= 'A' && c <= 'Z') c = (unsigned char)(c - 'A' + 'a');
        if ((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
            dst[i++] = (char)c;
            dash = 0;
        } else if (c == ' ' || c == '-' || c == '_') {
            if (i > 0 && !dash && i + 1 < n) {
                dst[i++] = '-';
                dash = 1;
            }
        }
    }
    if (i > 0 && dst[i - 1] == '-') i--;
    dst[i] = 0;
    if (i == 0) loft_strlcpy(dst, n, "connection");
    return (int)strlen(dst);
}

