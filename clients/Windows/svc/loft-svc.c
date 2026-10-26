/* loft-svc: Wintun adapter, IP Helper address and routes, packet pipe for loft-front.
   Windows 7 and later. The GUI is asInvoker; this process is LocalSystem.
   WintunDeleteDriver runs only on --uninstall-driver. */
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601
#endif
#ifndef WINVER
#define WINVER 0x0601
#endif
#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <shellapi.h>
#include <iphlpapi.h>
#include <sddl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#define LOFT_RTS 280
#define LOFT_PIPE_CTL L"\\\\.\\pipe\\loft-ctl"
#define LOFT_PIPE_TUN L"\\\\.\\pipe\\loft-tun"

typedef void *WAdapter;
typedef void *WSession;
typedef WAdapter(WINAPI *FnCreate)(const WCHAR *, const WCHAR *, const GUID *);
typedef void(WINAPI *FnCloseAd)(WAdapter);
typedef BOOL(WINAPI *FnDelDrv)(void);
typedef void(WINAPI *FnLuid)(WAdapter, NET_LUID *);
typedef WSession(WINAPI *FnStart)(WAdapter, DWORD);
typedef void(WINAPI *FnEnd)(WSession);
typedef HANDLE(WINAPI *FnEvent)(WSession);
typedef BYTE *(WINAPI *FnRecv)(WSession, DWORD *);
typedef void(WINAPI *FnRel)(WSession, const BYTE *);
typedef BYTE *(WINAPI *FnAlloc)(WSession, DWORD);
typedef void(WINAPI *FnSend)(WSession, const BYTE *);

static struct {
    HMODULE dll;
    FnCreate create;
    FnCloseAd close_ad;
    FnDelDrv del_drv;
    FnLuid luid;
    FnStart start;
    FnEnd end;
    FnEvent event;
    FnRecv recv;
    FnRel rel;
    FnAlloc alloc;
    FnSend send;
} W;

static struct {
    CRITICAL_SECTION cs;
    int ready;
    int up;
    int hold;
    int have_gw;
    int dns_saved;
    int nrts;
    volatile LONG run;
    NET_LUID luid;
    NET_IFINDEX idx;
    WAdapter ad;
    WSession ses;
    HANDLE rx_th, tx_th, client;
    HANDLE pipe_ready;
    MIB_IPFORWARD_ROW2 gw;
    MIB_IPFORWARD_ROW2 rts[LOFT_RTS];
    WCHAR dns_key[300];
    WCHAR dns_old[512];
} T;

static SERVICE_STATUS_HANDLE SvcStatus;
static SERVICE_STATUS Svc;
static HANDLE SvcStop;
static HANDLE CtrlThread;
static volatile LONG SvcRun;

static void slog(const char *s) {
    WCHAR dir[MAX_PATH], path[MAX_PATH];
    char line[640];
    SYSTEMTIME st;
    HANDLE h;
    DWORD w, sz;
    if (!s) return;
    GetLocalTime(&st);
    snprintf(line, sizeof line, "%04u-%02u-%02u %02u:%02u:%02u %s\r\n",
             st.wYear, st.wMonth, st.wDay, st.wHour, st.wMinute, st.wSecond, s);
    if (!ExpandEnvironmentStringsW(L"%ProgramData%\\loft", dir, MAX_PATH)) return;
    CreateDirectoryW(dir, NULL);
    if (wcslen(dir) + 12 >= MAX_PATH) return;
    wcscpy(path, dir);
    wcscat(path, L"\\svc.log");
    h = CreateFileW(path, FILE_APPEND_DATA, FILE_SHARE_READ | FILE_SHARE_WRITE, NULL, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return;
    sz = GetFileSize(h, NULL);
    if (sz != INVALID_FILE_SIZE && sz < 1024u * 1024u) WriteFile(h, line, (DWORD)strlen(line), &w, NULL);
    CloseHandle(h);
}

static void reply(char *resp, int n, const char *s) {
    if (!resp || n <= 0) return;
    snprintf(resp, (size_t)n, "%s", s ? s : "err");
}

static void reply_err(char *resp, int n, const char *what) {
    char b[192];
    snprintf(b, sizeof b, "err %s (%lu)", what ? what : "fail", GetLastError());
    slog(b);
    reply(resp, n, b);
}

static int is_admin(void) {
    BOOL yes = FALSE;
    SID_IDENTIFIER_AUTHORITY nt = SECURITY_NT_AUTHORITY;
    PSID sid = NULL;
    if (AllocateAndInitializeSid(&nt, 2, SECURITY_BUILTIN_DOMAIN_RID, DOMAIN_ALIAS_RID_ADMINS, 0, 0, 0, 0, 0, 0, &sid)) {
        CheckTokenMembership(NULL, sid, &yes);
        FreeSid(sid);
    }
    return yes ? 1 : 0;
}

static int pipe_sd(SECURITY_ATTRIBUTES *sa, PSECURITY_DESCRIPTOR *sd) {
    memset(sa, 0, sizeof *sa);
    *sd = NULL;
    if (!ConvertStringSecurityDescriptorToSecurityDescriptorW(
            L"D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;IU)", SDDL_REVISION_1, sd, NULL))
        return 0;
    sa->nLength = sizeof *sa;
    sa->lpSecurityDescriptor = *sd;
    sa->bInheritHandle = FALSE;
    return 1;
}

static void loft_guid(GUID *g) {
    static const unsigned char b[16] = {0x6c, 0x6f, 0x66, 0x74, 0x2d, 0x77, 0x69, 0x6e,
                                         0x74, 0x75, 0x6e, 0x2d, 0x63, 0x68, 0x6b, 0x01};
    memcpy(g, b, 16);
}

static int load_wintun(void) {
    WCHAR path[MAX_PATH], *slash;
    FARPROC p;
    if (W.dll) return 1;
    if (!GetModuleFileNameW(NULL, path, MAX_PATH)) return 0;
    slash = wcsrchr(path, L'\\');
    if (!slash) return 0;
    if ((size_t)(slash - path) + 12 >= MAX_PATH) return 0;
    wcscpy(slash + 1, L"wintun.dll");
    W.dll = LoadLibraryW(path);
    if (!W.dll) return 0;
#define BIND(field, name, type)                          \
    p = GetProcAddress(W.dll, name);                     \
    if (!p) return 0;                                    \
    W.field = (type)(void *)p
    BIND(create, "WintunCreateAdapter", FnCreate);
    BIND(close_ad, "WintunCloseAdapter", FnCloseAd);
    BIND(del_drv, "WintunDeleteDriver", FnDelDrv);
    BIND(luid, "WintunGetAdapterLUID", FnLuid);
    BIND(start, "WintunStartSession", FnStart);
    BIND(end, "WintunEndSession", FnEnd);
    BIND(event, "WintunGetReadWaitEvent", FnEvent);
    BIND(recv, "WintunReceivePacket", FnRecv);
    BIND(rel, "WintunReleaseReceivePacket", FnRel);
    BIND(alloc, "WintunAllocateSendPacket", FnAlloc);
    BIND(send, "WintunSendPacket", FnSend);
#undef BIND
    return 1;
}

static int write_full(HANDLE h, const void *b, int n) {
    const char *p = b;
    int got = 0;
    while (got < n) {
        DWORD w = 0;
        if (!WriteFile(h, p + got, (DWORD)(n - got), &w, NULL) || w == 0) return 0;
        got += (int)w;
    }
    return 1;
}

static int read_full(HANDLE h, void *b, int n) {
    char *p = b;
    int got = 0;
    while (got < n) {
        DWORD r = 0;
        if (!ReadFile(h, p + got, (DWORD)(n - got), &r, NULL) || r == 0) return 0;
        got += (int)r;
    }
    return 1;
}

static int write_client(const BYTE *b, int n) {
    HANDLE h = NULL, dup = NULL;
    int ok;
    EnterCriticalSection(&T.cs);
    h = T.client;
    if (h) DuplicateHandle(GetCurrentProcess(), h, GetCurrentProcess(), &dup, 0, FALSE, DUPLICATE_SAME_ACCESS);
    LeaveCriticalSection(&T.cs);
    if (!dup) return 0;
    ok = write_full(dup, b, n);
    CloseHandle(dup);
    return ok;
}

static DWORD WINAPI rx_thread(LPVOID unused) {
    BYTE *buf;
    HANDLE ev;
    WSession ses;
    (void)unused;
    buf = (BYTE *)malloc(65535u + 4u);
    ses = T.ses;
    if (!buf || !ses) {
        free(buf);
        return 0;
    }
    ev = W.event(ses);
    while (T.run) {
        DWORD sz = 0;
        BYTE *pkt = W.recv(ses, &sz);
        if (!pkt) {
            if (GetLastError() == ERROR_NO_MORE_ITEMS) {
                if (!T.run) break;
                if (ev) WaitForSingleObject(ev, 500);
                continue;
            }
            break;
        }
        if (T.run && sz >= 20 && sz <= 65535) {
            buf[0] = (BYTE)sz;
            buf[1] = (BYTE)(sz >> 8);
            buf[2] = (BYTE)(sz >> 16);
            buf[3] = (BYTE)(sz >> 24);
            memcpy(buf + 4, pkt, sz);
            W.rel(ses, pkt);
            write_client(buf, (int)sz + 4);
        } else {
            W.rel(ses, pkt);
        }
    }
    free(buf);
    return 0;
}

static void tx_packets(HANDLE pipe) {
    BYTE *buf = (BYTE *)malloc(65535u);
    if (!buf) return;
    while (T.run) {
        BYTE hdr[4];
        unsigned n;
        BYTE *dst;
        if (!read_full(pipe, hdr, 4)) break;
        n = (unsigned)hdr[0] | ((unsigned)hdr[1] << 8) | ((unsigned)hdr[2] << 16) | ((unsigned)hdr[3] << 24);
        if (n < 20 || n > 65535) break;
        if (!read_full(pipe, buf, (int)n)) break;
        if (!T.ses) break;
        dst = W.alloc(T.ses, n);
        if (!dst) continue;
        memcpy(dst, buf, n);
        W.send(T.ses, dst);
    }
    free(buf);
}

static DWORD WINAPI tx_thread(LPVOID unused) {
    SECURITY_ATTRIBUTES sa;
    PSECURITY_DESCRIPTOR sd = NULL;
    (void)unused;
    if (!pipe_sd(&sa, &sd)) {
        if (T.pipe_ready) SetEvent(T.pipe_ready);
        return 0;
    }
    while (T.run) {
        HANDLE p = CreateNamedPipeW(LOFT_PIPE_TUN, PIPE_ACCESS_DUPLEX, PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS, 1, 1 << 20, 1 << 20, 0, &sa);
        if (p == INVALID_HANDLE_VALUE) {
            if (T.pipe_ready) SetEvent(T.pipe_ready);
            Sleep(200);
            continue;
        }
        if (T.pipe_ready) SetEvent(T.pipe_ready);
        if (!ConnectNamedPipe(p, NULL) && GetLastError() != ERROR_PIPE_CONNECTED) {
            CloseHandle(p);
            if (!T.run) break;
            continue;
        }
        if (!T.run) {
            DisconnectNamedPipe(p);
            CloseHandle(p);
            break;
        }
        EnterCriticalSection(&T.cs);
        T.client = p;
        LeaveCriticalSection(&T.cs);
        tx_packets(p);
        EnterCriticalSection(&T.cs);
        if (T.client == p) T.client = NULL;
        LeaveCriticalSection(&T.cs);
        DisconnectNamedPipe(p);
        CloseHandle(p);
    }
    LocalFree(sd);
    return 0;
}

static void remember_gw(void) {
    PMIB_IPFORWARD_TABLE2 tab = NULL;
    ULONG i;
    if (T.have_gw) return;
    if (GetIpForwardTable2(AF_INET, &tab) != NO_ERROR || !tab) return;
    for (i = 0; i < tab->NumEntries; i++) {
        MIB_IPFORWARD_ROW2 *r = &tab->Table[i];
        if (r->DestinationPrefix.PrefixLength != 0) continue;
        if (r->DestinationPrefix.Prefix.si_family != AF_INET) continue;
        if (r->InterfaceLuid.Value == T.luid.Value) continue;
        if (!T.have_gw || r->Metric < T.gw.Metric) {
            T.gw = *r;
            T.have_gw = 1;
        }
    }
    FreeMibTable(tab);
}

static void wipe_addrs(void) {
    PMIB_UNICASTIPADDRESS_TABLE tab = NULL;
    ULONG i;
    if (GetUnicastIpAddressTable(AF_INET, &tab) != NO_ERROR || !tab) return;
    for (i = 0; i < tab->NumEntries; i++) {
        if (tab->Table[i].InterfaceLuid.Value == T.luid.Value) DeleteUnicastIpAddressEntry(&tab->Table[i]);
    }
    FreeMibTable(tab);
}

static int set_addr(const char *ip, int prefix) {
    MIB_UNICASTIPADDRESS_ROW row;
    IN_ADDR a;
    int i;
    if (prefix < 1 || prefix > 32) prefix = 32;
    if (inet_pton(AF_INET, ip, &a) != 1) return 0;
    for (i = 0; i < 15; i++) {
        if (ConvertInterfaceLuidToIndex(&T.luid, &T.idx) == NO_ERROR) break;
        Sleep(200);
    }
    wipe_addrs();
    InitializeUnicastIpAddressEntry(&row);
    row.Address.Ipv4.sin_family = AF_INET;
    row.Address.si_family = AF_INET;
    row.Address.Ipv4.sin_addr = a;
    row.InterfaceLuid = T.luid;
    row.InterfaceIndex = T.idx;
    row.OnLinkPrefixLength = (UINT8)prefix;
    row.PrefixOrigin = IpPrefixOriginManual;
    row.SuffixOrigin = IpSuffixOriginManual;
    row.ValidLifetime = 0xFFFFFFFF;
    row.PreferredLifetime = 0xFFFFFFFF;
    row.DadState = IpDadStatePreferred;
    row.SkipAsSource = FALSE;
    for (i = 0; i < 15; i++) {
        DWORD e = CreateUnicastIpAddressEntry(&row);
        if (e == NO_ERROR || e == ERROR_OBJECT_ALREADY_EXISTS) break;
        Sleep(200);
        if (i == 14) {
            SetLastError(e);
            return 0;
        }
    }
    {
        MIB_IPINTERFACE_ROW ir;
        InitializeIpInterfaceEntry(&ir);
        ir.InterfaceLuid = T.luid;
        ir.Family = AF_INET;
        if (GetIpInterfaceEntry(&ir) == NO_ERROR) {
            ir.NlMtu = 1280;
            ir.SitePrefixLength = 0;
            SetIpInterfaceEntry(&ir);
        }
    }
    return 1;
}

static int track(const MIB_IPFORWARD_ROW2 *row) {
    if (T.nrts >= LOFT_RTS) return 0;
    T.rts[T.nrts++] = *row;
    return 1;
}

static void drop_tracked(int only_default) {
    int i, w = 0;
    for (i = 0; i < T.nrts; i++) {
        int isdef = T.rts[i].DestinationPrefix.PrefixLength == 0;
        if (only_default ? isdef : !isdef) {
            DeleteIpForwardEntry2(&T.rts[i]);
        } else {
            T.rts[w++] = T.rts[i];
        }
    }
    T.nrts = w;
}

static void drop_all_routes(void) {
    int i;
    for (i = 0; i < T.nrts; i++) DeleteIpForwardEntry2(&T.rts[i]);
    T.nrts = 0;
}

static int add_route(int via_tun, const IN_ADDR *dest, int prefix) {
    MIB_IPFORWARD_ROW2 row;
    DWORD e;
    remember_gw();
    if (!via_tun && !T.have_gw) return 0;
    InitializeIpForwardEntry(&row);
    row.InterfaceLuid = via_tun ? T.luid : T.gw.InterfaceLuid;
    row.InterfaceIndex = 0;
    if (!via_tun) row.InterfaceIndex = T.gw.InterfaceIndex;
    else ConvertInterfaceLuidToIndex(&T.luid, &row.InterfaceIndex);
    row.DestinationPrefix.Prefix.si_family = AF_INET;
    row.DestinationPrefix.Prefix.Ipv4.sin_family = AF_INET;
    row.DestinationPrefix.Prefix.Ipv4.sin_addr = *dest;
    row.DestinationPrefix.PrefixLength = (UINT8)prefix;
    row.NextHop.si_family = AF_INET;
    row.NextHop.Ipv4.sin_family = AF_INET;
    if (via_tun) row.NextHop.Ipv4.sin_addr.s_addr = 0;
    else row.NextHop = T.gw.NextHop;
    row.Metric = via_tun ? 5 : (T.gw.Metric ? T.gw.Metric : 1);
    row.Protocol = MIB_IPPROTO_NETMGMT;
    row.ValidLifetime = 0xFFFFFFFF;
    row.PreferredLifetime = 0xFFFFFFFF;
    e = CreateIpForwardEntry2(&row);
    if (e != NO_ERROR && e != ERROR_OBJECT_ALREADY_EXISTS) {
        SetLastError(e);
        return 0;
    }
    track(&row);
    return 1;
}

static int parse_cidr(const char *s, IN_ADDR *a, int *pre) {
    char tmp[80];
    char *sl;
    size_t n;
    if (!s || !s[0]) return 0;
    n = strlen(s);
    if (n >= sizeof tmp) return 0;
    memcpy(tmp, s, n + 1);
    sl = strchr(tmp, '/');
    if (sl) {
        *pre = atoi(sl + 1);
        *sl = 0;
    } else {
        *pre = 32;
    }
    if (*pre < 0 || *pre > 32) return 0;
    return inet_pton(AF_INET, tmp, a) == 1;
}

static void dns_flush(void) {
    HMODULE dns = LoadLibraryW(L"dnsapi.dll");
    HMODULE dhcp = LoadLibraryW(L"dhcpcsvc.dll");
    if (dns) {
        BOOL(WINAPI * fn)(void) = (BOOL(WINAPI *)(void))(void *)GetProcAddress(dns, "DnsFlushResolverCache");
        if (fn) fn();
        FreeLibrary(dns);
    }
    if (dhcp) {
        DWORD(WINAPI * fn)(LPCWSTR, LPCWSTR, BOOL, DWORD, DWORD, DWORD, int) =
            (DWORD(WINAPI *)(LPCWSTR, LPCWSTR, BOOL, DWORD, DWORD, DWORD, int))(void *)GetProcAddress(dhcp, "DhcpNotifyConfigChange");
        if (fn) fn(NULL, NULL, FALSE, 0, 0, 0, 0);
        FreeLibrary(dhcp);
    }
}

static void guid_key(const GUID *g, WCHAR *dst, int n) {
    _snwprintf(dst, (size_t)n,
               L"SYSTEM\\CurrentControlSet\\Services\\Tcpip\\Parameters\\Interfaces\\{%08lX-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X}",
               g->Data1, (unsigned)g->Data2, (unsigned)g->Data3, (unsigned)g->Data4[0], (unsigned)g->Data4[1],
               (unsigned)g->Data4[2], (unsigned)g->Data4[3], (unsigned)g->Data4[4], (unsigned)g->Data4[5],
               (unsigned)g->Data4[6], (unsigned)g->Data4[7]);
    dst[n - 1] = 0;
}

static void dns_set(const char *ips) {
    GUID g;
    WCHAR sub[300], wips[512];
    HKEY k = NULL;
    int i;
    if (!ips || !ips[0]) return;
    if (ConvertInterfaceLuidToGuid(&T.luid, &g) != NO_ERROR) return;
    guid_key(&g, sub, 300);
    if (RegCreateKeyExW(HKEY_LOCAL_MACHINE, sub, 0, NULL, 0, KEY_QUERY_VALUE | KEY_SET_VALUE, NULL, &k, NULL) != ERROR_SUCCESS) return;
    if (!T.dns_saved) {
        DWORD typ = 0, sz = sizeof T.dns_old - sizeof(WCHAR);
        if (RegQueryValueExW(k, L"NameServer", NULL, &typ, (BYTE *)T.dns_old, &sz) != ERROR_SUCCESS) T.dns_old[0] = 0;
        wcscpy(T.dns_key, sub);
        T.dns_saved = 1;
    }
    i = MultiByteToWideChar(CP_UTF8, 0, ips, -1, wips, 512);
    if (i <= 0) {
        RegCloseKey(k);
        return;
    }
    RegSetValueExW(k, L"NameServer", 0, REG_SZ, (BYTE *)wips, (DWORD)((wcslen(wips) + 1) * sizeof(WCHAR)));
    RegCloseKey(k);
    dns_flush();
}

static void dns_restore(void) {
    HKEY k = NULL;
    if (!T.dns_saved) return;
    if (RegOpenKeyExW(HKEY_LOCAL_MACHINE, T.dns_key, 0, KEY_SET_VALUE, &k) == ERROR_SUCCESS) {
        RegSetValueExW(k, L"NameServer", 0, REG_SZ, (BYTE *)T.dns_old, (DWORD)((wcslen(T.dns_old) + 1) * sizeof(WCHAR)));
        RegCloseKey(k);
    }
    T.dns_saved = 0;
    dns_flush();
}

static void stop_threads(void) {
    HANDLE c = NULL;
    WSession ses = T.ses;
    T.run = 0;
    if (ses && W.end) W.end(ses);
    if (T.rx_th) CancelSynchronousIo(T.rx_th);
    if (T.tx_th) CancelSynchronousIo(T.tx_th);
    EnterCriticalSection(&T.cs);
    c = T.client;
    T.client = NULL;
    LeaveCriticalSection(&T.cs);
    if (c) {
        DisconnectNamedPipe(c);
        CloseHandle(c);
    }
    if (T.rx_th) {
        WaitForSingleObject(T.rx_th, 5000);
        CloseHandle(T.rx_th);
        T.rx_th = NULL;
    }
    if (T.tx_th) {
        WaitForSingleObject(T.tx_th, 5000);
        CloseHandle(T.tx_th);
        T.tx_th = NULL;
    }
    T.ses = NULL;
}

static void close_adapter(void) {
    if (T.ad && W.close_ad) W.close_ad(T.ad);
    T.ad = NULL;
    T.up = 0;
    T.idx = 0;
    memset(&T.luid, 0, sizeof T.luid);
}

static void tun_shutdown(int restore_dns) {
    stop_threads();
    drop_all_routes();
    if (T.up) wipe_addrs();
    if (restore_dns) dns_restore();
    close_adapter();
    T.have_gw = 0;
    T.hold = 0;
}

static int tun_open(char *resp, int n) {
    GUID g;
    WAdapter ad;
    WSession ses;
    int i;
    if (T.up && T.ses) {
        snprintf(resp, (size_t)n, "ok idx=%lu", (unsigned long)T.idx);
        return 1;
    }
    if (!load_wintun()) {
        reply_err(resp, n, "wintun.dll");
        return 0;
    }
    loft_guid(&g);
    SetLastError(0);
    ad = W.create(L"Loft", L"Loft", &g);
    if (!ad) {
        reply_err(resp, n, "adapter");
        return 0;
    }
    W.luid(ad, &T.luid);
    ses = W.start(ad, 0x400000);
    if (!ses) {
        W.close_ad(ad);
        ad = W.create(L"Loft", L"Loft", &g);
        if (!ad) {
            reply_err(resp, n, "adapter");
            return 0;
        }
        W.luid(ad, &T.luid);
        ses = W.start(ad, 0x400000);
        if (!ses) {
            W.close_ad(ad);
            reply_err(resp, n, "session");
            return 0;
        }
    }
    T.ad = ad;
    T.ses = ses;
    T.up = 1;
    T.run = 1;
    if (!T.pipe_ready) T.pipe_ready = CreateEventW(NULL, TRUE, FALSE, NULL);
    ResetEvent(T.pipe_ready);
    T.rx_th = CreateThread(NULL, 0, rx_thread, NULL, 0, NULL);
    T.tx_th = CreateThread(NULL, 0, tx_thread, NULL, 0, NULL);
    if (!T.rx_th || !T.tx_th) {
        tun_shutdown(0);
        reply(resp, n, "err thread");
        return 0;
    }
    WaitForSingleObject(T.pipe_ready, 3000);
    for (i = 0; i < 10; i++) {
        if (ConvertInterfaceLuidToIndex(&T.luid, &T.idx) == NO_ERROR) break;
        Sleep(100);
    }
    slog("tun-open");
    snprintf(resp, (size_t)n, "ok idx=%lu", (unsigned long)T.idx);
    return 1;
}

static void on_cmd(char *line, char *resp, int n) {
    char *arg;
    while (*line == ' ' || *line == '\t' || *line == '\r' || *line == '\n') line++;
    arg = line + strlen(line);
    while (arg > line && (arg[-1] == '\r' || arg[-1] == '\n' || arg[-1] == ' ')) {
        arg--;
        *arg = 0;
    }
    arg = strchr(line, ' ');
    if (arg) {
        *arg++ = 0;
        while (*arg == ' ') arg++;
    } else {
        arg = line + strlen(line);
    }
    if (strcmp(line, "ping") == 0) {
        reply(resp, n, "ok");
        return;
    }
    if (strcmp(line, "status") == 0) {
        snprintf(resp, (size_t)n, "status tun=%d hold=%d idx=%lu", T.up, T.hold, (unsigned long)T.idx);
        return;
    }
    if (strcmp(line, "tun-open") == 0) {
        tun_open(resp, n);
        return;
    }
    if (strcmp(line, "tun-close") == 0) {
        if (T.hold) {
            reply(resp, n, "ok held");
            return;
        }
        tun_shutdown(1);
        slog("tun-close");
        reply(resp, n, "ok");
        return;
    }
    if (strcmp(line, "reset") == 0) {
        tun_shutdown(1);
        slog("reset");
        reply(resp, n, "ok");
        return;
    }
    if (strcmp(line, "hold") == 0) {
        T.hold = (arg[0] == '1');
        reply(resp, n, "ok");
        return;
    }
    if (!T.up) {
        reply(resp, n, "err tun down");
        return;
    }
    if (strcmp(line, "addr") == 0) {
        char ip[64];
        int pre = 32;
        char *sp;
        snprintf(ip, sizeof ip, "%s", arg);
        sp = strchr(ip, ' ');
        if (!sp) sp = strchr(ip, '/');
        if (sp) {
            pre = atoi(sp + 1);
            *sp = 0;
        }
        if (!set_addr(ip, pre)) reply_err(resp, n, "addr");
        else reply(resp, n, "ok");
        return;
    }
    if (strcmp(line, "dns") == 0) {
        dns_set(arg);
        reply(resp, n, "ok");
        return;
    }
    if (strcmp(line, "exclude") == 0 || strcmp(line, "route") == 0 || strcmp(line, "direct") == 0) {
        IN_ADDR a;
        int pre = 32;
        int via = strcmp(line, "direct") != 0 && strcmp(line, "exclude") != 0;
        if (strcmp(line, "exclude") == 0) via = 0;
        if (!parse_cidr(arg, &a, &pre)) {
            reply(resp, n, "err cidr");
            return;
        }
        if (strcmp(line, "exclude") == 0) pre = 32;
        if (!add_route(via, &a, pre)) reply_err(resp, n, T.have_gw || via ? "route" : "no gateway");
        else reply(resp, n, "ok");
        return;
    }
    if (strcmp(line, "route-default") == 0) {
        IN_ADDR z;
        memset(&z, 0, sizeof z);
        if (strcmp(arg, "on") == 0) {
            remember_gw();
            if (!add_route(1, &z, 0)) reply_err(resp, n, "default");
            else reply(resp, n, "ok");
        } else {
            drop_tracked(1);
            reply(resp, n, "ok");
        }
        return;
    }
    if (strcmp(line, "routes-clear") == 0) {
        drop_tracked(0);
        reply(resp, n, "ok");
        return;
    }
    reply(resp, n, "err unknown");
}

static DWORD WINAPI ctrl_thread(LPVOID unused) {
    SECURITY_ATTRIBUTES sa;
    PSECURITY_DESCRIPTOR sd = NULL;
    (void)unused;
    if (!pipe_sd(&sa, &sd)) return 0;
    while (SvcRun) {
        HANDLE p = CreateNamedPipeW(LOFT_PIPE_CTL, PIPE_ACCESS_DUPLEX, PIPE_TYPE_MESSAGE | PIPE_READMODE_MESSAGE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS, PIPE_UNLIMITED_INSTANCES, 65536, 65536, 0, &sa);
        char req[512], resp[256];
        DWORD got = 0;
        if (p == INVALID_HANDLE_VALUE) {
            Sleep(200);
            continue;
        }
        if (!ConnectNamedPipe(p, NULL) && GetLastError() != ERROR_PIPE_CONNECTED) {
            CloseHandle(p);
            continue;
        }
        if (!SvcRun) {
            CloseHandle(p);
            break;
        }
        memset(req, 0, sizeof req);
        if (ReadFile(p, req, sizeof req - 1, &got, NULL) && got > 0) {
            req[got] = 0;
            resp[0] = 0;
            on_cmd(req, resp, (int)sizeof resp);
            WriteFile(p, resp, (DWORD)strlen(resp), &got, NULL);
            FlushFileBuffers(p);
        }
        DisconnectNamedPipe(p);
        CloseHandle(p);
    }
    LocalFree(sd);
    return 0;
}

static void report(DWORD state) {
    Svc.dwServiceType = SERVICE_WIN32_OWN_PROCESS;
    Svc.dwCurrentState = state;
    Svc.dwControlsAccepted = (state == SERVICE_RUNNING) ? (SERVICE_ACCEPT_STOP | SERVICE_ACCEPT_SHUTDOWN) : 0;
    Svc.dwWin32ExitCode = NO_ERROR;
    Svc.dwCheckPoint = 0;
    Svc.dwWaitHint = (state == SERVICE_START_PENDING || state == SERVICE_STOP_PENDING) ? 4000 : 0;
    SetServiceStatus(SvcStatus, &Svc);
}

static DWORD WINAPI handler(DWORD ctrl, DWORD type, LPVOID data, LPVOID ctx) {
    (void)type;
    (void)data;
    (void)ctx;
    if (ctrl == SERVICE_CONTROL_STOP || ctrl == SERVICE_CONTROL_SHUTDOWN) {
        report(SERVICE_STOP_PENDING);
        SvcRun = 0;
        if (CtrlThread) CancelSynchronousIo(CtrlThread);
        SetEvent(SvcStop);
        return NO_ERROR;
    }
    if (ctrl == SERVICE_CONTROL_INTERROGATE) return NO_ERROR;
    return ERROR_CALL_NOT_IMPLEMENTED;
}

static void WINAPI ServiceMain(DWORD argc, LPWSTR *argv) {
    (void)argc;
    (void)argv;
    SvcStatus = RegisterServiceCtrlHandlerExW(L"loft", handler, NULL);
    if (!SvcStatus) return;
    report(SERVICE_START_PENDING);
    if (!T.ready) {
        InitializeCriticalSection(&T.cs);
        T.ready = 1;
    }
    {
        WSADATA wsa;
        WSAStartup(MAKEWORD(2, 2), &wsa);
    }
    SvcStop = CreateEventW(NULL, TRUE, FALSE, NULL);
    SvcRun = 1;
    CtrlThread = CreateThread(NULL, 0, ctrl_thread, NULL, 0, NULL);
    report(SERVICE_RUNNING);
    slog("service up");
    WaitForSingleObject(SvcStop, INFINITE);
    SvcRun = 0;
    if (CtrlThread) {
        CancelSynchronousIo(CtrlThread);
        WaitForSingleObject(CtrlThread, 5000);
        CloseHandle(CtrlThread);
        CtrlThread = NULL;
    }
    tun_shutdown(1);
    if (SvcStop) CloseHandle(SvcStop);
    slog("service down");
    report(SERVICE_STOPPED);
}

static int elevate(const WCHAR *params) {
    WCHAR path[MAX_PATH];
    SHELLEXECUTEINFOW sei;
    GetModuleFileNameW(NULL, path, MAX_PATH);
    memset(&sei, 0, sizeof sei);
    sei.cbSize = sizeof sei;
    sei.fMask = SEE_MASK_NOCLOSEPROCESS;
    sei.lpVerb = L"runas";
    sei.lpFile = path;
    sei.lpParameters = params;
    sei.nShow = SW_SHOWNORMAL;
    if (!ShellExecuteExW(&sei)) return 1;
    if (sei.hProcess) {
        DWORD c = 1;
        WaitForSingleObject(sei.hProcess, 120000);
        GetExitCodeProcess(sei.hProcess, &c);
        CloseHandle(sei.hProcess);
        return (int)c;
    }
    return 0;
}

static int do_install(void) {
    SC_HANDLE scm, svc;
    WCHAR path[MAX_PATH], bin[MAX_PATH + 8];
    SERVICE_DESCRIPTIONW desc;
    if (!is_admin()) return elevate(L"--install");
    if (!GetModuleFileNameW(NULL, path, MAX_PATH)) return 1;
    _snwprintf(bin, MAX_PATH + 8, L"\"%s\"", path);
    bin[MAX_PATH + 7] = 0;
    scm = OpenSCManagerW(NULL, NULL, SC_MANAGER_ALL_ACCESS);
    if (!scm) {
        MessageBoxW(NULL, L"OpenSCManager failed", L"Loft", MB_ICONERROR);
        return 1;
    }
    svc = CreateServiceW(scm, L"loft", L"Loft", SERVICE_ALL_ACCESS, SERVICE_WIN32_OWN_PROCESS, SERVICE_AUTO_START, SERVICE_ERROR_NORMAL, bin, NULL, NULL, L"Tcpip\0", NULL, NULL);
    if (!svc && GetLastError() == ERROR_SERVICE_EXISTS) {
        svc = OpenServiceW(scm, L"loft", SERVICE_ALL_ACCESS);
        if (svc) ChangeServiceConfigW(svc, SERVICE_WIN32_OWN_PROCESS, SERVICE_AUTO_START, SERVICE_ERROR_NORMAL, bin, NULL, NULL, L"Tcpip\0", NULL, NULL, L"Loft");
    }
    if (!svc) {
        MessageBoxW(NULL, L"CreateService failed", L"Loft", MB_ICONERROR);
        CloseServiceHandle(scm);
        return 1;
    }
    desc.lpDescription = L"Loft tunnel: Wintun adapter, routes, and the packet pipe for loft-front.";
    ChangeServiceConfig2W(svc, SERVICE_CONFIG_DESCRIPTION, &desc);
    if (!StartServiceW(svc, 0, NULL) && GetLastError() != ERROR_SERVICE_ALREADY_RUNNING) {
        MessageBoxW(NULL, L"StartService failed", L"Loft", MB_ICONERROR);
        CloseServiceHandle(svc);
        CloseServiceHandle(scm);
        return 1;
    }
    CloseServiceHandle(svc);
    CloseServiceHandle(scm);
    return 0;
}

static int do_uninstall(int del_driver) {
    SC_HANDLE scm, svc;
    SERVICE_STATUS st;
    if (!is_admin()) return elevate(del_driver ? L"--uninstall-driver" : L"--uninstall");
    scm = OpenSCManagerW(NULL, NULL, SC_MANAGER_ALL_ACCESS);
    if (!scm) return 1;
    svc = OpenServiceW(scm, L"loft", SERVICE_ALL_ACCESS);
    if (svc) {
        ControlService(svc, SERVICE_CONTROL_STOP, &st);
        Sleep(400);
        DeleteService(svc);
        CloseServiceHandle(svc);
    }
    CloseServiceHandle(scm);
    if (del_driver && load_wintun() && W.del_drv) W.del_drv();
    return 0;
}

int WINAPI wWinMain(HINSTANCE inst, HINSTANCE prev, LPWSTR cmd, int show) {
    int argc = 0, i, uninstall = 0, driver = 0, install = 0;
    LPWSTR *argv;
    SERVICE_TABLE_ENTRYW table[2];
    (void)inst;
    (void)prev;
    (void)cmd;
    (void)show;
    argv = CommandLineToArgvW(GetCommandLineW(), &argc);
    for (i = 1; argv && i < argc; i++) {
        if (wcscmp(argv[i], L"--install") == 0) install = 1;
        if (wcscmp(argv[i], L"--uninstall") == 0) uninstall = 1;
        if (wcscmp(argv[i], L"--uninstall-driver") == 0) {
            uninstall = 1;
            driver = 1;
        }
    }
    if (argv) LocalFree(argv);
    if (install) return do_install();
    if (uninstall) return do_uninstall(driver);
    table[0].lpServiceName = L"loft";
    table[0].lpServiceProc = ServiceMain;
    table[1].lpServiceName = NULL;
    table[1].lpServiceProc = NULL;
    if (!StartServiceCtrlDispatcherW(table)) {
        if (GetLastError() == ERROR_FAILED_SERVICE_CONTROLLER_CONNECT) return do_install();
        return 1;
    }
    return 0;
}
