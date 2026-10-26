#include "common.h"

static void host_of(const char *url, char *host, int n) {
    const char *s, *sl, *at, *end, *colon;
    if (!host || n <= 0) return;
    host[0] = 0;
    if (!url || !url[0]) return;
    s = strstr(url, "://");
    s = s ? s + 3 : url;
    sl = strchr(s, '/');
    at = strchr(s, '@');
    if (at && (!sl || at < sl)) s = at + 1;
    if (*s == '[') {
        const char *rb = strchr(s, ']');
        int len;
        if (!rb) return;
        len = (int)(rb - s - 1);
        if (len <= 0 || len >= n) return;
        memcpy(host, s + 1, (size_t)len);
        host[len] = 0;
        return;
    }
    end = s + strlen(s);
    if (sl && sl < end) end = sl;
    colon = strchr(s, ':');
    if (colon && colon < end) end = colon;
    if ((int)(end - s) <= 0 || (int)(end - s) >= n) return;
    memcpy(host, s, (size_t)(end - s));
    host[end - s] = 0;
}

static void fill_excl(Sess *s) {
    char host[256];
    struct addrinfo hints, *res = NULL, *ai;
    const LoftProfile *p;
    if (!s || s->nexcl || s->profile < 0 || s->profile >= g.nprof) return;
    p = &g.prof[s->profile];
    host_of(p->url, host, (int)sizeof host);
    if (!host[0]) return;
    memset(&hints, 0, sizeof hints);
    hints.ai_family = AF_INET;
    if (getaddrinfo(host, NULL, &hints, &res) != 0) {
        journal_add("exclude dns");
        return;
    }
    for (ai = res; ai && s->nexcl < 4; ai = ai->ai_next) {
        char buf[48];
        struct sockaddr_in *in;
        if (ai->ai_family != AF_INET) continue;
        in = (struct sockaddr_in *)ai->ai_addr;
        if (!inet_ntop(AF_INET, &in->sin_addr, buf, sizeof buf)) continue;
        loft_strlcpy(s->excl[s->nexcl], sizeof s->excl[0], buf);
        s->nexcl++;
    }
    freeaddrinfo(res);
}

static int svc_ok(const char *cmd) {
    char resp[256];
    if (svc_call(cmd, resp, (int)sizeof resp)) return 1;
    if (resp[0]) journal_add(resp);
    return 0;
}

static void apply_routes(Sess *s) {
    char cmd[192];
    char ip[64];
    unsigned a, b, c, d;
    int i;
    if (!s || !s->overlay[0]) return;
    loft_strlcpy(ip, sizeof ip, s->overlay);
    {
        char *cut = strchr(ip, '%');
        if (cut) *cut = 0;
        cut = strchr(ip, '/');
        if (cut) *cut = 0;
    }
    snprintf(cmd, sizeof cmd, "hold %d", g.prefs.hold ? 1 : 0);
    svc_ok(cmd);
    snprintf(cmd, sizeof cmd, "addr %s 32", ip);
    if (!svc_ok(cmd)) return;
    if (s->dns[0]) {
        snprintf(cmd, sizeof cmd, "dns %s", s->dns);
        svc_ok(cmd);
    }
    svc_ok("routes-clear");
    for (i = 0; i < s->nexcl; i++) {
        snprintf(cmd, sizeof cmd, "exclude %s", s->excl[i]);
        svc_ok(cmd);
    }
    if (g.prefs.split) {
        for (i = 0; i < g.routes.ns; i++) {
            const LoftSet *set = &g.routes.sets[i];
            int zi, via = 1;
            if (!set->cidr[0] || loft_streq(set->src, "ignore")) continue;
            for (zi = 0; zi < g.routes.nz; zi++) {
                if (loft_streq(g.routes.zones[zi].id, set->zone)) via = g.routes.zones[zi].def_via;
            }
            snprintf(cmd, sizeof cmd, "%s %s", via ? "route" : "direct", set->cidr);
            svc_ok(cmd);
        }
    }
    if (g.prefs.defroute) {
        svc_ok("route-default on");
    } else if (sscanf(ip, "%u.%u.%u.%u", &a, &b, &c, &d) == 4) {
        snprintf(cmd, sizeof cmd, "route %u.%u.%u.0/24", a, b, c);
        svc_ok(cmd);
    }
    s->routed = 1;
    g.held = 0;
}

void session_apply_split(void) {
    int i;
    for (i = 0; i < LOFT_SESS; i++) {
        if (g.sess[i].used && g.sess[i].owns_tun && g.sess[i].phase == PH_ON && g.sess[i].overlay[0]) {
            g.sess[i].routed = 0;
            apply_routes(&g.sess[i]);
        }
    }
    redraw_all();
}

static int socks_owner_slot(void) {
    int i, first = -1, own = -1;
    for (i = 0; i < LOFT_SESS; i++) {
        if (!g.sess[i].used) continue;
        if (first < 0) first = i;
        if (g.sess[i].owns_tun) own = i;
    }
    return own >= 0 ? own : first;
}

static void socks_arg(Sess *s, char *dst, int n) {
    const LoftProfile *p = &g.prof[s->profile];
    if (loft_streq(p->socks, "off")) {
        loft_strlcpy(dst, (size_t)n, "off");
        return;
    }
    if (p->socks[0] && !loft_streq(p->socks, "auto")) {
        if (strchr(p->socks, ':')) loft_strlcpy(dst, (size_t)n, p->socks);
        else snprintf(dst, (size_t)n, "127.0.0.1:%s", p->socks);
        return;
    }
    if (!g.prefs.socks_share) {
        int port = g.prefs.socks_port + s->socks_slot;
        if (port > 65535) port = g.prefs.socks_port;
        snprintf(dst, (size_t)n, "127.0.0.1:%d", port);
        return;
    }
    if (socks_owner_slot() == (int)(s - g.sess)) snprintf(dst, (size_t)n, "127.0.0.1:%d", g.prefs.socks_port);
    else loft_strlcpy(dst, (size_t)n, "off");
}

static void cmd_put(wchar_t *d, int *len, int cap, const wchar_t *s) {
    int q = 0, i;
    if (!s) s = L"";
    for (i = 0; s[i]; i++)
        if (s[i] == L' ' || s[i] == L'\t' || s[i] == L'"') q = 1;
    if (*len && *len < cap - 1) d[(*len)++] = L' ';
    if (q && *len < cap - 1) d[(*len)++] = L'"';
    for (i = 0; s[i] && *len < cap - 1; i++) {
        if (s[i] == L'"' && *len < cap - 1) d[(*len)++] = L'\\';
        if (*len < cap - 1) d[(*len)++] = s[i];
    }
    if (q && *len < cap - 1) d[(*len)++] = L'"';
    d[*len] = 0;
}

typedef struct RdArg {
    int slot;
    HANDLE pipe;
} RdArg;

static DWORD WINAPI reader_thread(LPVOID arg) {
    RdArg *r = (RdArg *)arg;
    char line[1024];
    int n = 0;
    if (!r) return 0;
    for (;;) {
        char tmp[256];
        DWORD got = 0;
        int i;
        if (!ReadFile(r->pipe, tmp, sizeof tmp, &got, NULL) || got == 0) break;
        for (i = 0; i < (int)got; i++) {
            char ch = tmp[i];
            if (ch == '\n' || n >= (int)sizeof line - 1) {
                line[n] = 0;
                if (n > 0 && line[n - 1] == '\r') line[n - 1] = 0;
                if (line[0]) {
                    char *copy = dupstr(line);
                    if (copy && !PostMessageW(g.msg, WM_LOFT_STATUS, (WPARAM)r->slot, (LPARAM)copy)) free(copy);
                }
                n = 0;
            } else {
                line[n++] = ch;
            }
        }
    }
    PostMessageW(g.msg, WM_LOFT_EXIT, (WPARAM)r->slot, 0);
    free(r);
    return 0;
}

static int spawn_front(Sess *s, int with_tun) {
    wchar_t exe[MAX_PATH], dir[MAX_PATH], cmd[8192], ipath[MAX_PATH];
    char socks[64], ipath_u[MAX_PATH * 3];
    char inst_dir_u[16];
    LoftArgv av;
    SECURITY_ATTRIBUTES sa;
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    JOBOBJECT_EXTENDED_LIMIT_INFORMATION lim;
    HANDLE rd = NULL, wr = NULL, nul = NULL, job = NULL;
    RdArg *ra;
    int i, len = 0;
    const char *key;
    wchar_t warg[1024];
    (void)inst_dir_u;
    if (!s || s->profile < 0 || s->profile >= g.nprof) return 0;
    exe_file(exe, MAX_PATH, L"loft-front.exe");
    if (GetFileAttributesW(exe) == INVALID_FILE_ATTRIBUTES) {
        s->phase = PH_ERR;
        journal_add(loft_t("err.front"));
        tray_update();
        redraw_all();
        return 0;
    }
    exe_dir(dir, MAX_PATH);
    {
        wchar_t sdir[MAX_PATH];
        char idname[LOFT_ID + 8];
        wchar_t wid[LOFT_ID + 8];
        wchar_t root[MAX_PATH];
        store_root(root, MAX_PATH);
        path_join(sdir, MAX_PATH, root, L"sessions");
        CreateDirectoryW(sdir, NULL);
        snprintf(idname, sizeof idname, "%s", g.prof[s->profile].id);
        widen(idname, wid, LOFT_ID + 8);
        path_join(ipath, MAX_PATH, sdir, wid);
    }
    narrow(ipath, ipath_u, (int)sizeof ipath_u);
    socks_arg(s, socks, (int)sizeof socks);
    key = g.key[s->profile];
    if (!loft_build_argv(&av, "loft-front.exe", &g.prof[s->profile], key, socks, with_tun ? "\\\\.\\pipe\\loft-tun" : NULL, ipath_u))
        return 0;
    cmd[0] = 0;
    for (i = 0; i < av.n; i++) {
        widen(av.store[i], warg, 1024);
        if (i == 0) cmd_put(cmd, &len, 8192, exe);
        else cmd_put(cmd, &len, 8192, warg);
        loft_wipe(warg, sizeof warg);
    }
    for (i = 0; i < av.n; i++) loft_wipe(av.store[i], sizeof av.store[i]);
    memset(&sa, 0, sizeof sa);
    sa.nLength = sizeof sa;
    sa.bInheritHandle = TRUE;
    if (!CreatePipe(&rd, &wr, &sa, 0)) {
        loft_wipe(cmd, sizeof cmd);
        return 0;
    }
    SetHandleInformation(rd, HANDLE_FLAG_INHERIT, 0);
    nul = CreateFileW(L"NUL", GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE, &sa, OPEN_EXISTING, 0, NULL);
    memset(&si, 0, sizeof si);
    si.cb = sizeof si;
    si.dwFlags = STARTF_USESTDHANDLES | STARTF_USESHOWWINDOW;
    si.wShowWindow = SW_HIDE;
    si.hStdInput = nul;
    si.hStdOutput = wr;
    si.hStdError = wr;
    memset(&pi, 0, sizeof pi);
    if (!CreateProcessW(exe, cmd, NULL, NULL, TRUE, CREATE_NO_WINDOW | CREATE_SUSPENDED | CREATE_UNICODE_ENVIRONMENT, NULL, dir, &si, &pi)) {
        journal_add("loft-front");
        CloseHandle(rd);
        CloseHandle(wr);
        if (nul) CloseHandle(nul);
        loft_wipe(cmd, sizeof cmd);
        s->phase = PH_ERR;
        return 0;
    }
    loft_wipe(cmd, sizeof cmd);
    job = CreateJobObjectW(NULL, NULL);
    if (job) {
        memset(&lim, 0, sizeof lim);
        lim.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        SetInformationJobObject(job, JobObjectExtendedLimitInformation, &lim, sizeof lim);
        AssignProcessToJobObject(job, pi.hProcess);
    }
    ResumeThread(pi.hThread);
    CloseHandle(pi.hThread);
    CloseHandle(wr);
    if (nul) CloseHandle(nul);
    s->proc = pi.hProcess;
    s->job = job;
    s->out_r = rd;
    s->phase = PH_CONNECTING;
    s->up_since = 0;
    ra = (RdArg *)malloc(sizeof *ra);
    if (ra) {
        ra->slot = (int)(s - g.sess);
        ra->pipe = rd;
        s->rd_thread = CreateThread(NULL, 0, reader_thread, ra, 0, NULL);
        if (!s->rd_thread) free(ra);
    }
    tray_update();
    redraw_all();
    return 1;
}

static DWORD WINAPI tun_worker(LPVOID arg) {
    int slot = (int)(intptr_t)arg;
    char *resp = (char *)malloc(256);
    int ok;
    if (!resp) return 0;
    ok = svc_call_to("tun-open", resp, 256, 30000);
    if (!PostMessageW(g.msg, WM_LOFT_TUN, (WPARAM)((slot << 8) | (ok ? 1 : 0)), (LPARAM)resp)) free(resp);
    return 0;
}

static int alloc_slot(int profile) {
    int i, slot = -1;
    for (i = 0; i < LOFT_SESS; i++) {
        if (g.sess[i].used && g.sess[i].profile == profile) return i;
    }
    if (!g.prefs.multi) {
        for (i = 0; i < LOFT_SESS; i++)
            if (g.sess[i].used) session_disconnect_slot(i);
    }
    for (i = 0; i < LOFT_SESS; i++) {
        if (!g.sess[i].used) {
            slot = i;
            break;
        }
    }
    if (slot < 0) return -1;
    memset(&g.sess[slot], 0, sizeof g.sess[slot]);
    g.sess[slot].used = 1;
    g.sess[slot].profile = profile;
    g.sess[slot].socks_slot = slot;
    return slot;
}

void session_connect(int profile) {
    Sess *s;
    int slot, i;
    if (profile < 0 || profile >= g.nprof) return;
    if (!g.key[profile][0]) {
        journal_add(loft_t("need_key"));
        open_edit(profile);
        return;
    }
    slot = alloc_slot(profile);
    if (slot < 0) return;
    s = &g.sess[slot];
    if (s->tun_wait) return;
    s->user_stop = 0;
    s->restart_socks = 0;
    s->want_spawn = 0;
    s->phase = PH_CONNECTING;
    s->routed = 0;
    s->msg[0] = 0;
    g.active = profile;
    store_save_active();
    fill_excl(s);
    KillTimer(g.msg, T_RETRY + slot);
    if (g.prof[profile].tun) {
#ifdef LOFT_STORE
        (void)i;
        journal_add(loft_t("store.tun"));
        s->want_tun = 0;
        s->owns_tun = 0;
        spawn_front(s, 0);
#else
        s->want_tun = 1;
        for (i = 0; i < LOFT_SESS; i++) {
            if (i != slot && g.sess[i].used && g.sess[i].owns_tun) {
                g.sess[i].owns_tun = 0;
                g.sess[i].restart_socks = 1;
                if (g.sess[i].proc) TerminateProcess(g.sess[i].proc, 0);
            }
        }
        s->owns_tun = 1;
        if (s->proc) {
            s->want_spawn = 1;
            TerminateProcess(s->proc, 0);
            tray_update();
            redraw_all();
            return;
        }
        s->tun_wait = 1;
        CloseHandle(CreateThread(NULL, 0, tun_worker, (LPVOID)(intptr_t)slot, 0, NULL));
#endif
    } else {
        s->want_tun = 0;
        s->owns_tun = 0;
        if (s->proc) {
            s->want_spawn = 1;
            TerminateProcess(s->proc, 0);
        } else {
            spawn_front(s, 0);
        }
    }
    tray_update();
    redraw_all();
}

void session_disconnect_slot(int slot) {
    Sess *s;
    char resp[64];
    if (slot < 0 || slot >= LOFT_SESS || !g.sess[slot].used) return;
    s = &g.sess[slot];
    s->user_stop = 1;
    s->want_spawn = 0;
    s->restart_socks = 0;
    s->tun_wait = 0;
    KillTimer(g.msg, T_RETRY + slot);
    if (s->proc) {
        TerminateProcess(s->proc, 0);
        return;
    }
    if (s->owns_tun) {
        if (g.prefs.hold) {
            s->phase = PH_HELD;
            g.held = 1;
        } else {
            svc_call("hold 0", resp, (int)sizeof resp);
            svc_call("tun-close", resp, (int)sizeof resp);
            s->phase = PH_OFF;
            s->owns_tun = 0;
            g.held = 0;
        }
    } else {
        s->phase = PH_OFF;
    }
    tray_update();
    redraw_all();
}

void session_disconnect_active(void) {
    int i, any = 0;
    for (i = 0; i < LOFT_SESS; i++) {
        if (!g.sess[i].used) continue;
        if (g.sess[i].phase == PH_ON || g.sess[i].phase == PH_CONNECTING || g.sess[i].owns_tun) {
            session_disconnect_slot(i);
            any = 1;
        }
    }
    if (!any && g.active >= 0) {
        for (i = 0; i < LOFT_SESS; i++)
            if (g.sess[i].used && g.sess[i].profile == g.active) session_disconnect_slot(i);
    }
}

void session_reset_routes(void) {
    char resp[128];
    int i;
    g.held = 0;
    g.prefs.hold = 0;
    for (i = 0; i < LOFT_SESS; i++) {
        if (!g.sess[i].used) continue;
        g.sess[i].user_stop = 1;
        g.sess[i].want_spawn = 0;
        g.sess[i].restart_socks = 0;
        if (g.sess[i].proc) TerminateProcess(g.sess[i].proc, 0);
        g.sess[i].owns_tun = 0;
        if (g.sess[i].phase == PH_HELD) g.sess[i].phase = PH_OFF;
    }
    svc_call("hold 0", resp, (int)sizeof resp);
    svc_call("reset", resp, (int)sizeof resp);
    store_save_prefs();
    tray_update();
    redraw_all();
}

static int secret_line(const char *line) {
    int i;
    for (i = 0; i < g.nprof; i++) {
        if (g.key[i][0] && strlen(g.key[i]) > 6 && strstr(line, g.key[i])) return 1;
    }
    return 0;
}

void session_on_status(int slot, char *line) {
    Sess *s;
    LoftStatus st;
    if (slot < 0 || slot >= LOFT_SESS || !line) return;
    s = &g.sess[slot];
    if (!s->used) return;
    if (secret_line(line)) return;
    if (!loft_starts(line, "STATUS") && !strstr(line, "state=")) {
        journal_add(line);
        return;
    }
    if (!loft_parse_status(line, &st)) {
        journal_add(line);
        return;
    }
    if (st.msg[0] && !loft_streq(st.msg, s->msg)) {
        journal_add(st.msg);
        loft_strlcpy(s->msg, sizeof s->msg, st.msg);
    }
    if (st.front[0]) loft_strlcpy(s->front, sizeof s->front, st.front);
    if (st.has_up) s->up = st.up;
    if (st.has_down) s->down = st.down;
    if (loft_streq(st.state, "on")) {
        s->phase = PH_ON;
        s->retry_n = 0;
        if (!s->up_since) s->up_since = GetTickCount();
        if (st.ip[0] && !loft_streq(st.ip, s->overlay)) {
            loft_strlcpy(s->overlay, sizeof s->overlay, st.ip);
            s->routed = 0;
        }
        if (st.dns[0]) loft_strlcpy(s->dns, sizeof s->dns, st.dns);
        if (s->owns_tun && !s->routed && s->overlay[0]) apply_routes(s);
        if (g.prefs.ext_ip && !s->ext_busy && !s->ext_ip[0]) {
            s->ext_busy = 1;
            net_extip(slot);
        }
    } else if (loft_streq(st.state, "error")) {
        s->phase = PH_ERR;
    } else if (loft_streq(st.state, "connecting") && s->phase != PH_ON) {
        s->phase = PH_CONNECTING;
    }
    tray_update();
    redraw_all();
}

void session_on_exit(int slot) {
    Sess *s;
    int want, want_tun, restart, tun;
    char resp[64];
    if (slot < 0 || slot >= LOFT_SESS) return;
    s = &g.sess[slot];
    if (s->rd_thread) {
        WaitForSingleObject(s->rd_thread, 2000);
        CloseHandle(s->rd_thread);
        s->rd_thread = NULL;
    }
    if (s->out_r) {
        CloseHandle(s->out_r);
        s->out_r = NULL;
    }
    if (s->proc) {
        WaitForSingleObject(s->proc, 500);
        CloseHandle(s->proc);
        s->proc = NULL;
    }
    if (s->job) {
        CloseHandle(s->job);
        s->job = NULL;
    }
    if (!s->used || g.quit) return;
    want = s->want_spawn;
    want_tun = s->want_tun;
    restart = s->restart_socks;
    tun = s->owns_tun;
    s->want_spawn = 0;
    s->restart_socks = 0;
    if (want) {
        if (want_tun) {
            s->tun_wait = 1;
            CloseHandle(CreateThread(NULL, 0, tun_worker, (LPVOID)(intptr_t)slot, 0, NULL));
        } else {
            spawn_front(s, 0);
        }
        return;
    }
    if (restart) {
        s->owns_tun = 0;
        spawn_front(s, 0);
        return;
    }
    if (s->user_stop) {
        if (tun) {
            if (g.prefs.hold) {
                s->phase = PH_HELD;
                g.held = 1;
            } else {
                svc_call("hold 0", resp, (int)sizeof resp);
                svc_call("tun-close", resp, (int)sizeof resp);
                s->phase = PH_OFF;
                s->owns_tun = 0;
                g.held = 0;
            }
        } else {
            s->phase = PH_OFF;
        }
        s->routed = 0;
        tray_update();
        redraw_all();
        return;
    }
    if (g.prefs.reconnect && s->retry_n < 8) {
        s->retry_n++;
        s->phase = PH_CONNECTING;
        SetTimer(g.msg, T_RETRY + slot, s->retry_n < 4 ? 2000u * (DWORD)s->retry_n : 30000u, NULL);
        tray_update();
        redraw_all();
        return;
    }
    if (tun && g.prefs.hold) {
        s->phase = PH_HELD;
        g.held = 1;
    } else {
        if (tun) {
            svc_call("hold 0", resp, (int)sizeof resp);
            svc_call("tun-close", resp, (int)sizeof resp);
            s->owns_tun = 0;
        }
        s->phase = PH_ERR;
    }
    tray_update();
    redraw_all();
}

void session_tun_ready(int slot, int ok, char *resp) {
    Sess *s;
    if (slot < 0 || slot >= LOFT_SESS) return;
    s = &g.sess[slot];
    s->tun_wait = 0;
    if (!s->used || s->user_stop) {
        if (ok && !g.prefs.hold) svc_call("tun-close", resp, resp ? 128 : 0);
        return;
    }
    if (!ok) {
        s->phase = PH_ERR;
        s->owns_tun = 0;
        if (resp && strncmp(resp, "err pipe", 8) == 0) g.need_svc = 1;
        if (resp && resp[0]) journal_add(resp);
        tray_update();
        redraw_all();
        return;
    }
    g.need_svc = 0;
    spawn_front(s, 1);
}

void session_retry(int slot) {
    Sess *s;
    if (slot < 0 || slot >= LOFT_SESS) return;
    s = &g.sess[slot];
    if (!s->used || s->user_stop || s->proc) return;
    if (s->owns_tun) {
        s->tun_wait = 1;
        CloseHandle(CreateThread(NULL, 0, tun_worker, (LPVOID)(intptr_t)slot, 0, NULL));
    } else {
        spawn_front(s, 0);
    }
}

void session_extip(int slot, const char *ip) {
    if (slot < 0 || slot >= LOFT_SESS || !g.sess[slot].used) return;
    g.sess[slot].ext_busy = 0;
    if (ip && ip[0]) loft_strlcpy(g.sess[slot].ext_ip, sizeof g.sess[slot].ext_ip, ip);
    redraw_all();
}

int session_phase(void) {
    int i, on = 0, conn = 0, err = 0, held = 0;
    for (i = 0; i < LOFT_SESS; i++) {
        if (!g.sess[i].used) continue;
        if (g.sess[i].phase == PH_ON) on = 1;
        else if (g.sess[i].phase == PH_CONNECTING) conn = 1;
        else if (g.sess[i].phase == PH_ERR) err = 1;
        else if (g.sess[i].phase == PH_HELD) held = 1;
    }
    if (on) return PH_ON;
    if (conn) return PH_CONNECTING;
    if (err) return PH_ERR;
    if (held || g.held) return PH_HELD;
    return PH_OFF;
}

Sess *session_primary(void) {
    int i, conn = -1, any = -1;
    for (i = 0; i < LOFT_SESS; i++) {
        if (!g.sess[i].used) continue;
        if (any < 0) any = i;
        if (g.sess[i].owns_tun) return &g.sess[i];
        if (g.sess[i].phase == PH_ON || g.sess[i].phase == PH_CONNECTING) conn = i;
    }
    if (conn >= 0) return &g.sess[conn];
    if (any >= 0) return &g.sess[any];
    return NULL;
}
