/* Replace the running GitHub build from a portable zip. Store builds do not call this. */
#include "common.h"
#include "puff.h"

#include <bcrypt.h>
#include <userenv.h>
#include <wchar.h>

#define UPD_MAX (32u * 1024u * 1024u)

static unsigned long crc_table[256];
static int crc_ready;

static void crc_init(void) {
    unsigned long n, c;
    int k;
    if (crc_ready) return;
    for (n = 0; n < 256; n++) {
        c = n;
        for (k = 0; k < 8; k++) c = (c & 1) ? (0xedb88320ul ^ (c >> 1)) : (c >> 1);
        crc_table[n] = c;
    }
    crc_ready = 1;
}

static unsigned long crc32_bytes(const unsigned char *p, unsigned long n) {
    unsigned long c = 0xfffffffful;
    crc_init();
    while (n--) c = crc_table[(c ^ *p++) & 0xff] ^ (c >> 8);
    return c ^ 0xfffffffful;
}

static unsigned short ru16(const unsigned char *p) {
    return (unsigned short)(p[0] | (p[1] << 8));
}

static unsigned long ru32(const unsigned char *p) {
    return (unsigned long)p[0] | ((unsigned long)p[1] << 8) | ((unsigned long)p[2] << 16) | ((unsigned long)p[3] << 24);
}

static int hex_eq(const char *a, const char *b) {
    int i;
    if (!a || !b) return 0;
    for (i = 0; i < 64; i++) {
        char x = a[i], y = b[i];
        if (x >= 'A' && x <= 'F') x = (char)(x - 'A' + 'a');
        if (y >= 'A' && y <= 'F') y = (char)(y - 'A' + 'a');
        if (!x || !y || x != y) return 0;
    }
    return a[64] == 0 || a[64] == ' ' || a[64] == '\t' || a[64] == '\r' || a[64] == '\n';
}

int upd_sha256_hex(const wchar_t *path, char *dst, int n) {
    BCRYPT_ALG_HANDLE alg = NULL;
    BCRYPT_HASH_HANDLE hash = NULL;
    UCHAR stack[512], dig[32], buf[8192];
    UCHAR *obj = stack;
    DWORD objlen = 0, got = 0, rd = 0;
    HANDLE file;
    int i, ok = 0;
    if (!dst || n < 65) return 0;
    dst[0] = 0;
    if (BCryptOpenAlgorithmProvider(&alg, BCRYPT_SHA256_ALGORITHM, NULL, 0) < 0) return 0;
    if (BCryptGetProperty(alg, BCRYPT_OBJECT_LENGTH, (PUCHAR)&objlen, sizeof objlen, &got, 0) < 0) goto out;
    if (objlen > sizeof stack) {
        obj = (UCHAR *)malloc(objlen);
        if (!obj) goto out;
    }
    if (BCryptCreateHash(alg, &hash, obj, objlen, NULL, 0, 0) < 0) goto out;
    file = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (file == INVALID_HANDLE_VALUE) goto out;
    for (;;) {
        if (!ReadFile(file, buf, sizeof buf, &rd, NULL)) {
            CloseHandle(file);
            goto out;
        }
        if (rd == 0) break;
        if (BCryptHashData(hash, buf, rd, 0) < 0) {
            CloseHandle(file);
            goto out;
        }
    }
    CloseHandle(file);
    if (BCryptFinishHash(hash, dig, 32, 0) < 0) goto out;
    for (i = 0; i < 32; i++) sprintf(dst + i * 2, "%02x", dig[i]);
    dst[64] = 0;
    ok = 1;
out:
    if (hash) BCryptDestroyHash(hash);
    if (alg) BCryptCloseAlgorithmProvider(alg, 0);
    if (obj != stack) free(obj);
    return ok;
}

int upd_sums_match(const char *text, const char *name, const char *hex) {
    const char *p;
    if (!text || !name || !name[0] || !hex || !hex[0]) return 0;
    p = text;
    while (*p) {
        const char *line = p, *sp, *end, *file;
        char got[80];
        int i;
        while (*p && *p != '\n') p++;
        end = p;
        if (*p == '\n') p++;
        if (end > line && end[-1] == '\r') end--;
        while (line < end && (*line == ' ' || *line == '\t')) line++;
        if (line >= end || *line == '#') continue;
        sp = line;
        while (sp < end && *sp != ' ' && *sp != '\t') sp++;
        if ((int)(sp - line) < 64) continue;
        for (i = 0; i < 64 && line + i < sp; i++) got[i] = line[i];
        got[64] = 0;
        file = sp;
        while (file < end && (*file == ' ' || *file == '\t' || *file == '*')) file++;
        if ((size_t)(end - file) >= strlen(name) && memcmp(end - strlen(name), name, strlen(name)) == 0 && hex_eq(got, hex))
            return 1;
    }
    return 0;
}

static int name_ok(const char *name) {
    const char *p;
    int dot = 0;
    if (!name || !name[0] || name[0] == '/' || name[0] == '\\') return 0;
    if ((name[0] >= 'A' && name[0] <= 'Z' && name[1] == ':') || name[0] == '.') return 0;
    for (p = name; *p; p++) {
        unsigned char c = (unsigned char)*p;
        if (c == '\\' || c < 0x20 || c == ':' || c == '*' || c == '?' || c == '"' || c == '<' || c == '>' || c == '|') return 0;
        if (c == '/') {
            if (dot == 1 || dot == 2) return 0;
            dot = 0;
            continue;
        }
        if (c == '.') {
            dot++;
            continue;
        }
        dot = 0;
        if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-')) return 0;
    }
    return dot != 2;
}

static int mkdir_one(const wchar_t *path) {
    if (CreateDirectoryW(path, NULL)) return 1;
    return GetLastError() == ERROR_ALREADY_EXISTS;
}

static int ensure_parent(const wchar_t *path) {
    wchar_t buf[MAX_PATH];
    wchar_t *s;
    size_t n = wcslen(path);
    if (n == 0 || n >= MAX_PATH) return 0;
    memcpy(buf, path, (n + 1) * sizeof(wchar_t));
    s = wcsrchr(buf, L'\\');
    if (!s) return 1;
    *s = 0;
    for (s = buf; *s; s++) {
        if (*s != L'\\') continue;
        if (s == buf) continue;
        if (s == buf + 2 && buf[1] == L':') continue;
        *s = 0;
        if (!mkdir_one(buf)) return 0;
        *s = L'\\';
    }
    return mkdir_one(buf);
}

static int write_bytes(const wchar_t *path, const unsigned char *data, unsigned long n) {
    HANDLE h;
    DWORD wr = 0, off = 0;
    if (!ensure_parent(path)) return 0;
    h = CreateFileW(path, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return 0;
    while (off < n) {
        DWORD chunk = n - off > 1u << 20 ? 1u << 20 : (DWORD)(n - off);
        if (!WriteFile(h, data + off, chunk, &wr, NULL) || wr == 0) {
            CloseHandle(h);
            DeleteFileW(path);
            return 0;
        }
        off += wr;
    }
    CloseHandle(h);
    return 1;
}

static int to_wide(const char *name, wchar_t *dst, int n) {
    wchar_t *w;
    int k;
    if (name[0] == '.' && name[1] == '/') name += 2;
    if (!name_ok(name)) return 0;
    k = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, name, -1, dst, n);
    if (k <= 0) return 0;
    for (w = dst; *w; w++)
        if (*w == L'/') *w = L'\\';
    return 1;
}

static int unzip_to(const wchar_t *zip, const wchar_t *dest) {
    HANDLE h;
    DWORD sz, rd = 0;
    unsigned char *buf = NULL;
    const unsigned char *eocd, *cd, *end;
    unsigned long n, off, count, i;
    int ok = 0;
    h = CreateFileW(zip, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return 0;
    sz = GetFileSize(h, NULL);
    if (sz < 22 || sz > UPD_MAX) {
        CloseHandle(h);
        return 0;
    }
    buf = (unsigned char *)malloc(sz);
    if (!buf || !ReadFile(h, buf, sz, &rd, NULL) || rd != sz) {
        CloseHandle(h);
        free(buf);
        return 0;
    }
    CloseHandle(h);
    n = sz;
    eocd = NULL;
    off = n - 22;
    for (;;) {
        if (buf[off] == 0x50 && buf[off + 1] == 0x4b && buf[off + 2] == 0x05 && buf[off + 3] == 0x06) {
            eocd = buf + off;
            break;
        }
        if (off == 0 || n - off > 65557) break;
        off--;
    }
    if (!eocd) goto done;
    count = ru16(eocd + 10);
    off = ru32(eocd + 16);
    if (count == 0 || count > 256 || off >= n) goto done;
    cd = buf + off;
    end = eocd;
    for (i = 0; i < count; i++) {
        unsigned short method, flag, nlen, elen, clen;
        unsigned long crc, comp, uncomp, local, dlen, slen;
        const unsigned char *data, *lh;
        char name[260];
        wchar_t rel[260], path[MAX_PATH];
        unsigned char *out = NULL;
        int dir;
        if (cd + 46 > end || ru32(cd) != 0x02014b50ul) goto done;
        flag = ru16(cd + 8);
        method = ru16(cd + 10);
        crc = ru32(cd + 16);
        comp = ru32(cd + 20);
        uncomp = ru32(cd + 24);
        nlen = ru16(cd + 28);
        elen = ru16(cd + 30);
        clen = ru16(cd + 32);
        local = ru32(cd + 42);
        if (flag & 1) goto done;
        if (nlen == 0 || nlen >= sizeof name || cd + 46 + nlen > end) goto done;
        memcpy(name, cd + 46, nlen);
        name[nlen] = 0;
        cd += 46 + nlen + elen + clen;
        dir = name[nlen - 1] == '/';
        if (name[0] == '.' && name[1] == '/') {
            memmove(name, name + 2, strlen(name + 2) + 1);
        }
        if (!name[0]) continue;
        if (dir && name[strlen(name) - 1] == '/') name[strlen(name) - 1] = 0;
        if (!name_ok(name)) goto done;
        if (!to_wide(name, rel, 260)) goto done;
        if (wcslen(dest) + wcslen(rel) + 2 >= MAX_PATH) goto done;
        path_join(path, MAX_PATH, dest, rel);
        if (dir) {
            if (!ensure_parent(path) || !mkdir_one(path)) goto done;
            continue;
        }
        if (local + 30 > n || ru32(buf + local) != 0x04034b50ul) goto done;
        lh = buf + local;
        if (ru16(lh + 6) & 1) goto done;
        data = lh + 30 + ru16(lh + 26) + ru16(lh + 28);
        if (data + comp > buf + n) goto done;
        if (comp > UPD_MAX || uncomp > UPD_MAX) goto done;
        if (method == 0) {
            if (comp != uncomp) goto done;
            if (uncomp && crc32_bytes(data, uncomp) != crc) goto done;
            if (!write_bytes(path, data, uncomp)) goto done;
            continue;
        }
        if (method != 8) goto done;
        out = (unsigned char *)malloc(uncomp ? uncomp : 1);
        if (!out) goto done;
        dlen = uncomp;
        slen = comp;
        if (puff(out, &dlen, data, &slen) != 0 || dlen != uncomp || crc32_bytes(out, uncomp) != crc) {
            free(out);
            goto done;
        }
        if (!write_bytes(path, out, uncomp)) {
            free(out);
            goto done;
        }
        free(out);
    }
    ok = 1;
done:
    free(buf);
    return ok;
}

static int under_stage(const wchar_t *dir) {
    if (!dir || wcsstr(dir, L"..")) return 0;
    if (wcsrchr(dir, L'\\') && wcscmp(wcsrchr(dir, L'\\') + 1, L"loft-update") == 0) return 1;
    return wcsstr(dir, L"\\loft-update\\") != NULL;
}

static int rm_tree(const wchar_t *dir) {
    wchar_t spec[MAX_PATH], path[MAX_PATH];
    WIN32_FIND_DATAW fd;
    HANDLE h;
    DWORD attr;
    if (!under_stage(dir)) return 0;
    attr = GetFileAttributesW(dir);
    if (attr == INVALID_FILE_ATTRIBUTES) return 1;
    if (!(attr & FILE_ATTRIBUTE_DIRECTORY)) return DeleteFileW(dir);
    _snwprintf(spec, MAX_PATH, L"%s\\*", dir);
    spec[MAX_PATH - 1] = 0;
    h = FindFirstFileW(spec, &fd);
    if (h != INVALID_HANDLE_VALUE) {
        do {
            if (wcscmp(fd.cFileName, L".") == 0 || wcscmp(fd.cFileName, L"..") == 0) continue;
            path_join(path, MAX_PATH, dir, fd.cFileName);
            if (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) {
                if (!rm_tree(path)) {
                    FindClose(h);
                    return 0;
                }
            } else if (!DeleteFileW(path)) {
                FindClose(h);
                return 0;
            }
        } while (FindNextFileW(h, &fd));
        FindClose(h);
    }
    return RemoveDirectoryW(dir) || GetFileAttributesW(dir) == INVALID_FILE_ATTRIBUTES;
}

static int copy_one(const wchar_t *src, const wchar_t *dst) {
    wchar_t bak[MAX_PATH];
    if (CopyFileW(src, dst, FALSE)) return 1;
    if (wcslen(dst) + 5 >= MAX_PATH) return 0;
    _snwprintf(bak, MAX_PATH, L"%s.bak", dst);
    bak[MAX_PATH - 1] = 0;
    if (!MoveFileExW(dst, bak, MOVEFILE_REPLACE_EXISTING) && GetFileAttributesW(dst) != INVALID_FILE_ATTRIBUTES) return 0;
    if (!CopyFileW(src, dst, FALSE)) return 0;
    DeleteFileW(bak);
    return 1;
}

static int copy_tree(const wchar_t *src, const wchar_t *dst) {
    wchar_t spec[MAX_PATH];
    WIN32_FIND_DATAW fd;
    HANDLE h;
    if (!mkdir_one(dst)) return 0;
    if (wcslen(src) + 3 >= MAX_PATH) return 0;
    _snwprintf(spec, MAX_PATH, L"%s\\*", src);
    spec[MAX_PATH - 1] = 0;
    h = FindFirstFileW(spec, &fd);
    if (h == INVALID_HANDLE_VALUE) return 0;
    do {
        wchar_t from[MAX_PATH], to[MAX_PATH];
        if (wcscmp(fd.cFileName, L".") == 0 || wcscmp(fd.cFileName, L"..") == 0) continue;
        if (wcsstr(fd.cFileName, L"..")) {
            FindClose(h);
            return 0;
        }
        path_join(from, MAX_PATH, src, fd.cFileName);
        path_join(to, MAX_PATH, dst, fd.cFileName);
        if (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) {
            if (!copy_tree(from, to)) {
                FindClose(h);
                return 0;
            }
        } else if (!copy_one(from, to)) {
            FindClose(h);
            return 0;
        }
    } while (FindNextFileW(h, &fd));
    FindClose(h);
    return 1;
}

static int elevated(void) {
    HANDLE tok = NULL;
    TOKEN_ELEVATION el;
    DWORD n = 0;
    int on = 0;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &tok)) return 0;
    if (GetTokenInformation(tok, TokenElevation, &el, sizeof el, &n)) on = el.TokenIsElevated ? 1 : 0;
    CloseHandle(tok);
    return on;
}

static int launch_user(const wchar_t *exe, const wchar_t *dir) {
    HWND shell = GetShellWindow();
    DWORD pid = 0;
    HANDLE proc = NULL, tok = NULL, dup = NULL;
    LPVOID env = NULL;
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    wchar_t cmd[MAX_PATH + 8];
    int ok = 0, n;
    if (!shell) return 0;
    GetWindowThreadProcessId(shell, &pid);
    proc = OpenProcess(PROCESS_QUERY_INFORMATION, FALSE, pid);
    if (!proc || !OpenProcessToken(proc, TOKEN_DUPLICATE | TOKEN_QUERY, &tok)) goto done;
    if (!DuplicateTokenEx(tok, TOKEN_QUERY | TOKEN_DUPLICATE | TOKEN_ASSIGN_PRIMARY | TOKEN_ADJUST_DEFAULT | TOKEN_ADJUST_SESSIONID, NULL, SecurityImpersonation, TokenPrimary, &dup))
        goto done;
    if (!CreateEnvironmentBlock(&env, dup, FALSE)) env = NULL;
    n = _snwprintf(cmd, MAX_PATH + 8, L"\"%s\"", exe);
    if (n < 0 || n >= MAX_PATH + 8) goto done;
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

static int writable(const wchar_t *dir) {
    wchar_t p[MAX_PATH];
    HANDLE h;
    path_join(p, MAX_PATH, dir, L"loft-upd.tmp");
    h = CreateFileW(p, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_HIDDEN, NULL);
    if (h == INVALID_HANDLE_VALUE) return 0;
    CloseHandle(h);
    DeleteFileW(p);
    return 1;
}

static int relaunch_elevated(const wchar_t *src, unsigned long pid, const wchar_t *dst) {
    SHELLEXECUTEINFOW si;
    wchar_t self[MAX_PATH], params[2048];
    int n;
    if (wcschr(src, L'"') || wcschr(dst, L'"')) return 0;
    if (!GetModuleFileNameW(NULL, self, MAX_PATH)) return 0;
    n = _snwprintf(params, 2048, L"--apply-update \"%s\" %lu \"%s\"", src, pid, dst);
    if (n < 0 || n >= 2048) return 0;
    memset(&si, 0, sizeof si);
    si.cbSize = sizeof si;
    si.fMask = SEE_MASK_NOCLOSEPROCESS | SEE_MASK_FLAG_NO_UI;
    si.lpVerb = L"runas";
    si.lpFile = self;
    si.lpParameters = params;
    si.nShow = SW_SHOWNORMAL;
    if (!ShellExecuteExW(&si)) return 0;
    if (si.hProcess) CloseHandle(si.hProcess);
    return 1;
}

static int launch_explorer(const wchar_t *exe) {
    wchar_t params[MAX_PATH + 8];
    SHELLEXECUTEINFOW si;
    int n;
    n = _snwprintf(params, MAX_PATH + 8, L"\"%s\"", exe);
    if (n < 0 || n >= MAX_PATH + 8) return 0;
    memset(&si, 0, sizeof si);
    si.cbSize = sizeof si;
    si.fMask = SEE_MASK_FLAG_NO_UI;
    si.lpFile = L"explorer.exe";
    si.lpParameters = params;
    si.nShow = SW_SHOWNORMAL;
    return ShellExecuteExW(&si) ? 1 : 0;
}

static int start_app(const wchar_t *dir) {
    wchar_t exe[MAX_PATH], cmd[MAX_PATH + 8];
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    int n;
    path_join(exe, MAX_PATH, dir, L"loft.exe");
    if (GetFileAttributesW(exe) == INVALID_FILE_ATTRIBUTES) return 0;
    if (elevated()) {
        if (launch_user(exe, dir)) return 1;
        return launch_explorer(exe);
    }
    n = _snwprintf(cmd, MAX_PATH + 8, L"\"%s\"", exe);
    if (n < 0 || n >= MAX_PATH + 8) return 0;
    memset(&si, 0, sizeof si);
    si.cb = sizeof si;
    memset(&pi, 0, sizeof pi);
    if (!CreateProcessW(exe, cmd, NULL, NULL, FALSE, 0, NULL, dir, &si, &pi)) return 0;
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    return 1;
}

int upd_apply(const wchar_t *src, unsigned long pid, const wchar_t *dst) {
    HANDLE ph;
    wchar_t exe[MAX_PATH];
    if (!src || !dst || !under_stage(src) || wcsstr(dst, L"..")) return 0;
    if (wcschr(src, L'"') || wcschr(dst, L'"')) return 0;
    ph = OpenProcess(SYNCHRONIZE, FALSE, (DWORD)pid);
    if (ph) {
        WaitForSingleObject(ph, 30000);
        CloseHandle(ph);
    }
    Sleep(200);
    if (!writable(dst)) {
        if (elevated()) return 0;
        return relaunch_elevated(src, pid, dst);
    }
    if (!copy_tree(src, dst)) return 0;
    path_join(exe, MAX_PATH, dst, L"loft.exe");
    if (GetFileAttributesW(exe) == INVALID_FILE_ATTRIBUTES) return 0;
    return start_app(dst);
}

int upd_stage(const wchar_t *zip) {
    wchar_t tmp[MAX_PATH], dir[MAX_PATH], exe[MAX_PATH], dest[MAX_PATH], cmd[2048];
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    int n;
    if (!zip || !GetTempPathW(MAX_PATH, tmp)) return 0;
    path_join(dir, MAX_PATH, tmp, L"loft-update");
    if (!under_stage(dir)) return 0;
    rm_tree(dir);
    if (!mkdir_one(dir) || !unzip_to(zip, dir)) return 0;
    path_join(exe, MAX_PATH, dir, L"loft.exe");
    if (GetFileAttributesW(exe) == INVALID_FILE_ATTRIBUTES) return 0;
    exe_dir(dest, MAX_PATH);
    if (!dest[0] || wcschr(dir, L'"') || wcschr(dest, L'"')) return 0;
    n = _snwprintf(cmd, 2048, L"\"%s\" --apply-update \"%s\" %lu \"%s\"", exe, dir, GetCurrentProcessId(), dest);
    if (n < 0 || n >= 2048) return 0;
    memset(&si, 0, sizeof si);
    si.cb = sizeof si;
    si.dwFlags = STARTF_USESHOWWINDOW;
    si.wShowWindow = SW_HIDE;
    memset(&pi, 0, sizeof pi);
    if (!CreateProcessW(exe, cmd, NULL, NULL, FALSE, 0, NULL, dir, &si, &pi)) return 0;
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    return 1;
}

