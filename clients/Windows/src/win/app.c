/* Windows client GUI: the global App state g, DPI scaling, paths next to the
   exe, loading the language packs, the journal (in-app log, also written to
   journal.log), shared fonts and brushes, and app start-up and shutdown.
   main.c calls app_boot; the rest of src/win reads g. */
#include "common.h"

/* g is the one global App: settings, profiles, keys, sessions, open windows,
   GDI objects and the journal. All of it is touched only from the GUI thread;
   worker threads post window messages instead. */
App g;

/* sc scales a size given in 96-DPI pixels to the screen DPI read at start.
   Windows 7 has no per-monitor DPI API, so one system DPI is used everywhere. */
int sc(int px) {
    int d = g.dpi > 0 ? g.dpi : 96;
    return MulDiv(px, d, 96);
}

/* exe_dir writes the folder that holds loft.exe into d. */
void exe_dir(wchar_t *d, int n) {
    wchar_t *slash;
    if (!d || n <= 0) return;
    d[0] = 0;
    if (!GetModuleFileNameW(NULL, d, n)) return;
    slash = wcsrchr(d, L'\\');
    if (slash) *slash = 0;
}

/* exe_file writes the full path of file name next to loft.exe into d. */
void exe_file(wchar_t *d, int n, const wchar_t *name) {
    wchar_t dir[MAX_PATH];
    exe_dir(dir, MAX_PATH);
    path_join(d, n, dir, name);
}

/* load_lang reads the language packs from the lang folder next to the exe:
   one <code>.lang per language with the texts shared with the macOS client,
   plus win.lang with the Windows-only texts for all languages. Missing files
   are skipped (keys then show as is). Finally selects the language from the
   settings. */
static void load_lang(void) {
    wchar_t dir[MAX_PATH], path[MAX_PATH];
    static const char *codes[] = {"en", "fr", "de", "it", "pt", "ru", "vi", "zh", "ja", "ko"};
    int i;
    exe_dir(dir, MAX_PATH);
    path_join(dir, MAX_PATH, dir, L"lang");
    for (i = 0; i < 10; i++) {
        wchar_t name[32];
        char *text;
        char fn[32];
        snprintf(fn, sizeof fn, "%s.lang", codes[i]);
        widen(fn, name, 32);
        path_join(path, MAX_PATH, dir, name);
        text = file_read(path);
        if (!text) continue;
        loft_l10n_add_macos(codes[i], text);
        free(text);
    }
    path_join(path, MAX_PATH, dir, L"win.lang");
    {
        char *text = file_read(path);
        if (text) {
            for (i = 0; i < 10; i++) loft_l10n_add_win(codes[i], text);
            free(text);
        }
    }
    loft_l10n_set(g.prefs.lang);
}

/* journal_file appends line with a timestamp to journal.log in the store
   folder, but only when diagnostics are on (g.prefs.diag). Past about 1.5 MB
   the log rotates: journal.log -> .1 -> .2, and the oldest is dropped. */
static void journal_file(const char *line) {
    wchar_t root[MAX_PATH], path[MAX_PATH], older[MAX_PATH];
    HANDLE h;
    DWORD sz = 0, w = 0;
    char buf[320];
    SYSTEMTIME st;
    if (!g.prefs.diag) return;
    if (!store_root(root, MAX_PATH)) return;
    path_join(path, MAX_PATH, root, L"journal.log");
    h = CreateFileW(path, FILE_APPEND_DATA | FILE_READ_ATTRIBUTES, FILE_SHARE_READ, NULL, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return;
    sz = GetFileSize(h, NULL);
    if (sz != INVALID_FILE_SIZE && sz > 1500u * 1000u) {
        wchar_t a[MAX_PATH], b[MAX_PATH];
        CloseHandle(h);
        path_join(older, MAX_PATH, root, L"journal.log.2");
        path_join(a, MAX_PATH, root, L"journal.log.1");
        path_join(b, MAX_PATH, root, L"journal.log");
        DeleteFileW(older);
        MoveFileW(a, older);
        MoveFileW(b, a);
        h = CreateFileW(path, FILE_APPEND_DATA, FILE_SHARE_READ, NULL, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
        if (h == INVALID_HANDLE_VALUE) return;
    }
    GetLocalTime(&st);
    snprintf(buf, sizeof buf, "%04u-%02u-%02u %02u:%02u:%02u %s\r\n", st.wYear, st.wMonth, st.wDay, st.wHour, st.wMinute, st.wSecond, line);
    WriteFile(h, buf, (DWORD)strlen(buf), &w, NULL);
    CloseHandle(h);
}

/* journal_add records one event in the journal: an in-memory ring of the last
   LOFT_JOURNAL lines shown in the Journal window, plus the file when
   diagnostics are on. Then refreshes the open windows. */
void journal_add(const char *line) {
    if (!line || !line[0]) return;
    loft_strlcpy(g.journal[g.nj % LOFT_JOURNAL], 240, line);
    g.nj++;
    journal_file(line);
    journal_sync();
    redraw_all();
}

/* fonts_make creates the shared fonts once, sized for the current DPI:
   Segoe UI regular, medium and semibold for the UI, Consolas for addresses
   and logs, and a small Segoe UI. */
void fonts_make(void) {
    LOGFONTW lf;
    if (g.f_ui) return;
    memset(&lf, 0, sizeof lf);
    lf.lfHeight = -sc(13);
    lf.lfWeight = FW_NORMAL;
    lf.lfQuality = CLEARTYPE_QUALITY;
    lf.lfCharSet = DEFAULT_CHARSET;
    wcscpy(lf.lfFaceName, L"Segoe UI");
    g.f_ui = CreateFontIndirectW(&lf);
    lf.lfHeight = -sc(13);
    lf.lfWeight = FW_MEDIUM;
    g.f_med = CreateFontIndirectW(&lf);
    lf.lfHeight = -sc(16);
    lf.lfWeight = FW_SEMIBOLD;
    g.f_semi = CreateFontIndirectW(&lf);
    lf.lfHeight = -sc(12);
    lf.lfWeight = FW_NORMAL;
    wcscpy(lf.lfFaceName, L"Consolas");
    g.f_mono = CreateFontIndirectW(&lf);
    lf.lfHeight = -sc(11);
    wcscpy(lf.lfFaceName, L"Segoe UI");
    g.f_small = CreateFontIndirectW(&lf);
}

/* brushes_make creates the shared brushes and the edge pen once, one per
   palette colour from common.h, so painting code never creates GDI objects. */
void brushes_make(void) {
    if (g.br_ink) return;
    g.br_ink = CreateSolidBrush(C_INK);
    g.br_ink2 = CreateSolidBrush(C_INK2);
    g.br_field = CreateSolidBrush(C_FIELD);
    g.br_fly = CreateSolidBrush(C_FLY);
    g.br_copper = CreateSolidBrush(C_COPPER);
    g.br_lamp = CreateSolidBrush(C_LAMP);
    g.br_err = CreateSolidBrush(C_ERR);
    g.br_slate = CreateSolidBrush(C_SLATE);
    g.br_edge = CreateSolidBrush(C_EDGE);
    g.br_close = CreateSolidBrush(C_CLOSE);
    g.pen_edge = CreatePen(PS_SOLID, 1, C_EDGE);
}

/* fonts_free deletes all shared fonts, brushes and the pen and clears them. */
void fonts_free(void) {
    if (g.f_ui) DeleteObject(g.f_ui);
    if (g.f_med) DeleteObject(g.f_med);
    if (g.f_semi) DeleteObject(g.f_semi);
    if (g.f_mono) DeleteObject(g.f_mono);
    if (g.f_small) DeleteObject(g.f_small);
    if (g.br_ink) DeleteObject(g.br_ink);
    if (g.br_ink2) DeleteObject(g.br_ink2);
    if (g.br_field) DeleteObject(g.br_field);
    if (g.br_fly) DeleteObject(g.br_fly);
    if (g.br_copper) DeleteObject(g.br_copper);
    if (g.br_lamp) DeleteObject(g.br_lamp);
    if (g.br_err) DeleteObject(g.br_err);
    if (g.br_slate) DeleteObject(g.br_slate);
    if (g.br_edge) DeleteObject(g.br_edge);
    if (g.br_close) DeleteObject(g.br_close);
    if (g.pen_edge) DeleteObject(g.pen_edge);
    g.f_ui = g.f_med = g.f_semi = g.f_mono = g.f_small = NULL;
    g.br_ink = g.br_ink2 = g.br_field = g.br_fly = g.br_copper = NULL;
    g.br_lamp = g.br_err = g.br_slate = g.br_edge = g.br_close = NULL;
    g.pen_edge = NULL;
}

/* app_boot prepares the app before any window opens: reads the DPI, loads
   settings, profiles, keys and routes from disk (store_load), then the
   language packs, fonts and brushes. */
void app_boot(void) {
    HDC dc = GetDC(NULL);
    g.dpi = dc ? GetDeviceCaps(dc, LOGPIXELSX) : 96;
    if (g.dpi < 96) g.dpi = 96;
    if (dc) ReleaseDC(NULL, dc);
    g.active = -1;
    loft_prefs_default(&g.prefs);
    store_load();
    load_lang();
    fonts_make();
    brushes_make();
}

/* app_shutdown cleans up on exit: removes the tray icon, stops every
   loft-front process, wipes keys from memory and frees GDI objects. Unless
   "hold" is on (block traffic while disconnected), it also tells the service
   to release the hold and close the tunnel adapter. */
void app_shutdown(void) {
    int i;
    tray_remove();
    for (i = 0; i < LOFT_SESS; i++) {
        g.sess[i].user_stop = 1;
        if (g.sess[i].proc) TerminateProcess(g.sess[i].proc, 0);
        if (g.sess[i].job) CloseHandle(g.sess[i].job);
    }
    for (i = 0; i < LOFT_PROFILES; i++) loft_wipe(g.key[i], sizeof g.key[i]);
    if (!g.prefs.hold) {
        char r[64];
        svc_call("hold 0", r, (int)sizeof r);
        svc_call("tun-close", r, (int)sizeof r);
    }
    for (i = 0; i < 4; i++)
        if (g.icon[i]) DestroyIcon(g.icon[i]);
    fonts_free();
    WSACleanup();
}
