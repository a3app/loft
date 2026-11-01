/* loft-setup: copy the client into Program Files, register the loft service, write the uninstaller.
   Windows 7 and later. No MSI. The exe carries a stored zip after the image (footer LOFTPK01).
   asInvoker: UAC runs only when the user presses Install.

   How it is built: the Makefile compiles this file into loft-setup-stub.exe, then
   setup/pack_setup.py appends a zip of the payload (loft.exe, loft-svc.exe,
   wintun.dll, lang, tray, ...) and a 16-byte footer. At run time the exe reads that
   zip from its own file. The service itself is registered by running
   "loft-svc.exe --install" (svc/loft-svc.c). After install, a copy of this exe stays
   in the install folder as loft-setup.exe and serves as the uninstaller. */
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601
#endif
#ifndef WINVER
#define WINVER 0x0601
#endif
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <shellapi.h>
#include <shlobj.h>
#include <objbase.h>
#include <userenv.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#ifndef LOFT_VERSION
#define LOFT_VERSION "1.0.0"
#endif

/* Control id of the install-folder edit box. */
#define ID_DIR 10

/* Window and run state.
   g_lang      - UI language: 0 English, 1 Russian, 2 Chinese (from the Windows UI).
   g_uninstall - this run removes Loft instead of installing it.
   g_auto      - start the work at once without a click; set in the elevated copy
                 that on_go starts (--do / --do-uninstall).
   g_open      - start Loft when the install finishes (checkbox).
   g_assoc     - open .loft files and loft:// links in Loft (checkbox).
   g_busy      - work is running; ignore more clicks.
   g_fail      - the last attempt failed; the exit code and status color show it.
   g_dir       - install folder; g_status - the status line under the button.
   g_edit, g_field, g_font - folder edit box, dark field brush, UI font. */
static int g_dpi = 96;
static int g_lang;
static int g_uninstall;
static int g_auto;
static int g_open = 1;
static int g_assoc = 1;
static int g_busy;
static int g_fail;
static wchar_t g_dir[MAX_PATH];
static wchar_t g_status[400];
static HWND g_edit;
static HBRUSH g_field;
static HFONT g_font;

/* sc scales a size given at 96 DPI to the screen DPI. */
static int sc(int px) { return MulDiv(px, g_dpi, 96); }

/* tr picks the text for the current UI language. The installer has no lang
   files of its own, so every string carries all three translations. */
static const wchar_t *tr(const wchar_t *en, const wchar_t *ru, const wchar_t *zh) {
    if (g_lang == 1) return ru;
    if (g_lang == 2) return zh;
    return en;
}

/* set_status replaces the status line and asks the window to repaint. */
static void set_status(HWND hwnd, const wchar_t *s) {
    wcsncpy(g_status, s ? s : L"", 399);
    g_status[399] = 0;
    if (hwnd) InvalidateRect(hwnd, NULL, FALSE);
}

/* u32 and u16 read little-endian integers, the byte order of zip headers. */
static unsigned u32(const unsigned char *p) {
    return (unsigned)p[0] | ((unsigned)p[1] << 8) | ((unsigned)p[2] << 16) | ((unsigned)p[3] << 24);
}

static unsigned u16(const unsigned char *p) { return (unsigned)p[0] | ((unsigned)p[1] << 8); }

/* crc32_update continues a zip-style CRC-32 over n more bytes (start with 0).
   Used to check that each extracted file matches the zip. */
static unsigned crc32_update(unsigned crc, const unsigned char *p, unsigned n) {
    unsigned i, k;
    crc = ~crc;
    for (i = 0; i < n; i++) {
        crc ^= p[i];
        for (k = 0; k < 8; k++) crc = (crc >> 1) ^ (0xEDB88320u & (0u - (crc & 1u)));
    }
    return ~crc;
}

/* is_admin tells whether this process already runs as an administrator. */
static int is_admin(void) {
    BOOL yes = FALSE;
    SID_IDENTIFIER_AUTHORITY nt = SECURITY_NT_AUTHORITY;
    PSID sid = NULL;
    if (!AllocateAndInitializeSid(&nt, 2, SECURITY_BUILTIN_DOMAIN_RID, DOMAIN_ALIAS_RID_ADMINS, 0, 0, 0, 0, 0, 0, &sid))
        return 0;
    CheckTokenMembership(NULL, sid, &yes);
    FreeSid(sid);
    return yes ? 1 : 0;
}

/* read_at reads exactly n bytes at byte offset off. Returns 0 on a short read. */
static int read_at(HANDLE h, unsigned long long off, void *buf, DWORD n) {
    LARGE_INTEGER li;
    DWORD got = 0;
    li.QuadPart = (LONGLONG)off;
    if (!SetFilePointerEx(h, li, NULL, FILE_BEGIN)) return 0;
    while (got < n) {
        DWORD r = 0;
        if (!ReadFile(h, (unsigned char *)buf + got, n - got, &r, NULL) || r == 0) return 0;
        got += r;
    }
    return 1;
}

/* self_path gets the full path of this exe. */
static void self_path(wchar_t *d, int n) {
    d[0] = 0;
    GetModuleFileNameW(NULL, d, n);
    if (n > 0) d[n - 1] = 0;
}

/* parent_dir cuts a path back to its folder, in place. */
static void parent_dir(wchar_t *path) {
    wchar_t *s = wcsrchr(path, L'\\');
    if (s) *s = 0;
}

/* payload finds the zip appended to this exe. The last 16 bytes of the file are
   an 8-byte little-endian zip size and the tag "LOFTPK01" (written by
   pack_setup.py); the zip sits right before them. On success *out is an open
   handle to this exe and base/size locate the zip inside it. */
static int payload(HANDLE *out, unsigned long long *base, unsigned long long *size) {
    wchar_t path[MAX_PATH];
    unsigned char foot[16];
    LARGE_INTEGER sz;
    unsigned long long n;
    HANDLE h;
    self_path(path, MAX_PATH);
    h = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return 0;
    if (!GetFileSizeEx(h, &sz) || sz.QuadPart < 16) {
        CloseHandle(h);
        return 0;
    }
    n = (unsigned long long)sz.QuadPart;
    if (!read_at(h, n - 16, foot, 16) || memcmp(foot + 8, "LOFTPK01", 8) != 0) {
        CloseHandle(h);
        return 0;
    }
    *size = (unsigned long long)u32(foot) | ((unsigned long long)u32(foot + 4) << 32);
    if (*size < 22 || *size + 16 > n) {
        CloseHandle(h);
        return 0;
    }
    *base = n - 16 - *size;
    *out = h;
    return 1;
}

/* mkdir_parents creates every folder on the way to path (not the last part).
   It skips the drive prefix "C:\". The string is changed and restored. */
static void mkdir_parents(wchar_t *path) {
    wchar_t *p;
    for (p = path + 3; *p; p++) {
        if (*p != L'\\') continue;
        *p = 0;
        CreateDirectoryW(path, NULL);
        *p = L'\\';
    }
}

/* safe_path turns a zip entry name into a path under dest. Absolute names, drive
   letters and ".." parts are rejected, so a damaged or hostile zip cannot write
   outside the install folder. "/" becomes "\" and "." parts are dropped. */
static int safe_path(const wchar_t *dest, const char *name, wchar_t *out, int outn) {
    wchar_t rel[260], clean[260];
    wchar_t *p;
    int i, ci = 0;
    if (!name || !name[0] || name[0] == '/' || name[0] == '\\' || strchr(name, ':')) return 0;
    if (!MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, name, -1, rel, 260)) return 0;
    for (i = 0; rel[i]; i++)
        if (rel[i] == L'/') rel[i] = L'\\';
    p = rel;
    while (*p) {
        wchar_t *start = p;
        int len;
        while (*p && *p != L'\\') p++;
        len = (int)(p - start);
        if (*p == L'\\') p++;
        if (len == 0 || (len == 1 && start[0] == L'.')) continue;
        if (len == 2 && start[0] == L'.' && start[1] == L'.') return 0;
        if (ci && ci < 259) clean[ci++] = L'\\';
        if (ci + len >= 259) return 0;
        memcpy(clean + ci, start, (size_t)len * sizeof(wchar_t));
        ci += len;
    }
    clean[ci] = 0;
    if (!clean[0] || (int)(wcslen(dest) + 1 + wcslen(clean)) >= outn) return 0;
    _snwprintf(out, (size_t)outn, L"%s\\%s", dest, clean);
    out[outn - 1] = 0;
    return 1;
}

/* write_stored copies n bytes of an uncompressed zip entry to path in 64 KiB
   chunks and returns 1 only when the CRC-32 matches the zip header. */
static int write_stored(HANDLE h, unsigned long long off, unsigned n, unsigned expect, const wchar_t *path) {
    wchar_t copy[MAX_PATH];
    HANDLE out;
    unsigned crc = 0, left = n;
    unsigned char buf[65536];
    wcsncpy(copy, path, MAX_PATH - 1);
    copy[MAX_PATH - 1] = 0;
    mkdir_parents(copy);
    out = CreateFileW(path, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
    if (out == INVALID_HANDLE_VALUE) return 0;
    while (left) {
        DWORD wr = 0;
        unsigned chunk = left > sizeof buf ? (unsigned)sizeof buf : left;
        if (!read_at(h, off, buf, chunk) || !WriteFile(out, buf, chunk, &wr, NULL) || wr != chunk) {
            CloseHandle(out);
            return 0;
        }
        crc = crc32_update(crc, buf, chunk);
        off += chunk;
        left -= chunk;
    }
    CloseHandle(out);
    return crc == expect;
}

/* extract unpacks the payload zip into dest. It is a small zip reader that
   accepts only what pack_setup.py writes: stored (uncompressed) entries with the
   sizes in the local header, each up to 64 MiB. It walks the local file headers
   and stops with success at the central directory. Anything else (compression,
   bad sizes, a bad name, a CRC mismatch) stops with "The payload is damaged". */
static int extract(HWND hwnd, const wchar_t *dest) {
    HANDLE h = NULL;
    unsigned long long base = 0, size = 0, pos = 0;
    if (!payload(&h, &base, &size)) {
        set_status(hwnd, tr(L"This installer has no payload.", L"В установщике нет данных.", L"安装包里没有数据。"));
        return 0;
    }
    CreateDirectoryW(dest, NULL);
    while (pos + 30 <= size) {
        unsigned char hdr[30];
        char name[260];
        wchar_t path[MAX_PATH], shown[120];
        unsigned sig, method, flags, namelen, extra, comp, uncomp, crc;
        if (!read_at(h, base + pos, hdr, 30)) break;
        sig = u32(hdr);
        if (sig == 0x02014b50u || sig == 0x06054b50u) {
            CloseHandle(h);
            return 1;
        }
        if (sig != 0x04034b50u) break;
        flags = u16(hdr + 6);
        method = u16(hdr + 8);
        crc = u32(hdr + 14);
        comp = u32(hdr + 18);
        uncomp = u32(hdr + 22);
        namelen = u16(hdr + 26);
        extra = u16(hdr + 28);
        /* Flag bit 3 means the sizes come after the data; this reader cannot handle that. */
        if ((flags & 8) || namelen == 0 || namelen >= sizeof name) break;
        if (comp > 64u * 1024u * 1024u || uncomp > 64u * 1024u * 1024u) break;
        pos += 30;
        if (pos + namelen + extra + (unsigned long long)comp > size) break;
        if (!read_at(h, base + pos, name, namelen)) break;
        name[namelen] = 0;
        pos += namelen + extra;
        /* Folder entries carry no data and are skipped. */
        if (name[namelen - 1] == '/' || name[namelen - 1] == '\\') {
            pos += comp;
            continue;
        }
        if (method != 0 || comp != uncomp) break;
        if (!safe_path(dest, name, path, MAX_PATH)) break;
        MultiByteToWideChar(CP_UTF8, 0, name, -1, shown, 120);
        set_status(hwnd, shown);
        UpdateWindow(hwnd);
        if (comp == 0) {
            HANDLE empty = CreateFileW(path, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
            if (empty == INVALID_HANDLE_VALUE || crc != 0) {
                if (empty != INVALID_HANDLE_VALUE) CloseHandle(empty);
                break;
            }
            CloseHandle(empty);
        } else if (!write_stored(h, base + pos, comp, crc, path)) {
            break;
        }
        pos += comp;
    }
    CloseHandle(h);
    set_status(hwnd, tr(L"The payload is damaged.", L"Данные установщика повреждены.", L"安装包数据已损坏。"));
    return 0;
}

/* run_wait starts exe with args, hidden, and waits up to 2 minutes.
   Returns 1 only when it exits with code 0. */
static int run_wait(const wchar_t *exe, const wchar_t *args, const wchar_t *dir) {
    wchar_t cmd[MAX_PATH * 2];
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    DWORD code = 1;
    _snwprintf(cmd, sizeof cmd / sizeof cmd[0], L"\"%s\" %s", exe, args);
    cmd[(sizeof cmd / sizeof cmd[0]) - 1] = 0;
    memset(&si, 0, sizeof si);
    si.cb = sizeof si;
    si.dwFlags = STARTF_USESHOWWINDOW;
    si.wShowWindow = SW_HIDE;
    memset(&pi, 0, sizeof pi);
    if (!CreateProcessW(exe, cmd, NULL, NULL, FALSE, CREATE_NO_WINDOW, NULL, dir, &si, &pi)) return 0;
    WaitForSingleObject(pi.hProcess, 120000);
    GetExitCodeProcess(pi.hProcess, &code);
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    return code == 0;
}

/* shortcut creates "Loft" in the Start menu for all users, pointing at loft.exe
   in dir. It uses the shell's COM link object; the CLSID and IIDs are spelled
   out here. */
static int shortcut(const wchar_t *dir) {
    static const CLSID clsid = {0x00021401, 0x0000, 0x0000, {0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}};
    static const IID iid_link = {0x000214F9, 0x0000, 0x0000, {0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}};
    static const IID iid_file = {0x0000010b, 0x0000, 0x0000, {0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}};
    IShellLinkW *sl = NULL;
    IPersistFile *pf = NULL;
    wchar_t exe[MAX_PATH], menu[MAX_PATH], lnk[MAX_PATH];
    int ok = 0;
    _snwprintf(exe, MAX_PATH, L"%s\\loft.exe", dir);
    exe[MAX_PATH - 1] = 0;
    if (SHGetFolderPathW(NULL, CSIDL_COMMON_PROGRAMS, NULL, 0, menu) != S_OK) return 0;
    _snwprintf(lnk, MAX_PATH, L"%s\\Loft.lnk", menu);
    lnk[MAX_PATH - 1] = 0;
    CoInitialize(NULL);
    if (CoCreateInstance(&clsid, NULL, CLSCTX_INPROC_SERVER, &iid_link, (void **)&sl) != S_OK) return 0;
    sl->lpVtbl->SetPath(sl, exe);
    sl->lpVtbl->SetWorkingDirectory(sl, dir);
    sl->lpVtbl->SetDescription(sl, L"Loft");
    if (sl->lpVtbl->QueryInterface(sl, &iid_file, (void **)&pf) == S_OK) {
        ok = pf->lpVtbl->Save(pf, lnk, TRUE) == S_OK;
        pf->lpVtbl->Release(pf);
    }
    sl->lpVtbl->Release(sl);
    return ok;
}

/* remove_shortcut deletes the all-users Start menu shortcut. */
static void remove_shortcut(void) {
    wchar_t menu[MAX_PATH], lnk[MAX_PATH];
    if (SHGetFolderPathW(NULL, CSIDL_COMMON_PROGRAMS, NULL, 0, menu) != S_OK) return;
    _snwprintf(lnk, MAX_PATH, L"%s\\Loft.lnk", menu);
    lnk[MAX_PATH - 1] = 0;
    DeleteFileW(lnk);
}

/* write_uninstall_key adds Loft to "Programs and Features": name, version,
   publisher, folder, icon and the command that runs loft-setup.exe --uninstall. */
static void write_uninstall_key(const wchar_t *dir) {
    HKEY k = NULL;
    wchar_t setup[MAX_PATH], exe[MAX_PATH], cmd[MAX_PATH + 32], ver[32];
    DWORD one = 1;
    if (RegCreateKeyExW(HKEY_LOCAL_MACHINE, L"Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Loft", 0, NULL, 0, KEY_SET_VALUE, NULL, &k, NULL) != ERROR_SUCCESS)
        return;
    _snwprintf(setup, MAX_PATH, L"%s\\loft-setup.exe", dir);
    setup[MAX_PATH - 1] = 0;
    _snwprintf(exe, MAX_PATH, L"%s\\loft.exe", dir);
    exe[MAX_PATH - 1] = 0;
    _snwprintf(cmd, MAX_PATH + 32, L"\"%s\" --uninstall", setup);
    cmd[MAX_PATH + 31] = 0;
    MultiByteToWideChar(CP_UTF8, 0, LOFT_VERSION, -1, ver, 32);
    RegSetValueExW(k, L"DisplayName", 0, REG_SZ, (const BYTE *)L"Loft", 5 * sizeof(wchar_t));
    RegSetValueExW(k, L"DisplayVersion", 0, REG_SZ, (const BYTE *)ver, (DWORD)((wcslen(ver) + 1) * sizeof(wchar_t)));
    RegSetValueExW(k, L"Publisher", 0, REG_SZ, (const BYTE *)L"a3app", 6 * sizeof(wchar_t));
    RegSetValueExW(k, L"InstallLocation", 0, REG_SZ, (const BYTE *)dir, (DWORD)((wcslen(dir) + 1) * sizeof(wchar_t)));
    RegSetValueExW(k, L"UninstallString", 0, REG_SZ, (const BYTE *)cmd, (DWORD)((wcslen(cmd) + 1) * sizeof(wchar_t)));
    RegSetValueExW(k, L"DisplayIcon", 0, REG_SZ, (const BYTE *)exe, (DWORD)((wcslen(exe) + 1) * sizeof(wchar_t)));
    RegSetValueExW(k, L"NoModify", 0, REG_DWORD, (const BYTE *)&one, sizeof one);
    RegSetValueExW(k, L"NoRepair", 0, REG_DWORD, (const BYTE *)&one, sizeof one);
    RegCloseKey(k);
}

/* read_install_dir reads the install folder back from the uninstall key.
   Returns 0 when Loft is not registered. */
static int read_install_dir(wchar_t *dir, int n) {
    HKEY k = NULL;
    DWORD nb = (DWORD)n * sizeof(wchar_t), typ = 0;
    if (RegOpenKeyExW(HKEY_LOCAL_MACHINE, L"Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Loft", 0, KEY_QUERY_VALUE, &k) != ERROR_SUCCESS)
        return 0;
    if (RegQueryValueExW(k, L"InstallLocation", NULL, &typ, (BYTE *)dir, &nb) != ERROR_SUCCESS || typ != REG_SZ) {
        RegCloseKey(k);
        return 0;
    }
    RegCloseKey(k);
    dir[n - 1] = 0;
    return dir[0] != 0;
}

/* launch_user starts exe as the signed-in user, not as administrator.
   The installer runs elevated, but Loft must run as the normal user: its
   profiles and keys live in that user's AppData. The token is borrowed from the
   desktop shell (Explorer), which runs unelevated. Returns 0 if that fails. */
static int launch_user(const wchar_t *exe, const wchar_t *dir) {
    HWND shell = GetShellWindow();
    DWORD pid = 0;
    HANDLE proc = NULL, tok = NULL, dup = NULL;
    LPVOID env = NULL;
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    wchar_t cmd[MAX_PATH + 8];
    int ok = 0;
    if (!shell) return 0;
    GetWindowThreadProcessId(shell, &pid);
    proc = OpenProcess(PROCESS_QUERY_INFORMATION, FALSE, pid);
    if (!proc || !OpenProcessToken(proc, TOKEN_DUPLICATE | TOKEN_QUERY, &tok)) goto done;
    if (!DuplicateTokenEx(tok, TOKEN_QUERY | TOKEN_DUPLICATE | TOKEN_ASSIGN_PRIMARY | TOKEN_ADJUST_DEFAULT | TOKEN_ADJUST_SESSIONID, NULL, SecurityImpersonation, TokenPrimary, &dup))
        goto done;
    if (!CreateEnvironmentBlock(&env, dup, FALSE)) env = NULL;
    _snwprintf(cmd, MAX_PATH + 8, L"\"%s\"", exe);
    cmd[MAX_PATH + 7] = 0;
    memset(&si, 0, sizeof si);
    si.cb = sizeof si;
    si.dwFlags = STARTF_USESHOWWINDOW;
    si.wShowWindow = SW_SHOWNORMAL;
    memset(&pi, 0, sizeof pi);
    if (CreateProcessAsUserW(dup, exe, cmd, NULL, NULL, FALSE, CREATE_UNICODE_ENVIRONMENT, env, dir, &si, &pi)) {
        CloseHandle(pi.hThread);
        CloseHandle(pi.hProcess);
        ok = 1;
    }
done:
    if (env) DestroyEnvironmentBlock(env);
    if (dup) CloseHandle(dup);
    if (tok) CloseHandle(tok);
    if (proc) CloseHandle(proc);
    return ok;
}

/* has_loft_exe tells whether dir contains loft.exe. Used as a safety check
   before deleting a folder, so a wrong path never wipes an unrelated folder. */
static int has_loft_exe(const wchar_t *dir) {
    wchar_t p[MAX_PATH];
    _snwprintf(p, MAX_PATH, L"%s\\loft.exe", dir);
    p[MAX_PATH - 1] = 0;
    return GetFileAttributesW(p) != INVALID_FILE_ATTRIBUTES;
}

/* delete_dir deletes a folder and everything inside it. It does not follow
   junctions or symbolic links (reparse points) into other places, and refuses
   very short paths such as a drive root. */
static int delete_dir(const wchar_t *dir) {
    wchar_t spec[MAX_PATH], path[MAX_PATH];
    WIN32_FIND_DATAW fd;
    HANDLE f;
    if (!dir || !dir[0] || wcslen(dir) < 4) return 0;
    _snwprintf(spec, MAX_PATH, L"%s\\*", dir);
    spec[MAX_PATH - 1] = 0;
    f = FindFirstFileW(spec, &fd);
    if (f == INVALID_HANDLE_VALUE) return RemoveDirectoryW(dir);
    do {
        if (wcscmp(fd.cFileName, L".") == 0 || wcscmp(fd.cFileName, L"..") == 0) continue;
        _snwprintf(path, MAX_PATH, L"%s\\%s", dir, fd.cFileName);
        path[MAX_PATH - 1] = 0;
        if (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) {
            if (!(fd.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)) delete_dir(path);
            RemoveDirectoryW(path);
        } else {
            SetFileAttributesW(path, FILE_ATTRIBUTE_NORMAL);
            DeleteFileW(path);
        }
    } while (FindNextFileW(f, &fd));
    FindClose(f);
    return RemoveDirectoryW(dir);
}

/* purge_tree deletes dir only if it looks like a Loft folder. */
static int purge_tree(const wchar_t *dir) {
    if (!has_loft_exe(dir)) return 0;
    return delete_dir(dir);
}

/* do_purge is the last uninstall step, run from a copy of this exe in %TEMP%
   (--purge). It waits up to 20 s for the uninstaller process pid to exit,
   because Windows cannot delete a running exe. Then it removes the uninstall
   key, the shortcut and the install folder. Exit code 0 means the folder is gone. */
static int do_purge(const wchar_t *dir, unsigned pid) {
    if (pid) {
        HANDLE ph = OpenProcess(SYNCHRONIZE, FALSE, pid);
        if (ph) {
            WaitForSingleObject(ph, 20000);
            CloseHandle(ph);
        }
    }
    Sleep(300);
    RegDeleteKeyW(HKEY_LOCAL_MACHINE, L"Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Loft");
    remove_shortcut();
    return purge_tree(dir) ? 0 : 1;
}

/* copy_self copies this exe into the install folder as loft-setup.exe, which
   the uninstall key points to. */
static int copy_self(const wchar_t *dir) {
    wchar_t src[MAX_PATH], dst[MAX_PATH];
    self_path(src, MAX_PATH);
    _snwprintf(dst, MAX_PATH, L"%s\\loft-setup.exe", dir);
    dst[MAX_PATH - 1] = 0;
    if (wcsicmp(src, dst) == 0) return 1;
    return CopyFileW(src, dst, FALSE);
}

/* schedule_purge copies this exe to %TEMP% and starts it with --purge.
   An exe cannot delete the folder it runs from, so the copy finishes the job
   after this process exits. */
static void schedule_purge(const wchar_t *dir) {
    wchar_t src[MAX_PATH], tmp[MAX_PATH], cmd[MAX_PATH * 2];
    wchar_t temp[MAX_PATH];
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    self_path(src, MAX_PATH);
    if (!GetTempPathW(MAX_PATH, temp)) return;
    _snwprintf(tmp, MAX_PATH, L"%sloft-setup-purge.exe", temp);
    tmp[MAX_PATH - 1] = 0;
    if (!CopyFileW(src, tmp, FALSE)) return;
    _snwprintf(cmd, sizeof cmd / sizeof cmd[0], L"\"%s\" --purge \"%s\" %lu", tmp, dir, GetCurrentProcessId());
    cmd[(sizeof cmd / sizeof cmd[0]) - 1] = 0;
    memset(&si, 0, sizeof si);
    si.cb = sizeof si;
    memset(&pi, 0, sizeof pi);
    if (CreateProcessW(tmp, cmd, NULL, NULL, FALSE, CREATE_NO_WINDOW, NULL, NULL, &si, &pi)) {
        CloseHandle(pi.hThread);
        CloseHandle(pi.hProcess);
    }
}

/* install_work does the install itself. It must run as administrator.
   Returns 1 on full success. */
static int install_work(HWND hwnd) {
    wchar_t svc[MAX_PATH], exe[MAX_PATH], mark[MAX_PATH];
    GetWindowTextW(g_edit, g_dir, MAX_PATH);
    if (!g_dir[0] || wcslen(g_dir) < 4) return 0;
    /* 1. Unpack the payload and keep this exe as the uninstaller. */
    set_status(hwnd, tr(L"Copying files.", L"Копирование файлов.", L"正在复制文件。"));
    if (!extract(hwnd, g_dir) || !has_loft_exe(g_dir)) return 0;
    if (!copy_self(g_dir)) {
        set_status(hwnd, tr(L"Could not write loft-setup.exe.", L"Не удалось записать loft-setup.exe.", L"无法写入 loft-setup.exe。"));
        return 0;
    }
    /* 2. If file links were turned off, leave an "assoc.off" marker; loft.exe reads
          it on its first start (src/win/assoc.c) and turns the associations off. */
    if (!g_assoc) {
        _snwprintf(mark, MAX_PATH, L"%s\\assoc.off", g_dir);
        mark[MAX_PATH - 1] = 0;
        {
            HANDLE mk = CreateFileW(mark, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
            if (mk != INVALID_HANDLE_VALUE) CloseHandle(mk);
        }
    }
    /* 3. Register and start the loft service. On failure the files stay, with a
          shortcut and an uninstall entry, so the user can still remove them. */
    _snwprintf(svc, MAX_PATH, L"%s\\loft-svc.exe", g_dir);
    svc[MAX_PATH - 1] = 0;
    set_status(hwnd, tr(L"Registering the loft service.", L"Регистрация службы loft.", L"正在注册 loft 服务。"));
    UpdateWindow(hwnd);
    if (!run_wait(svc, L"--install", g_dir)) {
        set_status(hwnd, tr(L"The service was not registered. Files are in place.", L"Служба не зарегистрирована. Файлы уже на месте.", L"服务没有注册。文件已经就位。"));
        write_uninstall_key(g_dir);
        shortcut(g_dir);
        return 0;
    }
    /* 4. Shortcut, uninstall entry, and start Loft if asked. */
    shortcut(g_dir);
    write_uninstall_key(g_dir);
    if (g_open) {
        _snwprintf(exe, MAX_PATH, L"%s\\loft.exe", g_dir);
        exe[MAX_PATH - 1] = 0;
        if (!launch_user(exe, g_dir))
            set_status(hwnd, tr(L"Installed. Open Loft from the Start menu.", L"Установлено. Откройте Loft из меню Пуск.", L"已安装。请从开始菜单打开 Loft。"));
    }
    set_status(hwnd, tr(L"Installed.", L"Установлено.", L"已安装。"));
    return 1;
}

/* uninstall_work removes the service and the shortcut, then hands deletion
   of the folder to the --purge copy. Must run as administrator. */
static int uninstall_work(HWND hwnd) {
    wchar_t svc[MAX_PATH];
    if (!g_dir[0] && !read_install_dir(g_dir, MAX_PATH)) {
        self_path(g_dir, MAX_PATH);
        parent_dir(g_dir);
    }
    if (!has_loft_exe(g_dir)) {
        set_status(hwnd, tr(L"Loft is not in this folder.", L"В этой папке нет Loft.", L"此文件夹里没有 Loft。"));
        return 0;
    }
    _snwprintf(svc, MAX_PATH, L"%s\\loft-svc.exe", g_dir);
    svc[MAX_PATH - 1] = 0;
    set_status(hwnd, tr(L"Removing the service.", L"Удаление службы.", L"正在删除服务。"));
    UpdateWindow(hwnd);
    if (GetFileAttributesW(svc) != INVALID_FILE_ATTRIBUTES) run_wait(svc, L"--uninstall", g_dir);
    remove_shortcut();
    schedule_purge(g_dir);
    set_status(hwnd, tr(L"Removed.", L"Удалено.", L"已删除。"));
    return 1;
}

/* elevate re-runs this exe with parameters through UAC and waits for it.
   Returns 0 when UAC was refused or the start failed; *code gets the child's exit code. */
static int elevate(const wchar_t *parameters, DWORD *code) {
    wchar_t path[MAX_PATH];
    SHELLEXECUTEINFOW sei;
    self_path(path, MAX_PATH);
    memset(&sei, 0, sizeof sei);
    sei.cbSize = sizeof sei;
    sei.fMask = SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC;
    sei.lpVerb = L"runas";
    sei.lpFile = path;
    sei.lpParameters = parameters;
    sei.nShow = SW_SHOWNORMAL;
    if (!ShellExecuteExW(&sei)) return 0;
    if (sei.hProcess) {
        WaitForSingleObject(sei.hProcess, INFINITE);
        if (code) GetExitCodeProcess(sei.hProcess, code);
        CloseHandle(sei.hProcess);
    }
    return 1;
}

/* quote_dir takes the folder from the edit box and wraps it in quotes for a
   command line. */
static void quote_dir(wchar_t *d, int n) {
    wchar_t tmp[MAX_PATH];
    GetWindowTextW(g_edit, g_dir, MAX_PATH);
    wcsncpy(tmp, g_dir, MAX_PATH - 1);
    tmp[MAX_PATH - 1] = 0;
    _snwprintf(d, (size_t)n, L"\"%s\"", tmp);
    if (n > 0) d[n - 1] = 0;
}

/* on_go handles the Install / Uninstall button. As administrator it does the
   work here. Otherwise it hides the window, starts an elevated copy with the
   same choices (--do or --do-uninstall plus --dir and options), and exits with
   that copy's exit code. */
static void on_go(HWND hwnd) {
    wchar_t params[MAX_PATH + 80], qdir[MAX_PATH + 2];
    DWORD code = 1;
    if (g_busy) return;
    g_busy = 1;
    if (is_admin()) {
        int ok = g_uninstall ? uninstall_work(hwnd) : install_work(hwnd);
        g_fail = !ok;
        g_busy = 0;
        if (ok) PostQuitMessage(0);
        return;
    }
    quote_dir(qdir, MAX_PATH + 2);
    if (g_uninstall) _snwprintf(params, sizeof params / sizeof params[0], L"--do-uninstall --dir %s", qdir);
    else
        _snwprintf(params, sizeof params / sizeof params[0], L"--do --dir %s%s%s", qdir, g_open ? L"" : L" --no-open", g_assoc ? L"" : L" --no-assoc");
    params[(sizeof params / sizeof params[0]) - 1] = 0;
    ShowWindow(hwnd, SW_HIDE);
    if (!elevate(params, &code)) {
        ShowWindow(hwnd, SW_SHOWNORMAL);
        set_status(hwnd, tr(L"Administrator rights were not granted.", L"Права администратора не даны.", L"没有获得管理员权限。"));
        g_busy = 0;
        return;
    }
    ExitProcess(code);
}

/* wnd_proc runs the installer window. The window has no standard frame or
   buttons: everything except the folder edit box is drawn in WM_PAINT, and
   clicks are matched against the same rectangles in WM_LBUTTONUP
   (close "x", the two checkboxes, the main button). */
static LRESULT CALLBACK wnd_proc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    switch (msg) {
    case WM_CREATE:
        g_edit = CreateWindowExW(0, L"EDIT", g_dir, WS_CHILD | WS_VISIBLE | ES_AUTOHSCROLL, sc(16), sc(78), sc(448), sc(24), hwnd, (HMENU)(INT_PTR)ID_DIR, NULL, NULL);
        SendMessageW(g_edit, WM_SETFONT, (WPARAM)g_font, TRUE);
        if (g_auto) PostMessageW(hwnd, WM_APP, 0, 0);
        return 0;
    case WM_APP:
        on_go(hwnd);
        return 0;
    case WM_ERASEBKGND:
        return 1;
    case WM_CTLCOLOREDIT: {
        HDC dc = (HDC)wp;
        SetTextColor(dc, RGB(0xF3, 0xED, 0xE4));
        SetBkColor(dc, RGB(0x21, 0x1C, 0x19));
        return (LRESULT)g_field;
    }
    case WM_COMMAND:
        if (LOWORD(wp) == ID_DIR && HIWORD(wp) == EN_CHANGE) GetWindowTextW(g_edit, g_dir, MAX_PATH);
        return 0;
    case WM_LBUTTONUP: {
        int x = LOWORD(lp), y = HIWORD(lp);
        RECT go, open, assoc, closeb;
        SetRect(&go, sc(16), sc(210), sc(200), sc(244));
        SetRect(&open, sc(16), sc(150), sc(460), sc(172));
        SetRect(&assoc, sc(16), sc(176), sc(460), sc(198));
        SetRect(&closeb, sc(434), 0, sc(480), sc(32));
        {
            POINT pt;
            pt.x = x;
            pt.y = y;
            if (PtInRect(&closeb, pt)) {
                PostQuitMessage(g_fail ? 1 : 0);
                return 0;
            }
            if (!g_uninstall && PtInRect(&open, pt)) {
                g_open = !g_open;
                InvalidateRect(hwnd, NULL, FALSE);
                return 0;
            }
            if (!g_uninstall && PtInRect(&assoc, pt)) {
                g_assoc = !g_assoc;
                InvalidateRect(hwnd, NULL, FALSE);
                return 0;
            }
            if (PtInRect(&go, pt)) on_go(hwnd);
        }
        return 0;
    }
    case WM_PAINT: {
        PAINTSTRUCT ps;
        HDC dc = BeginPaint(hwnd, &ps);
        RECT rc, go;
        wchar_t box[8];
        GetClientRect(hwnd, &rc);
        FillRect(dc, &rc, (HBRUSH)GetStockObject(BLACK_BRUSH));
        SetBkMode(dc, TRANSPARENT);
        SelectObject(dc, g_font);
        SetTextColor(dc, RGB(0xE8, 0xB8, 0x4A));
        TextOutW(dc, sc(16), sc(8), L"Loft", 4);
        SetTextColor(dc, RGB(0xF3, 0xED, 0xE4));
        TextOutW(dc, sc(434), sc(6), L"\x00D7", 1);
        SetTextColor(dc, RGB(0xB7, 0xA9, 0x9A));
        {
            const wchar_t *body = g_uninstall ? tr(L"Remove Loft, the service and the Start menu shortcut. The driver stays. Profiles stay in AppData.", L"Удалить Loft, службу и ярлык в меню Пуск. Драйвер остаётся. Профили остаются в AppData.", L"删除 Loft、服务和开始菜单快捷方式。驱动保留。配置留在 AppData。")
                                              : tr(L"Copy Loft to the folder below and register the loft service. Windows 7 and later.", L"Скопировать Loft в папку ниже и зарегистрировать службу loft. Windows 7 и новее.", L"把 Loft 复制到下面的文件夹，并注册 loft 服务。Windows 7 及更高版本。");
            RECT br;
            SetRect(&br, sc(16), sc(36), sc(464), sc(74));
            DrawTextW(dc, body, -1, &br, DT_LEFT | DT_WORDBREAK);
        }
        if (!g_uninstall) {
            SetTextColor(dc, RGB(0xF3, 0xED, 0xE4));
            lstrcpyW(box, g_open ? L"[x]" : L"[ ]");
            TextOutW(dc, sc(16), sc(150), box, 3);
            TextOutW(dc, sc(44), sc(150), tr(L"Open Loft when finished", L"Открыть Loft после установки", L"完成后打开 Loft"), (int)wcslen(tr(L"Open Loft when finished", L"Открыть Loft после установки", L"完成后打开 Loft")));
            lstrcpyW(box, g_assoc ? L"[x]" : L"[ ]");
            TextOutW(dc, sc(16), sc(176), box, 3);
            TextOutW(dc, sc(44), sc(176), tr(L"Open .loft and loft:// in Loft", L"Открывать .loft и loft:// в Loft", L"用 Loft 打开 .loft 和 loft://"), (int)wcslen(tr(L"Open .loft and loft:// in Loft", L"Открывать .loft и loft:// в Loft", L"用 Loft 打开 .loft 和 loft://")));
        }
        SetRect(&go, sc(16), sc(210), sc(200), sc(244));
        FillRect(dc, &go, g_field);
        SetTextColor(dc, RGB(0xE8, 0xB8, 0x4A));
        DrawTextW(dc, g_uninstall ? tr(L"Uninstall", L"Удалить", L"卸载") : tr(L"Install", L"Установить", L"安装"), -1, &go, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
        SetTextColor(dc, g_fail ? RGB(0xE2, 0x4B, 0x4B) : RGB(0xB7, 0xA9, 0x9A));
        {
            RECT sr;
            SetRect(&sr, sc(16), sc(252), sc(464), sc(292));
            DrawTextW(dc, g_status, -1, &sr, DT_LEFT | DT_WORDBREAK);
        }
        EndPaint(hwnd, &ps);
        return 0;
    }
    case WM_DESTROY:
        PostQuitMessage(g_fail ? 1 : 0);
        return 0;
    default:
        return DefWindowProcW(hwnd, msg, wp, lp);
    }
}

/* default_dir picks the starting folder: for install, "Program Files\Loft";
   for uninstall, the registered folder, or the folder of this exe. */
static void default_dir(void) {
    wchar_t pf[MAX_PATH];
    g_dir[0] = 0;
    if (g_uninstall) {
        if (read_install_dir(g_dir, MAX_PATH)) return;
        self_path(g_dir, MAX_PATH);
        parent_dir(g_dir);
        return;
    }
    if (SHGetFolderPathW(NULL, CSIDL_PROGRAM_FILES, NULL, SHGFP_TYPE_CURRENT, pf) != S_OK) lstrcpyW(pf, L"C:\\Program Files");
    _snwprintf(g_dir, MAX_PATH, L"%s\\Loft", pf);
    g_dir[MAX_PATH - 1] = 0;
}

/* wWinMain reads the command line and either runs --purge without a window or
   shows the installer window.
   Flags: --uninstall, --do / --do-uninstall (start at once), --dir PATH,
   --no-open, --no-assoc, --purge DIR PID. */
int WINAPI wWinMain(HINSTANCE inst, HINSTANCE prev, LPWSTR cmd, int show) {
    WNDCLASSW wc;
    HWND hwnd;
    MSG msg;
    int argc = 0, i, purge = 0;
    unsigned purge_pid = 0;
    wchar_t purge_dir[MAX_PATH];
    LPWSTR *argv;
    HDC dc;
    (void)prev;
    (void)cmd;
    (void)show;
    g_lang = 0;
    switch (PRIMARYLANGID(GetUserDefaultUILanguage())) {
    case LANG_RUSSIAN:
        g_lang = 1;
        break;
    case LANG_CHINESE:
        g_lang = 2;
        break;
    default:
        break;
    }
    purge_dir[0] = 0;
    argv = CommandLineToArgvW(GetCommandLineW(), &argc);
    for (i = 1; argv && i < argc; i++) {
        if (wcscmp(argv[i], L"--uninstall") == 0) g_uninstall = 1;
        else if (wcscmp(argv[i], L"--do") == 0) g_auto = 1;
        else if (wcscmp(argv[i], L"--do-uninstall") == 0) {
            g_uninstall = 1;
            g_auto = 1;
        } else if (wcscmp(argv[i], L"--no-open") == 0) g_open = 0;
        else if (wcscmp(argv[i], L"--no-assoc") == 0) g_assoc = 0;
        else if (wcscmp(argv[i], L"--dir") == 0 && i + 1 < argc) {
            wcsncpy(g_dir, argv[++i], MAX_PATH - 1);
            g_dir[MAX_PATH - 1] = 0;
        } else if (wcscmp(argv[i], L"--purge") == 0 && i + 1 < argc) {
            purge = 1;
            wcsncpy(purge_dir, argv[++i], MAX_PATH - 1);
            purge_dir[MAX_PATH - 1] = 0;
            if (i + 1 < argc) purge_pid = (unsigned)_wtoi(argv[++i]);
        }
    }
    if (argv) LocalFree(argv);
    if (purge) return do_purge(purge_dir, purge_pid);
    if (!g_dir[0]) default_dir();
    /* Window setup: DPI, font, dark field brush, centred borderless window. */
    SetProcessDPIAware();
    dc = GetDC(NULL);
    if (dc) {
        g_dpi = GetDeviceCaps(dc, LOGPIXELSX);
        if (g_dpi < 96) g_dpi = 96;
        ReleaseDC(NULL, dc);
    }
    g_font = CreateFontW(-sc(15), 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE, DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY, DEFAULT_PITCH, L"Segoe UI");
    g_field = CreateSolidBrush(RGB(0x21, 0x1C, 0x19));
    memset(&wc, 0, sizeof wc);
    wc.lpfnWndProc = wnd_proc;
    wc.hInstance = inst;
    wc.lpszClassName = L"LoftSetup";
    wc.hIcon = LoadIconW(inst, MAKEINTRESOURCEW(1));
    wc.hCursor = LoadCursor(NULL, IDC_ARROW);
    wc.hbrBackground = (HBRUSH)GetStockObject(BLACK_BRUSH);
    RegisterClassW(&wc);
    hwnd = CreateWindowExW(WS_EX_APPWINDOW, L"LoftSetup", L"Loft", WS_POPUP | WS_VISIBLE, (GetSystemMetrics(SM_CXSCREEN) - sc(480)) / 2, (GetSystemMetrics(SM_CYSCREEN) - sc(300)) / 2, sc(480), sc(300), NULL, NULL, inst, NULL);
    if (!hwnd) return 1;
    while (GetMessageW(&msg, NULL, 0, 0) > 0) {
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }
    return (int)msg.wParam;
}
