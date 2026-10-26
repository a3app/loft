#include "common.h"

static int reg_sz(HKEY root, const wchar_t *sub, const wchar_t *value) {
    HKEY k = NULL;
    LONG e;
    if (!value) value = L"";
    e = RegCreateKeyExW(root, sub, 0, NULL, 0, KEY_SET_VALUE, NULL, &k, NULL);
    if (e != ERROR_SUCCESS) return 0;
    e = RegSetValueExW(k, NULL, 0, REG_SZ, (const BYTE *)value, (DWORD)((wcslen(value) + 1) * sizeof(wchar_t)));
    RegCloseKey(k);
    return e == ERROR_SUCCESS;
}

static void exe_cmd(wchar_t *d, int n) {
    wchar_t p[MAX_PATH];
    GetModuleFileNameW(NULL, p, MAX_PATH);
    _snwprintf(d, (size_t)n, L"\"%s\" \"%%1\"", p);
    if (n > 0) d[n - 1] = 0;
}

void assoc_apply(void) {
    wchar_t cmd[MAX_PATH + 32], mark[MAX_PATH];
    exe_file(mark, MAX_PATH, L"assoc.off");
    if (GetFileAttributesW(mark) != INVALID_FILE_ATTRIBUTES) {
        g.prefs.assoc_file = 0;
        g.prefs.assoc_link = 0;
        store_save_prefs();
        DeleteFileW(mark);
    }
    exe_cmd(cmd, MAX_PATH + 32);
    if (g.prefs.assoc_file) {
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\.loft", L"Loft.Profile");
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\Loft.Profile", L"Loft profile");
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\Loft.Profile\\shell\\open\\command", cmd);
    } else {
        RegDeleteTreeW(HKEY_CURRENT_USER, L"Software\\Classes\\.loft");
        RegDeleteTreeW(HKEY_CURRENT_USER, L"Software\\Classes\\Loft.Profile");
    }
    if (g.prefs.assoc_link) {
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\loft", L"URL:loft");
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\loft\\URL Protocol", L"");
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\loft\\shell\\open\\command", cmd);
    } else {
        RegDeleteTreeW(HKEY_CURRENT_USER, L"Software\\Classes\\loft");
    }
}

void login_apply(void) {
    HKEY k = NULL;
    wchar_t p[MAX_PATH], cmd[MAX_PATH + 16];
    if (RegOpenKeyExW(HKEY_CURRENT_USER, L"Software\\Microsoft\\Windows\\CurrentVersion\\Run", 0, KEY_SET_VALUE, &k) != ERROR_SUCCESS)
        return;
    if (g.prefs.login) {
        GetModuleFileNameW(NULL, p, MAX_PATH);
        _snwprintf(cmd, MAX_PATH + 16, L"\"%s\" --tray", p);
        cmd[MAX_PATH + 15] = 0;
        RegSetValueExW(k, L"Loft", 0, REG_SZ, (const BYTE *)cmd, (DWORD)((wcslen(cmd) + 1) * sizeof(wchar_t)));
    } else {
        RegDeleteValueW(k, L"Loft");
    }
    RegCloseKey(k);
}
