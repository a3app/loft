/* Windows client, portable core (also built into the host tests): UI strings
   in ten languages. Keeps the Windows-specific pack and the shared macOS pack
   per language and looks a key up through both, falling back to English.
   win/app.c loads the pack files; json.c parses them. */
#include "l10n.h"

#include "json.h"
#include "util.h"

#include <string.h>

/* Supported languages: ISO code and the name written in that language, as
   shown in the language picker. The index into these arrays is the language id
   used everywhere else in this file. */
static const char *codes[LOFT_LANGS] = {"en", "fr", "de", "it", "pt", "ru", "vi", "zh", "ja", "ko"};
static const char *natives[LOFT_LANGS] = {
    "English", "Français", "Deutsch", "Italiano", "Português",
    "Русский", "Tiếng Việt", "中文", "日本語", "한국어"};

/* Loaded string packs per language: mac holds the shared texts of the macOS
   client (L10N_PACKS), win holds Windows-only texts (WIN_PACKS). cur is the
   language in use. */
static LoftStrMap mac[LOFT_LANGS];
static LoftStrMap win[LOFT_LANGS];
static int cur = 5; /* Russian, matching the macOS client. */

/* loft_lang_code returns the ISO code of language i ("ru" when out of range). */
const char *loft_lang_code(int i) {
    if (i < 0 || i >= LOFT_LANGS) return "ru";
    return codes[i];
}

/* loft_lang_native returns the native name of language i for the picker. */
const char *loft_lang_native(int i) {
    if (i < 0 || i >= LOFT_LANGS) return natives[5];
    return natives[i];
}

/* loft_lang_index maps an ISO code (any case) to a language id, or -1. */
int loft_lang_index(const char *code) {
    int i;
    for (i = 0; i < LOFT_LANGS; i++) {
        if (loft_strieq(code, codes[i])) return i;
    }
    return -1;
}

/* loft_l10n_reset frees all loaded packs and goes back to Russian. */
void loft_l10n_reset(void) {
    int i;
    for (i = 0; i < LOFT_LANGS; i++) {
        loft_map_free(&mac[i]);
        loft_map_free(&win[i]);
    }
    cur = 5;
}

/* loft_l10n_add_macos loads language code from the text of a macOS lang file
   (L10N_PACKS["code"] = {...}). Returns 0 on unknown code or parse failure. */
int loft_l10n_add_macos(const char *code, const char *text) {
    int i = loft_lang_index(code);
    if (i < 0 || !text) return 0;
    return loft_map_pack(&mac[i], text, "L10N_PACKS", code);
}

/* loft_l10n_add_win loads language code from a Windows lang file
   (WIN_PACKS["code"] = {...}). */
int loft_l10n_add_win(const char *code, const char *text) {
    int i = loft_lang_index(code);
    if (i < 0 || !text) return 0;
    return loft_map_pack(&win[i], text, "WIN_PACKS", code);
}

/* loft_l10n_set switches the UI language; unknown codes fall back to Russian. */
void loft_l10n_set(const char *code) {
    int i = loft_lang_index(code);
    cur = i < 0 ? 5 : i;
}

/* loft_l10n_lang returns the code of the current UI language. */
const char *loft_l10n_lang(void) { return codes[cur]; }

/* lookup finds key in the Windows pack of one language only. */
static const char *lookup(int lang, const char *key) {
    const char *s;
    if (lang < 0 || lang >= LOFT_LANGS) return 0;
    s = loft_map_get(&win[lang], key);
    if (s) return s;
    return 0;
}

/* loft_t translates key. Search order: Windows pack in the current language,
   Windows pack in English, macOS pack in the current language, macOS pack in
   English, macOS pack in Russian. If nothing matches and the key has
   underscores, the same search runs again with dots (a_b -> a.b), because
   some keys exist only in dotted form, such as "board.sub". A key that is
   found nowhere is returned as is, so a missing text is visible but
   harmless. */
const char *loft_t(const char *key) {
    const char *s;
    char dotted[128];
    int en = 0;
    if (!key) return "";
    s = lookup(cur, key);
    if (!s && cur != en) s = lookup(en, key);
    if (!s) s = loft_map_get(&mac[cur], key);
    if (!s && cur != en) s = loft_map_get(&mac[en], key);
    if (!s && cur != 5) s = loft_map_get(&mac[5], key);
    if (!s && strchr(key, '_')) {
        size_t i;
        loft_strlcpy(dotted, sizeof dotted, key);
        for (i = 0; dotted[i]; i++)
            if (dotted[i] == '_') dotted[i] = '.';
        if (!loft_streq(dotted, key)) {
            s = loft_t(dotted);
            if (s && !loft_streq(s, dotted)) return s;
            s = 0;
        }
    }
    return s ? s : key;
}

/* loft_tf translates key and puts arg in place of the first %d or %@ in the
   text (%@ is the macOS placeholder). The result goes into dst of size n. */
void loft_tf(char *dst, size_t n, const char *key, const char *arg) {
    const char *s = loft_t(key);
    const char *hit;
    size_t i = 0;
    if (!dst || n == 0) return;
    if (!arg) arg = "";
    hit = strstr(s, "%d");
    if (!hit) hit = strstr(s, "%@");
    if (!hit) {
        loft_strlcpy(dst, n, s);
        return;
    }
    while (s < hit && i + 1 < n) dst[i++] = *s++;
    s += 2;
    while (*arg && i + 1 < n) dst[i++] = *arg++;
    while (*s && i + 1 < n) dst[i++] = *s++;
    dst[i] = 0;
}

