/* Windows client GUI: network helpers that run on background threads.
   - http_get: a plain WinHTTP GET, also used by ui.c to pull split-routing
     lists from a zone's repository URL.
   - Update check and download from the GitHub releases of a3app/loft
     (not in the Store build); the verified zip goes to upd.c to install.
   - External IP lookup through the local SOCKS5 proxy of loft-front, so the
     answer shows the address the tunnel exits from.
   Workers report back by posting WM_LOFT_* messages to the hidden message
   window (main.c), which frees the posted strings. */
#include "common.h"

/* url_unescape turns JSON-escaped slashes ("\/") back into "/" in place. */
static void url_unescape(char *s) {
    char *r, *w;
    if (!s) return;
    for (r = w = s; *r; r++) {
        if (r[0] == '\\' && r[1] == '/') r++;
        *w++ = *r;
    }
    *w = 0;
}

/* http_read reads a whole WinHTTP response body into a new NUL-terminated
   buffer, growing it as data arrives, and stops at cap - 1 bytes. The
   caller frees *body. */
static int http_read(HINTERNET req, char **body, int cap) {
    char *buf;
    int n = 0;
    buf = (char *)malloc(4096);
    if (!buf) return 0;
    for (;;) {
        DWORD avail = 0, got = 0;
        char *nb;
        if (!WinHttpQueryDataAvailable(req, &avail)) break;
        if (avail == 0) break;
        if (n + (int)avail + 1 > cap) avail = (DWORD)(cap - n - 1);
        if (avail == 0) break;
        if (n + (int)avail + 1 > 4096 && n + (int)avail + 1 > n) {
            nb = (char *)realloc(buf, (size_t)n + avail + 1);
            if (!nb) break;
            buf = nb;
        }
        if (!WinHttpReadData(req, buf + n, avail, &got) || got == 0) break;
        n += (int)got;
        buf[n] = 0;
    }
    buf[n] = 0;
    *body = buf;
    return 1;
}

/* http_get fetches url with a GET and returns the body (at most 4 MB) in
   *body, which the caller frees. Fails on HTTP status 400 and above.
   TLS 1.1 and 1.2 are switched on explicitly because Windows 7 does not
   enable them by default. Requests to GitHub ask for the API's JSON format.
   Blocking: call it only from a worker thread. timeout_ms 0 means 15 s. */
int http_get(const char *url, char **body, DWORD timeout_ms) {
    wchar_t wurl[2048], host[256], path[1600], extra[600], full[2048];
    URL_COMPONENTS uc;
    HINTERNET ses = NULL, con = NULL, req = NULL;
    DWORD prot, status = 0, slen, flags;
    int ok = 0;
    if (body) *body = NULL;
    if (!url || !url[0] || !body) return 0;
    if (!widen(url, wurl, 2048)) return 0;
    memset(&uc, 0, sizeof uc);
    uc.dwStructSize = sizeof uc;
    uc.lpszHostName = host;
    uc.dwHostNameLength = 256;
    uc.lpszUrlPath = path;
    uc.dwUrlPathLength = 1600;
    uc.lpszExtraInfo = extra;
    uc.dwExtraInfoLength = 600;
    if (!WinHttpCrackUrl(wurl, 0, 0, &uc)) return 0;
    ses = WinHttpOpen(L"loft", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY, WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!ses) return 0;
    if (timeout_ms == 0) timeout_ms = 15000;
    WinHttpSetTimeouts(ses, timeout_ms, timeout_ms, timeout_ms, timeout_ms);
#ifndef WINHTTP_OPTION_SECURE_PROTOCOLS
#define WINHTTP_OPTION_SECURE_PROTOCOLS 84
#endif
#ifndef WINHTTP_FLAG_SECURE_PROTOCOL_TLS1_2
#define WINHTTP_FLAG_SECURE_PROTOCOL_TLS1_2 0x00000800
#define WINHTTP_FLAG_SECURE_PROTOCOL_TLS1_1 0x00000200
#define WINHTTP_FLAG_SECURE_PROTOCOL_TLS1 0x00000080
#endif
    prot = WINHTTP_FLAG_SECURE_PROTOCOL_TLS1 | WINHTTP_FLAG_SECURE_PROTOCOL_TLS1_1 | WINHTTP_FLAG_SECURE_PROTOCOL_TLS1_2;
    WinHttpSetOption(ses, WINHTTP_OPTION_SECURE_PROTOCOLS, &prot, sizeof prot);
    con = WinHttpConnect(ses, host, uc.nPort, 0);
    if (!con) goto out;
    full[0] = 0;
    wcsncpy(full, path, 1600);
    full[1599] = 0;
    if (extra[0]) wcsncat(full, extra, 400);
    flags = (uc.nScheme == INTERNET_SCHEME_HTTPS) ? WINHTTP_FLAG_SECURE : 0;
    req = WinHttpOpenRequest(con, L"GET", full, NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, flags);
    if (!req) goto out;
    if (wcsstr(host, L"github.com"))
        WinHttpAddRequestHeaders(req, L"Accept: application/vnd.github+json\r\n", (DWORD)-1, WINHTTP_ADDREQ_FLAG_ADD);
    if (!WinHttpSendRequest(req, WINHTTP_NO_ADDITIONAL_HEADERS, 0, WINHTTP_NO_REQUEST_DATA, 0, 0, 0)) goto out;
    if (!WinHttpReceiveResponse(req, NULL)) goto out;
    slen = sizeof status;
    WinHttpQueryHeaders(req, WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER, WINHTTP_HEADER_NAME_BY_INDEX, &status, &slen, WINHTTP_NO_HEADER_INDEX);
    if (status >= 400) goto out;
    if (!http_read(req, body, 4 * 1024 * 1024)) goto out;
    ok = 1;
out:
    if (req) WinHttpCloseHandle(req);
    if (con) WinHttpCloseHandle(con);
    if (ses) WinHttpCloseHandle(ses);
    if (!ok && body && *body) {
        free(*body);
        *body = NULL;
    }
    return ok;
}

#ifndef LOFT_STORE
/* http_save downloads url straight into a file (at most 64 MB), deleting
   the file on any failure. Used for the release zip and its checksum list. */
static int http_save(const char *url, const wchar_t *path) {
    wchar_t wurl[2048], host[256], upath[1600], extra[600], full[2048];
    URL_COMPONENTS uc;
    HINTERNET ses = NULL, con = NULL, req = NULL;
    HANDLE file = INVALID_HANDLE_VALUE;
    DWORD prot, status = 0, slen, flags, total = 0;
    int ok = 0;
    if (!widen(url, wurl, 2048)) return 0;
    memset(&uc, 0, sizeof uc);
    uc.dwStructSize = sizeof uc;
    uc.lpszHostName = host;
    uc.dwHostNameLength = 256;
    uc.lpszUrlPath = upath;
    uc.dwUrlPathLength = 1600;
    uc.lpszExtraInfo = extra;
    uc.dwExtraInfoLength = 600;
    if (!WinHttpCrackUrl(wurl, 0, 0, &uc)) return 0;
    ses = WinHttpOpen(L"loft", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY, WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!ses) return 0;
    WinHttpSetTimeouts(ses, 20000, 20000, 20000, 60000);
    prot = WINHTTP_FLAG_SECURE_PROTOCOL_TLS1 | WINHTTP_FLAG_SECURE_PROTOCOL_TLS1_1 | WINHTTP_FLAG_SECURE_PROTOCOL_TLS1_2;
    WinHttpSetOption(ses, WINHTTP_OPTION_SECURE_PROTOCOLS, &prot, sizeof prot);
    con = WinHttpConnect(ses, host, uc.nPort, 0);
    if (!con) goto out;
    wcsncpy(full, upath, 1600);
    full[1599] = 0;
    if (extra[0]) wcsncat(full, extra, 400);
    flags = (uc.nScheme == INTERNET_SCHEME_HTTPS) ? WINHTTP_FLAG_SECURE : 0;
    req = WinHttpOpenRequest(con, L"GET", full, NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, flags);
    if (!req) goto out;
    if (!WinHttpSendRequest(req, WINHTTP_NO_ADDITIONAL_HEADERS, 0, WINHTTP_NO_REQUEST_DATA, 0, 0, 0)) goto out;
    if (!WinHttpReceiveResponse(req, NULL)) goto out;
    slen = sizeof status;
    WinHttpQueryHeaders(req, WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER, WINHTTP_HEADER_NAME_BY_INDEX, &status, &slen, WINHTTP_NO_HEADER_INDEX);
    if (status >= 400) goto out;
    file = CreateFileW(path, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
    if (file == INVALID_HANDLE_VALUE) goto out;
    for (;;) {
        DWORD avail = 0, got = 0, wr = 0;
        char chunk[8192];
        if (!WinHttpQueryDataAvailable(req, &avail)) goto out;
        if (avail == 0) break;
        if (avail > sizeof chunk) avail = (DWORD)sizeof chunk;
        if (!WinHttpReadData(req, chunk, avail, &got) || got == 0) goto out;
        if (!WriteFile(file, chunk, got, &wr, NULL) || wr != got) goto out;
        total += wr;
        if (total > 64u * 1024u * 1024u) goto out;
    }
    ok = total > 0;
out:
    if (file != INVALID_HANDLE_VALUE) CloseHandle(file);
    if (!ok) DeleteFileW(path);
    if (req) WinHttpCloseHandle(req);
    if (con) WinHttpCloseHandle(con);
    if (ses) WinHttpCloseHandle(ses);
    return ok;
}
#endif

/* json_str is a tiny JSON lookup: it finds the first "key" in body and
   copies the string value that follows. It does not parse JSON in general;
   it is enough for the few fields read from the GitHub release reply and
   from an external IP service. */
static int json_str(const char *body, const char *key, char *dst, int n) {
    char pat[64];
    const char *p, *e;
    int i;
    if (!body || !dst || n <= 0) return 0;
    dst[0] = 0;
    snprintf(pat, sizeof pat, "\"%s\"", key);
    p = strstr(body, pat);
    if (!p) return 0;
    p = strchr(p + strlen(pat), '"');
    if (!p) return 0;
    p++;
    e = strchr(p, '"');
    if (!e || (int)(e - p) >= n) return 0;
    for (i = 0; p < e && i < n - 1; p++, i++) dst[i] = *p;
    dst[i] = 0;
    url_unescape(dst);
    return dst[0] != 0;
}

#ifndef LOFT_STORE
/* find_url scans a GitHub release reply for the first asset download URL
   that contains part (for example "-windows-x86-64.zip"). */
static int find_url(const char *body, const char *part, char *dst, int n) {
    const char *p = body;
    if (!body || !part || !dst || n <= 0) return 0;
    dst[0] = 0;
    while ((p = strstr(p, "browser_download_url")) != NULL) {
        char url[1024];
        if (json_str(p, "browser_download_url", url, (int)sizeof url) && strstr(url, part)) {
            loft_strlcpy(dst, (size_t)n, url);
            return 1;
        }
        p += 20;
    }
    return 0;
}

/* find_sums finds the download URL of the Windows SHA256SUMS asset in a
   GitHub release reply. */
static int find_sums(const char *body, char *dst, int n) {
    const char *p = body;
    if (!body || !dst || n <= 0) return 0;
    dst[0] = 0;
    while ((p = strstr(p, "browser_download_url")) != NULL) {
        char url[1024];
        if (json_str(p, "browser_download_url", url, (int)sizeof url) && strstr(url, "SHA256SUMS") && strstr(url, "windows")) {
            loft_strlcpy(dst, (size_t)n, url);
            return 1;
        }
        p += 20;
    }
    return 0;
}

/* url_name returns the file name at the end of a URL, without any
   "?query". The result may point into a static buffer. */
static const char *url_name(const char *url) {
    const char *s = url ? strrchr(url, '/') : NULL;
    const char *q;
    s = s ? s + 1 : url;
    if (!s) return "";
    q = strchr(s, '?');
    if (q) {
        static char tmp[256];
        size_t n = (size_t)(q - s);
        if (n >= sizeof tmp) n = sizeof tmp - 1;
        memcpy(tmp, s, n);
        tmp[n] = 0;
        return tmp;
    }
    return s;
}

/* post_upd sends an update result to the UI thread as WM_LOFT_UPD with a
   heap copy of text. kind 0: a newer version (text is the tag); 2: a
   failure to log; 3: the update is staged and the app should quit. */
static void post_upd(WPARAM kind, const char *text) {
    char *d = dupstr(text ? text : "");
    if (!d) return;
    if (!g.msg || !PostMessageW(g.msg, WM_LOFT_UPD, kind, (LPARAM)d)) free(d);
}

/* find_zip picks the portable zip for this architecture. Prefers the Dist
   names windows-x86-64 / windows-arm64; still accepts the older windows-x64.
   llvm-mingw clang defines __aarch64__, not MSVC _M_ARM64. */
static int find_zip(const char *body, char *dst, int n) {
#if defined(_M_ARM64) || defined(__aarch64__)
    return find_url(body, "-windows-arm64.zip", dst, n);
#elif defined(_WIN64)
    if (find_url(body, "-windows-x86-64.zip", dst, n)) return 1;
    return find_url(body, "-windows-x64.zip", dst, n);
#else
    return find_url(body, "-windows-x86.zip", dst, n);
#endif
}

/* update_worker asks GitHub for the latest release and, when its tag is
   newer than this build and has a zip for this architecture, reports the
   tag so the UI can offer the update. Nothing is reported when already up
   to date. */
static DWORD WINAPI update_worker(LPVOID arg) {
    char *body = NULL;
    char tag[64], url[1024];
    (void)arg;
    if (!http_get("https://api.github.com/repos/a3app/loft/releases/latest", &body, 20000) || !body) {
        post_upd(2, "update");
        return 0;
    }
    if (!json_str(body, "tag_name", tag, (int)sizeof tag) || !loft_newer(tag, LOFT_VERSION) || !find_zip(body, url, (int)sizeof url)) {
        free(body);
        return 0;
    }
    free(body);
    post_upd(0, tag);
    return 0;
}

/* download_worker installs the latest release: it downloads the zip and the
   SHA256SUMS file to %TEMP%, checks the zip's hash against the list, and
   hands the zip to upd_stage. Any failure deletes the downloads and is
   reported. After a successful stage the app is told to quit so the new
   version can replace it. */
static DWORD WINAPI download_worker(LPVOID arg) {
    char *body = NULL;
    char url[1024], sums_url[1024], hex[80], line[1100];
    wchar_t path[MAX_PATH], sums_path[MAX_PATH], dir[MAX_PATH];
    const char *name;
    char *sums = NULL;
    (void)arg;
    if (!http_get("https://api.github.com/repos/a3app/loft/releases/latest", &body, 20000) || !body || !find_zip(body, url, (int)sizeof url)) {
        free(body);
        post_upd(2, "update");
        return 0;
    }
    find_sums(body, sums_url, (int)sizeof sums_url);
    free(body);
    if (!sums_url[0] || !GetTempPathW(MAX_PATH, dir)) {
        post_upd(2, "update");
        return 0;
    }
    path_join(path, MAX_PATH, dir, L"loft-update.zip");
    path_join(sums_path, MAX_PATH, dir, L"loft-update.sums");
    if (!http_save(url, path)) {
        post_upd(2, "update");
        return 0;
    }
    name = url_name(url);
    sums = NULL;
    if (!http_save(sums_url, sums_path) || !(sums = file_read(sums_path)) || !upd_sha256_hex(path, hex, (int)sizeof hex) || !upd_sums_match(sums, name, hex)) {
        free(sums);
        DeleteFileW(path);
        DeleteFileW(sums_path);
        post_upd(2, "update");
        return 0;
    }
    free(sums);
    if (!upd_stage(path)) {
        post_upd(2, "update");
        return 0;
    }
    narrow(path, line, (int)sizeof line);
    post_upd(3, line);
    return 0;
}
#endif

/* thread_detach starts a worker thread and lets it run on its own. If the
   thread cannot start, arg is freed because no one else will. */
static void thread_detach(LPTHREAD_START_ROUTINE fn, void *arg) {
    HANDLE th = CreateThread(NULL, 0, fn, arg, 0, NULL);
    if (th) CloseHandle(th);
    else free(arg);
}

/* net_update_check starts a background update check, if the user allows
   update checks. The Store build never checks. */
void net_update_check(void) {
#ifdef LOFT_STORE
    return;
#else
    if (!g.prefs.update_check || !g.msg) return;
    thread_detach(update_worker, NULL);
#endif
}

/* net_download starts downloading and installing the latest release in the
   background. The Store build does nothing. */
void net_download(void) {
#ifdef LOFT_STORE
    return;
#else
    if (!g.msg) return;
    thread_detach(download_worker, NULL);
#endif
}

/* take_ip pulls the IP address out of an external IP service reply: either
   a JSON object with an "ip" field or plain text. Only the first word is
   kept. */
static void take_ip(const char *body, char *dst, int n) {
    const char *p;
    char *q;
    if (!dst || n <= 0) return;
    dst[0] = 0;
    if (!body) return;
    p = body;
    while (*p == ' ' || *p == '\r' || *p == '\n' || *p == '\t') p++;
    if (*p == '{') {
        if (!json_str(body, "ip", dst, n)) return;
    } else {
        loft_strlcpy(dst, (size_t)n, p);
    }
    for (q = dst; *q && *q != '\r' && *q != '\n' && *q != ' ' && *q != '"'; q++) {
    }
    *q = 0;
}

/* socks_get makes a plain HTTP/1.0 GET through the local SOCKS5 proxy on
   127.0.0.1:socks_port, so the request leaves through the tunnel. The host
   name is resolved locally to IPv4 and the proxy is asked to connect to
   that address (no authentication). Returns the body without headers in
   *body (caller frees), at most about 1 MB. Socket timeouts are 8 s. */
static int socks_get(int socks_port, int dest_port, const char *host, const char *path, char **body) {
    struct addrinfo hints, *res = NULL;
    SOCKET s = INVALID_SOCKET;
    unsigned char greq[8], grep[8], creq[32], crep[32];
    char http[1024];
    char *buf;
    struct sockaddr_in in;
    DWORD tv = 8000;
    int n = 0, cap = 8192, http_n;
    if (body) *body = NULL;
    memset(&hints, 0, sizeof hints);
    hints.ai_family = AF_INET;
    if (getaddrinfo(host, NULL, &hints, &res) != 0 || !res) return 0;
    s = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (s == INVALID_SOCKET) {
        freeaddrinfo(res);
        return 0;
    }
    setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, (char *)&tv, sizeof tv);
    setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, (char *)&tv, sizeof tv);
    memset(&in, 0, sizeof in);
    in.sin_family = AF_INET;
    in.sin_addr.s_addr = htonl(0x7F000001);
    in.sin_port = htons((unsigned short)socks_port);
    /* SOCKS5 greeting (version 5, one method: no auth), then CONNECT to the
       IPv4 address and port. */
    if (connect(s, (struct sockaddr *)&in, sizeof in) != 0) goto fail;
    greq[0] = 5;
    greq[1] = 1;
    greq[2] = 0;
    if (send(s, (char *)greq, 3, 0) != 3) goto fail;
    if (recv(s, (char *)grep, 2, 0) != 2 || grep[0] != 5 || grep[1] != 0) goto fail;
    creq[0] = 5;
    creq[1] = 1;
    creq[2] = 0;
    creq[3] = 1;
    memcpy(creq + 4, &((struct sockaddr_in *)res->ai_addr)->sin_addr, 4);
    if (dest_port <= 0 || dest_port > 65535) dest_port = 80;
    creq[8] = (unsigned char)((dest_port >> 8) & 0xff);
    creq[9] = (unsigned char)(dest_port & 0xff);
    if (send(s, (char *)creq, 10, 0) != 10) goto fail;
    if (recv(s, (char *)crep, 10, 0) < 2 || crep[1] != 0) goto fail;
    http_n = snprintf(http, sizeof http, "GET %s HTTP/1.0\r\nHost: %s\r\nConnection: close\r\n\r\n", path[0] ? path : "/", host);
    if (http_n <= 0 || send(s, http, http_n, 0) != http_n) goto fail;
    buf = (char *)malloc((size_t)cap);
    if (!buf) goto fail;
    while (n < cap - 1) {
        int r = recv(s, buf + n, cap - 1 - n, 0);
        if (r <= 0) break;
        n += r;
        if (n > cap - 1024) {
            char *nb;
            cap *= 2;
            if (cap > 1024 * 1024) break;
            nb = (char *)realloc(buf, (size_t)cap);
            if (!nb) break;
            buf = nb;
        }
    }
    buf[n] = 0;
    {
        char *sep = strstr(buf, "\r\n\r\n");
        if (sep) memmove(buf, sep + 4, strlen(sep + 4) + 1);
    }
    *body = buf;
    closesocket(s);
    freeaddrinfo(res);
    return 1;
fail:
    if (s != INVALID_SOCKET) closesocket(s);
    if (res) freeaddrinfo(res);
    return 0;
}

/* ExtJob is the job for extip_worker: the session slot to report to, the
   local SOCKS port, and the lookup URL from prefs. */
typedef struct ExtJob {
    int slot;
    int port;
    char url[LOFT_URL];
} ExtJob;

/* extip_worker looks up the external IP address through SOCKS and posts it
   (empty on failure) as WM_LOFT_EXTIP. An https:// URL is fetched as plain
   http on port 80, because socks_get speaks only plain HTTP and Windows 7
   may not support TLS 1.2. The default service is api.ipify.org. */
static DWORD WINAPI extip_worker(LPVOID arg) {
    ExtJob *job = (ExtJob *)arg;
    char url[LOFT_URL], host[256], path[512], ip[48];
    char *body = NULL;
    char *posted;
    const char *h, *sl, *colon;
    int port = 80, https = 0;
    ip[0] = 0;
    loft_strlcpy(url, sizeof url, job->url[0] ? job->url : "https://api.ipify.org");
    if (loft_starts(url, "https://")) {
        https = 1;
        memmove(url + 4, url + 5, strlen(url + 5) + 1);
    }
    h = url;
    if (loft_starts(h, "http://")) h += 7;
    sl = strchr(h, '/');
    colon = strchr(h, ':');
    if (colon && (!sl || colon < sl)) {
        int hn = (int)(colon - h);
        if (hn > 0 && hn < (int)sizeof host) {
            memcpy(host, h, (size_t)hn);
            host[hn] = 0;
        } else host[0] = 0;
        port = atoi(colon + 1);
    } else {
        int hn = sl ? (int)(sl - h) : (int)strlen(h);
        if (hn <= 0 || hn >= (int)sizeof host) hn = 0;
        if (hn) memcpy(host, h, (size_t)hn);
        host[hn] = 0;
    }
    loft_strlcpy(path, sizeof path, sl ? sl : "/");
    if (https && port == 443) port = 80;
    if (port <= 0 || port > 65535) port = 80;
    if (host[0] && socks_get(job->port, port, host, path, &body)) take_ip(body, ip, (int)sizeof ip);
    free(body);
    posted = dupstr(ip);
    if (posted && !PostMessageW(g.msg, WM_LOFT_EXTIP, (WPARAM)job->slot, (LPARAM)posted)) free(posted);
    free(job);
    return 0;
}

/* net_extip starts an external IP lookup for a session in the background.
   It uses the SOCKS port from prefs (default 1080). */
void net_extip(int slot) {
    ExtJob *job;
    if (slot < 0 || slot >= LOFT_SESS || !g.msg) return;
    job = (ExtJob *)calloc(1, sizeof *job);
    if (!job) return;
    job->slot = slot;
    job->port = g.prefs.socks_port > 0 ? g.prefs.socks_port : 1080;
    loft_strlcpy(job->url, sizeof job->url, g.prefs.ext_ip_url);
    thread_detach(extip_worker, job);
}
