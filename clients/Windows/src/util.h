/* Small string helpers shared by the Windows client and the host tests. */
#ifndef LOFT_UTIL_H
#define LOFT_UTIL_H

#include <stddef.h>
#include <stdint.h>

/* Bounded copy and comparisons; NULL-safe. */
void loft_strlcpy(char *dst, size_t n, const char *src);
int loft_streq(const char *a, const char *b);
int loft_strieq(const char *a, const char *b);
void loft_trim(char *s);
void loft_lower(char *s);
int loft_starts(const char *s, const char *pfx);
/* Reading and writing values in the client's text config files. */
int loft_bool(const char *v, int fallback);
void loft_quote(const char *s, char *dst, size_t n);
void loft_unquote(char *s);
/* Version compare for the update check, key wiping, and name-to-id slugs. */
int loft_newer(const char *remote, const char *local);
void loft_wipe(void *p, size_t n);
int loft_slug(const char *name, char *dst, size_t n);

#endif
