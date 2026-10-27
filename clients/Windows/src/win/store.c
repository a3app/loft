/* Windows client GUI: on-disk storage under %APPDATA%\loft (prefs.yaml,
   profiles/, active, routes.txt), plus import and export of profile files.
   Keys go to key.c; parsing and writing use the portable core in src/
   (profile.c, share.c, routes.c). */
#include "common.h"

/* store_root writes the path of the user's data folder %APPDATA%\loft into
   d, creating the folder if it is missing. Returns 0 when the AppData path
   cannot be found. */
int store_root(wchar_t *d, int n) {
    wchar_t app[MAX_PATH];
    if (!d || n <= 0) return 0;
    d[0] = 0;
    if (FAILED(SHGetFolderPathW(NULL, CSIDL_APPDATA, NULL, 0, app))) return 0;
    path_join(d, n, app, L"loft");
    CreateDirectoryW(d, NULL);
    return d[0] != 0;
}

/* subdir builds the path of a folder inside the data folder (for example
   "profiles") and creates it if needed. */
static void subdir(const wchar_t *name, wchar_t *d, int n) {
    wchar_t root[MAX_PATH];
    store_root(root, MAX_PATH);
    path_join(d, n, root, name);
    CreateDirectoryW(d, NULL);
}

/* id_taken reports whether a loaded profile other than index skip already
   uses this id. */
static int id_taken(const char *id, int skip) {
    int i;
    for (i = 0; i < g.nprof; i++) {
        if (i != skip && loft_streq(g.prof[i].id, id)) return 1;
    }
    return 0;
}

/* unique_id makes sure a new profile has an id no other profile uses. The
   id is the profile's file name and the name of its stored key, so two
   profiles must never share one. A missing id is made from the profile name;
   a taken id gets a "-2", "-3", ... suffix (tried up to 99). */
static void unique_id(LoftProfile *p) {
    char base[LOFT_ID], tryid[LOFT_ID];
    int n = 2;
    if (!p->id[0]) loft_slug(p->name[0] ? p->name : "loft", p->id, sizeof p->id);
    if (!p->id[0]) loft_strlcpy(p->id, sizeof p->id, "loft");
    if (!id_taken(p->id, -1)) return;
    loft_strlcpy(base, sizeof base, p->id);
    while (n < 100) {
        snprintf(tryid, sizeof tryid, "%.36s-%d", base, n);
        if (!id_taken(tryid, -1)) {
            loft_strlcpy(p->id, sizeof p->id, tryid);
            return;
        }
        n++;
    }
}

/* profile_path builds the file path profiles\<id>.yaml for a profile. */
static void profile_path(const char *id, wchar_t *d, int n) {
    wchar_t dir[MAX_PATH], wname[96];
    char name[96];
    subdir(L"profiles", dir, MAX_PATH);
    snprintf(name, sizeof name, "%s.yaml", id);
    widen(name, wname, 96);
    path_join(d, n, dir, wname);
}

/* store_load reads everything from disk into the global state g at start-up:
   prefs, split-routing lists, all profiles with their keys (from key.c),
   and which profile is active. Missing files leave the defaults in place.
   It also switches the UI language to the one in prefs. */
void store_load(void) {
    wchar_t root[MAX_PATH], path[MAX_PATH], dir[MAX_PATH];
    char *text;
    WIN32_FIND_DATAW fd;
    HANDLE h;
    int i;
    loft_prefs_default(&g.prefs);
    loft_routes_default(&g.routes);
    g.nprof = 0;
    g.active = -1;
    if (!store_root(root, MAX_PATH)) return;
    path_join(path, MAX_PATH, root, L"prefs.yaml");
    text = file_read(path);
    if (text) {
        loft_prefs_load(&g.prefs, text);
        free(text);
    }
    path_join(path, MAX_PATH, root, L"routes.txt");
    text = file_read(path);
    if (text) {
        loft_routes_load(&g.routes, text);
        free(text);
    } else {
        loft_routes_default(&g.routes);
    }
    /* Load every profiles\*.yaml; the key is stored apart and fetched by id. */
    subdir(L"profiles", dir, MAX_PATH);
    path_join(path, MAX_PATH, dir, L"*.yaml");
    h = FindFirstFileW(path, &fd);
    if (h != INVALID_HANDLE_VALUE) {
        do {
            wchar_t fp[MAX_PATH];
            LoftParsed ps;
            int n;
            if (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) continue;
            if (g.nprof >= LOFT_PROFILES) break;
            path_join(fp, MAX_PATH, dir, fd.cFileName);
            text = file_read(fp);
            if (!text) continue;
            memset(&ps, 0, sizeof ps);
            n = loft_parse_blob(text, NULL, &ps, 1);
            free(text);
            if (n <= 0 || !ps.p.id[0]) continue;
            g.prof[g.nprof] = ps.p;
            g.key[g.nprof][0] = 0;
            if (key_load(ps.p.id, g.key[g.nprof], LOFT_KEY)) g.prof[g.nprof].has_key = 1;
            loft_wipe(ps.key, sizeof ps.key);
            g.nprof++;
        } while (FindNextFileW(h, &fd));
        FindClose(h);
    }
    /* The "active" file names the profile selected last; fall back to the first. */
    path_join(path, MAX_PATH, root, L"active");
    text = file_read(path);
    if (text && text[0]) {
        loft_trim(text);
        loft_strlcpy(g.prefs.active, sizeof g.prefs.active, text);
    }
    free(text);
    for (i = 0; i < g.nprof; i++) {
        if (g.prefs.active[0] && loft_streq(g.prof[i].id, g.prefs.active)) g.active = i;
    }
    if (g.active < 0 && g.nprof > 0) g.active = 0;
    loft_l10n_set(g.prefs.lang);
}

/* store_save_prefs writes prefs.yaml, including the id of the active
   profile. Returns 1 on success. */
int store_save_prefs(void) {
    wchar_t root[MAX_PATH], path[MAX_PATH];
    char buf[4096];
    if (g.active >= 0 && g.active < g.nprof) loft_strlcpy(g.prefs.active, sizeof g.prefs.active, g.prof[g.active].id);
    if (!loft_prefs_save(&g.prefs, buf, sizeof buf)) return 0;
    if (!store_root(root, MAX_PATH)) return 0;
    path_join(path, MAX_PATH, root, L"prefs.yaml");
    return file_write(path, buf);
}

/* store_save_profile writes profile i to profiles\<id>.yaml. The key is
   never written into this file; it lives in key.c storage. */
int store_save_profile(int i) {
    wchar_t path[MAX_PATH];
    char *yaml;
    int ok;
    if (i < 0 || i >= g.nprof) return 0;
    yaml = (char *)malloc(16384);
    if (!yaml) return 0;
    ok = loft_profile_yaml(&g.prof[i], NULL, yaml, 16384);
    if (!ok) {
        free(yaml);
        return 0;
    }
    profile_path(g.prof[i].id, path, MAX_PATH);
    ok = file_write(path, yaml);
    free(yaml);
    return ok;
}

/* store_save_active records which profile is active, both in the "active"
   file and in prefs.yaml. */
int store_save_active(void) {
    wchar_t root[MAX_PATH], path[MAX_PATH];
    char line[LOFT_ID + 2];
    if (g.active >= 0 && g.active < g.nprof) loft_strlcpy(g.prefs.active, sizeof g.prefs.active, g.prof[g.active].id);
    snprintf(line, sizeof line, "%s\n", g.prefs.active);
    if (!store_root(root, MAX_PATH)) return 0;
    path_join(path, MAX_PATH, root, L"active");
    store_save_prefs();
    return file_write(path, line);
}

/* store_save_routes writes the split-routing lists to routes.txt. */
int store_save_routes(void) {
    wchar_t root[MAX_PATH], path[MAX_PATH];
    char *buf = (char *)malloc(256 * 1024);
    int ok;
    if (!buf) return 0;
    ok = loft_routes_save(&g.routes, buf, 256 * 1024);
    if (!ok) {
        free(buf);
        return 0;
    }
    if (!store_root(root, MAX_PATH)) {
        free(buf);
        return 0;
    }
    path_join(path, MAX_PATH, root, L"routes.txt");
    ok = file_write(path, buf);
    free(buf);
    return ok;
}

/* store_add_parsed adds one imported profile: it gets a unique id, its key
   (if the import carried one) is saved through key.c and wiped from the
   parsed copy, the profile file is written and the new profile becomes
   active. Returns the new index, or -1 when the profile list is full. */
int store_add_parsed(LoftParsed *p) {
    int i;
    if (!p || g.nprof >= LOFT_PROFILES) return -1;
    i = g.nprof;
    g.prof[i] = p->p;
    unique_id(&g.prof[i]);
    g.key[i][0] = 0;
    if (p->key[0]) {
        key_save(g.prof[i].id, p->key);
        loft_strlcpy(g.key[i], LOFT_KEY, p->key);
        g.prof[i].has_key = 1;
        loft_wipe(p->key, sizeof p->key);
    }
    g.nprof++;
    store_save_profile(i);
    g.active = i;
    store_save_active();
    return i;
}

/* store_delete_profile removes profile i: its file and stored key are
   deleted, the in-memory key is wiped, and later profiles move up one place.
   The active index is adjusted so it still points at the same profile (or at
   the first one when the active profile was deleted). */
void store_delete_profile(int i) {
    wchar_t path[MAX_PATH];
    int j;
    if (i < 0 || i >= g.nprof) return;
    profile_path(g.prof[i].id, path, MAX_PATH);
    DeleteFileW(path);
    key_delete(g.prof[i].id);
    loft_wipe(g.key[i], sizeof g.key[i]);
    for (j = i + 1; j < g.nprof; j++) {
        g.prof[j - 1] = g.prof[j];
        memcpy(g.key[j - 1], g.key[j], LOFT_KEY);
    }
    g.nprof--;
    if (g.nprof > 0) {
        loft_wipe(g.key[g.nprof], LOFT_KEY);
        memset(&g.prof[g.nprof], 0, sizeof g.prof[0]);
    }
    if (g.active == i) g.active = g.nprof ? 0 : -1;
    else if (g.active > i) g.active--;
    store_save_active();
}

/* import_path imports profiles from a share link (loft://, vless://,
   trojan://, sstp://) or from a file such as a .loft file. One link or file
   may hold several profiles (up to 8 here). An import that yields nothing
   logs an error to the journal. The first successful import also marks the
   first-run screen as done, then the main window opens. */
void import_path(const wchar_t *path) {
    char *text = NULL;
    char narrow_path[2048];
    char name[256];
    LoftParsed ps[8];
    int n = 0, i;
    const wchar_t *base;
    if (!path || !path[0]) return;
    memset(ps, 0, sizeof ps);
    if (wcsncmp(path, L"loft://", 7) == 0 || wcsncmp(path, L"vless://", 8) == 0 || wcsncmp(path, L"trojan://", 9) == 0 ||
        wcsncmp(path, L"sstp://", 7) == 0) {
        narrow(path, narrow_path, (int)sizeof narrow_path);
        n = loft_parse_share(narrow_path, NULL, ps, 8);
    } else {
        text = file_read(path);
        if (!text) return;
        base = wcsrchr(path, L'\\');
        base = base ? base + 1 : path;
        narrow(base, name, (int)sizeof name);
        n = loft_parse_share(text, name, ps, 8);
        free(text);
    }
    if (n <= 0) {
        journal_add(loft_t("import.body"));
        return;
    }
    for (i = 0; i < n; i++) store_add_parsed(&ps[i]);
    if (!g.prefs.first_done) {
        g.prefs.first_done = 1;
        store_save_prefs();
    }
    open_main();
    redraw_all();
}

/* import_dialog shows the standard Open File dialog and imports the chosen
   file. */
void import_dialog(HWND owner) {
    wchar_t file[MAX_PATH];
    OPENFILENAMEW ofn;
    file[0] = 0;
    memset(&ofn, 0, sizeof ofn);
    ofn.lStructSize = sizeof ofn;
    ofn.hwndOwner = owner;
    ofn.lpstrFilter = L"Loft\0*.loft;*.yaml;*.yml;*.vpn;*.bat\0All\0*.*\0";
    ofn.lpstrFile = file;
    ofn.nMaxFile = MAX_PATH;
    ofn.Flags = OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST;
    if (GetOpenFileNameW(&ofn)) import_path(file);
}

/* export_profile saves profile idx as a .loft file. The exported file
   contains the key, so the user must first confirm a warning. The YAML
   buffer is wiped after writing because it held the key. */
void export_profile(HWND owner, int idx) {
    wchar_t file[MAX_PATH], msg[640], title[128], wname[160];
    char name[160];
    char *yaml;
    OPENFILENAMEW ofn;
    if (idx < 0 || idx >= g.nprof) return;
    widen(loft_t("export.warn"), msg, 640);
    widen(loft_t("export.title"), title, 128);
    if (MessageBoxW(owner, msg, title, MB_OKCANCEL | MB_ICONWARNING) != IDOK) return;
    snprintf(name, sizeof name, "%s.loft", g.prof[idx].name[0] ? g.prof[idx].name : g.prof[idx].id);
    widen(name, wname, 160);
    file[0] = 0;
    wcsncpy(file, wname, MAX_PATH - 1);
    file[MAX_PATH - 1] = 0;
    memset(&ofn, 0, sizeof ofn);
    ofn.lStructSize = sizeof ofn;
    ofn.hwndOwner = owner;
    ofn.lpstrFilter = L"Loft (*.loft)\0*.loft\0All\0*.*\0";
    ofn.lpstrFile = file;
    ofn.nMaxFile = MAX_PATH;
    ofn.Flags = OFN_OVERWRITEPROMPT | OFN_PATHMUSTEXIST;
    ofn.lpstrDefExt = L"loft";
    if (!GetSaveFileNameW(&ofn)) return;
    yaml = (char *)malloc(16384);
    if (!yaml) return;
    if (!loft_profile_yaml(&g.prof[idx], g.key[idx], yaml, 16384)) {
        free(yaml);
        return;
    }
    file_write(file, yaml);
    loft_wipe(yaml, strlen(yaml));
    free(yaml);
}
