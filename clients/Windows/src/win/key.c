/* Windows client GUI: stores each profile's secret key separately from the
   profile file, encrypted with Windows DPAPI (CryptProtectData) under
   %APPDATA%\loft\keys, so only the same Windows user can read it.
   Used by store.c. */
#include "common.h"

static void key_path(const char *id, wchar_t *d, int n) {
    wchar_t root[MAX_PATH], dir[MAX_PATH], wname[80];
    char name[80];
    store_root(root, MAX_PATH);
    path_join(dir, MAX_PATH, root, L"keys");
    CreateDirectoryW(dir, NULL);
    snprintf(name, sizeof name, "%s.bin", id && id[0] ? id : "key");
    widen(name, wname, 80);
    path_join(d, n, dir, wname);
}

int key_save(const char *id, const char *key) {
    DATA_BLOB in, out;
    wchar_t path[MAX_PATH];
    int ok;
    if (!id || !key || !key[0]) return 0;
    memset(&in, 0, sizeof in);
    memset(&out, 0, sizeof out);
    in.pbData = (BYTE *)key;
    in.cbData = (DWORD)strlen(key);
    if (!CryptProtectData(&in, L"loft", NULL, NULL, NULL, CRYPTPROTECT_UI_FORBIDDEN, &out)) return 0;
    key_path(id, path, MAX_PATH);
    ok = file_write_bin(path, out.pbData, (int)out.cbData);
    if (out.pbData) {
        loft_wipe(out.pbData, out.cbData);
        LocalFree(out.pbData);
    }
    return ok;
}

int key_load(const char *id, char *key, int n) {
    DATA_BLOB in, out;
    wchar_t path[MAX_PATH];
    unsigned char *buf = NULL;
    int len = 0;
    if (!id || !key || n <= 1) return 0;
    key[0] = 0;
    key_path(id, path, MAX_PATH);
    if (!file_read_bin(path, &buf, &len) || !buf || len <= 0) {
        free(buf);
        return 0;
    }
    memset(&in, 0, sizeof in);
    memset(&out, 0, sizeof out);
    in.pbData = buf;
    in.cbData = (DWORD)len;
    if (!CryptUnprotectData(&in, NULL, NULL, NULL, NULL, CRYPTPROTECT_UI_FORBIDDEN, &out)) {
        loft_wipe(buf, (size_t)len);
        free(buf);
        return 0;
    }
    if ((int)out.cbData >= n) out.cbData = (DWORD)n - 1;
    memcpy(key, out.pbData, out.cbData);
    key[out.cbData] = 0;
    loft_wipe(out.pbData, out.cbData);
    LocalFree(out.pbData);
    loft_wipe(buf, (size_t)len);
    free(buf);
    return key[0] != 0;
}

void key_delete(const char *id) {
    wchar_t path[MAX_PATH];
    if (!id || !id[0]) return;
    key_path(id, path, MAX_PATH);
    DeleteFileW(path);
}
