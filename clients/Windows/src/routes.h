/* routes.txt: zone blocks, then services. Same file the macOS client writes. */
#ifndef LOFT_ROUTES_H
#define LOFT_ROUTES_H

#include <stddef.h>

#define LOFT_ZONES 16
#define LOFT_SETS 256

typedef struct LoftZone {
    char id[32];
    char name[64];
    char repo[512];
    int builtin;
    int def_via; /* 1 = through Loft */
    int auto_pull;
} LoftZone;

typedef struct LoftSet {
    char id[32];
    char name[64];
    char zone[32];
    char src[16]; /* me, repo, ignore */
    char cidr[80];
} LoftSet;

typedef struct LoftRoutes {
    LoftZone zones[LOFT_ZONES];
    LoftSet sets[LOFT_SETS];
    int nz, ns;
} LoftRoutes;

void loft_routes_default(LoftRoutes *r);
int loft_routes_load(LoftRoutes *r, const char *text);
int loft_routes_save(const LoftRoutes *r, char *dst, size_t n);
int loft_routes_add(LoftRoutes *r, const char *zone, const char *name, const char *cidr, const char *src);
/* Replaces src=repo rows in zone with route ADD lines from a Keenetic .bat. */
int loft_routes_import_bat(LoftRoutes *r, const char *zone, const char *text);

#endif
