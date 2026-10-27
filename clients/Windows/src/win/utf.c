/* Windows client GUI: UTF-8 / UTF-16 conversion (Win32 APIs take wide strings,
   the core works in UTF-8), path joining and whole-file read/write helpers
   used across src/win. */
#include "common.h"

char *dupstr(const char *s) {
    size_t n;
    char *d;
    if (!s) s = "";
    n = strlen(s);
    d = (char *)malloc(n + 1);
    if (!d) return NULL;
    memcpy(d, s, n + 1);
    return d;
}

int widen(const char *u, wchar_t *d, int n) {
    int r;
    if (!d || n <= 0) return 0;
    d[0] = 0;
    if (!u) return 0;
    r = MultiByteToWideChar(CP_UTF8, 0, u, -1, d, n);
    if (r <= 0) d[0] = 0;
    return r > 0 ? r - 1 : 0;
}

int narrow(const wchar_t *w, char *d, int n) {
    int r;
    if (!d || n <= 0) return 0;
    d[0] = 0;
    if (!w) return 0;
    r = WideCharToMultiByte(CP_UTF8, 0, w, -1, d, n, NULL, NULL);
    if (r <= 0) d[0] = 0;
    return r > 0 ? r - 1 : 0;
}

void path_join(wchar_t *d, int n, const wchar_t *a, const wchar_t *b) {
    int i = 0, j = 0;
    if (!d || n <= 0) return;
    if (a)
        while (a[j] && i < n - 1) d[i++] = a[j++];
    if (i > 0 && d[i - 1] != L'\\' && i < n - 1) d[i++] = L'\\';
    j = 0;
    if (b)
        while (b[j] && i < n - 1) d[i++] = b[j++];
    d[i] = 0;
}

char *file_read(const wchar_t *path) {
    HANDLE h;
    DWORD sz, got = 0;
    char *b;
    h = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return NULL;
    sz = GetFileSize(h, NULL);
    if (sz == INVALID_FILE_SIZE || sz > 8u * 1024u * 1024u) {
        CloseHandle(h);
        return NULL;
    }
    b = (char *)malloc((size_t)sz + 1);
    if (!b) {
        CloseHandle(h);
        return NULL;
    }
    if (sz && !ReadFile(h, b, sz, &got, NULL)) {
        free(b);
        CloseHandle(h);
        return NULL;
    }
    CloseHandle(h);
    b[got] = 0;
    if (got >= 3 && (unsigned char)b[0] == 0xEF && (unsigned char)b[1] == 0xBB && (unsigned char)b[2] == 0xBF) {
        memmove(b, b + 3, got - 2);
    }
    return b;
}

int file_write(const wchar_t *path, const char *s) {
    if (!s) s = "";
    return file_write_bin(path, s, (int)strlen(s));
}

int file_write_bin(const wchar_t *path, const void *p, int n) {
    HANDLE h;
    DWORD w = 0;
    if (n < 0) return 0;
    h = CreateFileW(path, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return 0;
    if (n && !WriteFile(h, p, (DWORD)n, &w, NULL)) {
        CloseHandle(h);
        return 0;
    }
    CloseHandle(h);
    return w == (DWORD)n || n == 0;
}

int file_read_bin(const wchar_t *path, unsigned char **out, int *n) {
    HANDLE h;
    DWORD sz, got = 0;
    unsigned char *b;
    if (out) *out = NULL;
    if (n) *n = 0;
    h = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return 0;
    sz = GetFileSize(h, NULL);
    if (sz == INVALID_FILE_SIZE || sz > 1024u * 1024u) {
        CloseHandle(h);
        return 0;
    }
    b = (unsigned char *)malloc((size_t)sz + 1);
    if (!b) {
        CloseHandle(h);
        return 0;
    }
    if (sz && !ReadFile(h, b, sz, &got, NULL)) {
        free(b);
        CloseHandle(h);
        return 0;
    }
    CloseHandle(h);
    b[got] = 0;
    *out = b;
    *n = (int)got;
    return 1;
}
