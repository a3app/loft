/* Windows client, portable core (also built into the host tests): UI strings
   in ten languages. Keeps the Windows-specific pack and the shared macOS pack
   per language and looks a key up through both, falling back to English.
   win/app.c loads the pack files; json.c parses them. */
#include "l10n.h"

#include "json.h"
#include "util.h"

#include <string.h>

static const char *codes[LOFT_LANGS] = {"en", "fr", "de", "it", "pt", "ru", "vi", "zh", "ja", "ko"};
static const char *natives[LOFT_LANGS] = {
    "English", "Français", "Deutsch", "Italiano", "Português",
    "Русский", "Tiếng Việt", "中文", "日本語", "한국어"};

static LoftStrMap mac[LOFT_LANGS];
static LoftStrMap win[LOFT_LANGS];
static int cur = 5; /* Russian, matching the macOS client. */

const char *loft_lang_code(int i) {
    if (i < 0 || i >= LOFT_LANGS) return "ru";
    return codes[i];
}

const char *loft_lang_native(int i) {
    if (i < 0 || i >= LOFT_LANGS) return natives[5];
    return natives[i];
}

int loft_lang_index(const char *code) {
    int i;
    for (i = 0; i < LOFT_LANGS; i++) {
        if (loft_strieq(code, codes[i])) return i;
    }
    return -1;
}

void loft_l10n_reset(void) {
    int i;
    for (i = 0; i < LOFT_LANGS; i++) {
        loft_map_free(&mac[i]);
        loft_map_free(&win[i]);
    }
    cur = 5;
}

int loft_l10n_add_macos(const char *code, const char *text) {
    int i = loft_lang_index(code);
    if (i < 0 || !text) return 0;
    return loft_map_pack(&mac[i], text, "L10N_PACKS", code);
}

int loft_l10n_add_win(const char *code, const char *text) {
    int i = loft_lang_index(code);
    if (i < 0 || !text) return 0;
    return loft_map_pack(&win[i], text, "WIN_PACKS", code);
}

void loft_l10n_set(const char *code) {
    int i = loft_lang_index(code);
    cur = i < 0 ? 5 : i;
}

const char *loft_l10n_lang(void) { return codes[cur]; }

static const char *lookup(int lang, const char *key) {
    const char *s;
    if (lang < 0 || lang >= LOFT_LANGS) return 0;
    s = loft_map_get(&win[lang], key);
    if (s) return s;
    return 0;
}

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

