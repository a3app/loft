#include "routes.h"

#include "util.h"

#include <stdio.h>
#include <string.h>

static void zone_init(LoftZone *z, const char *id, const char *name, int via) {
    memset(z, 0, sizeof *z);
    loft_strlcpy(z->id, sizeof z->id, id);
    loft_strlcpy(z->name, sizeof z->name, name);
    z->builtin = 1;
    z->def_via = via;
    z->auto_pull = 1;
}

void loft_routes_default(LoftRoutes *r) {
    memset(r, 0, sizeof *r);
    zone_init(&r->zones[0], "local", "Local", 0);
    zone_init(&r->zones[1], "global", "Global", 1);
    r->nz = 2;
}

static LoftZone *find_zone(LoftRoutes *r, const char *id) {
    int i;
    for (i = 0; i < r->nz; i++)
        if (loft_streq(r->zones[i].id, id)) return &r->zones[i];
    return 0;
}

int loft_routes_load(LoftRoutes *r, const char *text) {
    char line[1024];
    const char *s = text ? text : "";
    LoftZone *z = 0;
    LoftSet *set = 0;
    int mode = 0; /* 1 zone, 2 set */
    loft_routes_default(r);
    r->ns = 0;
    /* Keep the built-in zones, then overwrite fields the file sets. A file
       that names them replaces the defaults in place. */
    while (*s) {
        size_t ln = 0;
        char *k, *c;
        char raw[1024];
        while (*s && *s != '\n' && ln + 1 < sizeof line) line[ln++] = *s++;
        line[ln] = 0;
        if (*s == '\n') s++;
        loft_strlcpy(raw, sizeof raw, line);
        loft_trim(line);
        if (!line[0] || line[0] == '#') {
            if (!line[0]) {
                mode = 0;
                set = 0;
            }
            continue;
        }
        /* `zone:` under a set is that set's zone. A blank line ended the set. */
        if (loft_starts(line, "zone:") && mode != 2) {
            char id[32];
            loft_strlcpy(id, sizeof id, line + 5);
            loft_trim(id);
            loft_unquote(id);
            z = find_zone(r, id);
            if (!z && r->nz < LOFT_ZONES) {
                z = &r->zones[r->nz++];
                memset(z, 0, sizeof *z);
                loft_strlcpy(z->id, sizeof z->id, id);
                loft_strlcpy(z->name, sizeof z->name, id);
                z->auto_pull = 1;
            }
            set = 0;
            mode = 1;
            continue;
        }
        if (loft_starts(line, "set:")) {

            if (r->ns >= LOFT_SETS) continue;
            set = &r->sets[r->ns++];
            memset(set, 0, sizeof *set);
            loft_strlcpy(set->id, sizeof set->id, line + 4);
            loft_trim(set->id);
            loft_unquote(set->id);
            loft_strlcpy(set->src, sizeof set->src, "me");
            if (z) loft_strlcpy(set->zone, sizeof set->zone, z->id);
            mode = 2;
            continue;
        }
        c = strchr(line, ':');
        if (!c) continue;
        *c = 0;
        k = line;
        loft_trim(k);
        c++;
        loft_trim(c);
        loft_unquote(c);
        if (mode == 1 && z) {
            if (loft_streq(k, "name")) loft_strlcpy(z->name, sizeof z->name, c);
            else if (loft_streq(k, "repo")) loft_strlcpy(z->repo, sizeof z->repo, c);
            else if (loft_streq(k, "builtin")) z->builtin = loft_bool(c, 0);
            else if (loft_streq(k, "def")) z->def_via = loft_bool(c, 0);
            else if (loft_streq(k, "auto")) z->auto_pull = loft_bool(c, 1);
        } else if (mode == 2 && set) {
            if (loft_streq(k, "name")) loft_strlcpy(set->name, sizeof set->name, c);
            else if (loft_streq(k, "zone")) loft_strlcpy(set->zone, sizeof set->zone, c);
            else if (loft_streq(k, "src")) loft_strlcpy(set->src, sizeof set->src, c);
            else if (loft_streq(k, "cidr")) loft_strlcpy(set->cidr, sizeof set->cidr, c);
            else if (loft_streq(k, "via")) {
                /* Legacy files put via on the set. false → local, true → global. */
                loft_strlcpy(set->zone, sizeof set->zone, loft_bool(c, 0) ? "global" : "local");
            }
        }
        (void)raw;
    }
    return 1;
}

int loft_routes_save(const LoftRoutes *r, char *dst, size_t n) {
    int i;
    size_t u = 0;
    if (!r || !dst || n == 0) return 0;
    dst[0] = 0;
    for (i = 0; i < r->nz; i++) {
        const LoftZone *z = &r->zones[i];
        int w = snprintf(dst + u, n - u,
                         "zone: %s\nname: %s\nbuiltin: %s\ndef: %s\nrepo: %s\nauto: %s\n\n",
                         z->id, z->name, z->builtin ? "true" : "false",
                         z->def_via ? "true" : "false", z->repo, z->auto_pull ? "true" : "false");
        if (w < 0 || u + (size_t)w >= n) return 0;
        u += (size_t)w;
    }
    for (i = 0; i < r->ns; i++) {
        const LoftSet *s = &r->sets[i];
        int w = snprintf(dst + u, n - u, "set: %s\nname: %s\nzone: %s\nsrc: %s\ncidr: %s\n\n",
                         s->id, s->name, s->zone, s->src[0] ? s->src : "me", s->cidr);
        if (w < 0 || u + (size_t)w >= n) return 0;
        u += (size_t)w;
    }
    return 1;
}

int loft_routes_add(LoftRoutes *r, const char *zone, const char *name, const char *cidr, const char *src) {
    LoftSet *s;
    if (!r || r->ns >= LOFT_SETS || !cidr || !cidr[0]) return 0;
    s = &r->sets[r->ns++];
    memset(s, 0, sizeof *s);
    loft_slug(name && name[0] ? name : cidr, s->id, sizeof s->id);
    loft_strlcpy(s->name, sizeof s->name, name && name[0] ? name : cidr);
    loft_strlcpy(s->zone, sizeof s->zone, zone && zone[0] ? zone : "global");
    loft_strlcpy(s->src, sizeof s->src, src && src[0] ? src : "me");
    loft_strlcpy(s->cidr, sizeof s->cidr, cidr);
    return 1;
}

static int mask_bits(const char *mask) {
    unsigned a, b, c, d, n = 0, i;
    unsigned long v;
    if (sscanf(mask, "%u.%u.%u.%u", &a, &b, &c, &d) != 4) return -1;
    if (a > 255 || b > 255 || c > 255 || d > 255) return -1;
    v = (a << 24) | (b << 16) | (c << 8) | d;
    for (i = 0; i < 32; i++) {
        if (v & (1ul << (31 - i))) n++;
        else break;
    }
    return (int)n;
}

int loft_routes_import_bat(LoftRoutes *r, const char *zone, const char *text) {
    char line[512];
    const char *s = text ? text : "";
    const char *z = zone && zone[0] ? zone : "global";
    int i, w = 0;
    if (!r) return 0;
    for (i = 0; i < r->ns; i++) {
        if (loft_streq(r->sets[i].zone, z) && loft_streq(r->sets[i].src, "repo")) continue;
        r->sets[w++] = r->sets[i];
    }
    r->ns = w;
    while (*s) {
        size_t ln = 0;
        char *rem, *add;
        unsigned a, b, c, d;
        char mask[32], name[64], cidr[80];
        int bits;
        while (*s && *s != '\n' && ln + 1 < sizeof line) line[ln++] = *s++;
        line[ln] = 0;
        if (*s == '\n') s++;
        loft_trim(line);
        add = strstr(line, "route ADD ");
        if (!add) add = strstr(line, "ROUTE ADD ");
        if (!add) continue;
        if (sscanf(add, "%*s %*s %u.%u.%u.%u MASK %31s", &a, &b, &c, &d, mask) != 5 &&
            sscanf(add, "route ADD %u.%u.%u.%u MASK %31s", &a, &b, &c, &d, mask) != 5)
            continue;
        bits = mask_bits(mask);
        if (bits < 0) continue;
        snprintf(cidr, sizeof cidr, "%u.%u.%u.%u/%d", a, b, c, d, bits);
        name[0] = 0;
        rem = strstr(line, "rem ");
        if (!rem) rem = strstr(line, "REM ");
        if (rem) {
            loft_strlcpy(name, sizeof name, rem + 4);
            loft_trim(name);
        }
        if (!name[0]) loft_strlcpy(name, sizeof name, cidr);
        loft_routes_add(r, z, name, cidr, "repo");
    }
    return 1;
}

