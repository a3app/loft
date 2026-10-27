/* Windows client GUI: per-user shell integration in the HKCU registry.
   assoc_apply makes Windows open .loft profile files and loft:// links with
   this loft.exe (or removes that), and login_apply adds or removes the
   "start at sign-in" entry. Both follow the switches in the settings window
   (g.prefs) and are called at start-up and when those switches change. */
#include "common.h"

/* reg_sz creates key root\sub if needed and sets its default value to the
   string value. Returns 1 on success. */
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

/* "loft.exe",-2 is the .loft document icon; -1 is the app icon. */
static void exe_icon(wchar_t *d, int n, int id) {
    wchar_t p[MAX_PATH];
    GetModuleFileNameW(NULL, p, MAX_PATH);
    _snwprintf(d, (size_t)n, L"\"%s\",-%d", p, id);
    if (n > 0) d[n - 1] = 0;
}

/* exe_cmd writes the shell "open" command for this exe: "path" "%1", where
   Windows puts the file or link in place of %1. */
static void exe_cmd(wchar_t *d, int n) {
    wchar_t p[MAX_PATH];
    GetModuleFileNameW(NULL, p, MAX_PATH);
    _snwprintf(d, (size_t)n, L"\"%s\" \"%%1\"", p);
    if (n > 0) d[n - 1] = 0;
}

/* assoc_apply registers or removes the .loft file type (Loft.Profile) and the
   loft:// URL protocol under HKCU\Software\Classes, following
   g.prefs.assoc_file and g.prefs.assoc_link. Registration always points at the
   running exe, so a moved portable copy fixes itself on the next start. If the
   installer left an "assoc.off" file next to the exe (the user unticked the
   association box), both switches are turned off once and the marker is
   deleted. */
void assoc_apply(void) {
    wchar_t cmd[MAX_PATH + 32], mark[MAX_PATH], icon[MAX_PATH + 16];
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
        exe_icon(icon, MAX_PATH + 16, 2);
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\Loft.Profile\\DefaultIcon", icon);
    } else {
        RegDeleteTreeW(HKEY_CURRENT_USER, L"Software\\Classes\\.loft");
        RegDeleteTreeW(HKEY_CURRENT_USER, L"Software\\Classes\\Loft.Profile");
    }
    if (g.prefs.assoc_link) {
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\loft", L"URL:loft");
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\loft\\URL Protocol", L"");
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\loft\\shell\\open\\command", cmd);
        exe_icon(icon, MAX_PATH + 16, 1);
        reg_sz(HKEY_CURRENT_USER, L"Software\\Classes\\loft\\DefaultIcon", icon);
    } else {
        RegDeleteTreeW(HKEY_CURRENT_USER, L"Software\\Classes\\loft");
    }
}

/* login_apply adds or removes the "Loft" value under the current user's
   Run key, so Windows starts loft.exe --tray (straight to the tray, no
   window) at sign-in when g.prefs.login is on. */
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
