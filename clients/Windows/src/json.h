/* String maps from the design lang packs (a JSON object of strings). */
#ifndef LOFT_JSON_H
#define LOFT_JSON_H

typedef struct LoftStrMap {
    char **keys;
    char **vals;
    int n;
    int cap;
} LoftStrMap;

void loft_map_free(LoftStrMap *m);
const char *loft_map_get(const LoftStrMap *m, const char *key);
/* Parse a JSON object whose values are strings. Nested objects are skipped. */
int loft_map_parse(LoftStrMap *m, const char *obj);
/* Find the object assigned to packs["code"] = { ... } and parse it. */
int loft_map_pack(LoftStrMap *m, const char *text, const char *pack_name, const char *code);

#endif
