#include "common.h"

int store_root(wchar_t *d, int n) {
    wchar_t app[MAX_PATH];
    if (!d || n <= 0) return 0;
    d[0] = 0;
    if (FAILED(SHGetFolderPathW(NULL, CSIDL_APPDATA, NULL, 0, app))) return 0;
    path_join(d, n, app, L"loft");
    CreateDirectoryW(d, NULL);
    return d[0] != 0;
}

static void subdir(const wchar_t *name, wchar_t *d, int n) {
    wchar_t root[MAX_PATH];
    store_root(root, MAX_PATH);
    path_join(d, n, root, name);
    CreateDirectoryW(d, NULL);
}

static int id_taken(const char *id, int skip) {
    int i;
    for (i = 0; i < g.nprof; i++) {
        if (i != skip && loft_streq(g.prof[i].id, id)) return 1;
    }
    return 0;
}

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

static void profile_path(const char *id, wchar_t *d, int n) {
    wchar_t dir[MAX_PATH], wname[96];
    char name[96];
    subdir(L"profiles", dir, MAX_PATH);
    snprintf(name, sizeof name, "%s.yaml", id);
    widen(name, wname, 96);
    path_join(d, n, dir, wname);
}

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

int store_save_prefs(void) {
    wchar_t root[MAX_PATH], path[MAX_PATH];
    char buf[4096];
    if (g.active >= 0 && g.active < g.nprof) loft_strlcpy(g.prefs.active, sizeof g.prefs.active, g.prof[g.active].id);
    if (!loft_prefs_save(&g.prefs, buf, sizeof buf)) return 0;
    if (!store_root(root, MAX_PATH)) return 0;
    path_join(path, MAX_PATH, root, L"prefs.yaml");
    return file_write(path, buf);
}

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
