/* UI strings. Lookup: Windows pack, English Windows pack, macOS pack. */
#ifndef LOFT_L10N_H
#define LOFT_L10N_H

#include <stddef.h>

#define LOFT_LANGS 10

/* The language list: code, native name, and code -> index. */
const char *loft_lang_code(int i);
const char *loft_lang_native(int i);
int loft_lang_index(const char *code);

/* Loading packs and choosing the current language. */
void loft_l10n_reset(void);
int loft_l10n_add_macos(const char *code, const char *text);
int loft_l10n_add_win(const char *code, const char *text);
void loft_l10n_set(const char *code);
const char *loft_l10n_lang(void);
/* Underscore keys also try the dotted form. Missing keys return the key. */
const char *loft_t(const char *key);
/* Substitutes the first %d or %@. */
void loft_tf(char *dst, size_t n, const char *key, const char *arg);

#endif
