/* loft-svc: Wintun adapter, IP Helper address and routes, packet pipe for loft-front.
   Windows 7 and later. The GUI is asInvoker; this process is LocalSystem.
   WintunDeleteDriver runs only on --uninstall-driver.

   Why it exists: loft.exe (the tray app in src/win/) runs as the normal user and is
   not allowed to change network settings. It asks this Windows service to do that
   over the control pipe \\.\pipe\loft-ctl: one short text command per connection,
   one text reply (see on_cmd; the client side is src/win/svcclient.c).
   loft-front.exe, the separate Go process that carries the tunnel session, moves raw
   IP packets through a second pipe, \\.\pipe\loft-tun (see rx_thread / tx_thread).
   Wintun is a small virtual network adapter driver; wintun.dll sits next to this exe.
   The same exe also installs and removes the service (--install, --uninstall);
   setup/loft-setup.c runs it that way. */
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

/* LOFT_RTS caps how many routes the service remembers (and removes later).
   The two pipe names are shared with src/win/ and with loft-front. */
#define LOFT_RTS 280
#define LOFT_PIPE_CTL L"\\\\.\\pipe\\loft-ctl"
#define LOFT_PIPE_TUN L"\\\\.\\pipe\\loft-tun"

/* Function types for the wintun.dll exports. The DLL is loaded at run time
   (load_wintun), so these mirror the signatures from wintun.h instead of linking
   an import library. */
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

/* W holds wintun.dll and the functions taken from it. All are NULL until
   load_wintun succeeds. */
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

/* T is the tunnel state. One adapter at a time, shared by the control thread
   and the two packet threads; cs guards the pipe client handle.
   up       - the adapter and its session exist.
   hold     - "no internet without the VPN": tun-close is ignored while set, so the
              adapter and routes stay and traffic does not leak to the open network.
   gw       - the machine's own default route, remembered before Loft adds routes;
              "exclude" and "direct" routes go through it.
   rts      - every route this service added, so it can remove exactly those.
   dns_key / dns_old - registry key and old NameServer value, restored on close. */
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

/* Service bookkeeping: SCM status handle and record, the stop event,
   the control-pipe thread and the flag that keeps it looping. */
static SERVICE_STATUS_HANDLE SvcStatus;
static SERVICE_STATUS Svc;
static HANDLE SvcStop;
static HANDLE CtrlThread;
static volatile LONG SvcRun;

/* slog appends one timestamped line to %ProgramData%\loft\svc.log.
   The service has no window, so this file is the only trace of what it did.
   Writes stop once the file reaches 1 MiB, so the log cannot fill the disk. */
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

/* reply copies a text answer into the response buffer for the control pipe. */
static void reply(char *resp, int n, const char *s) {
    if (!resp || n <= 0) return;
    snprintf(resp, (size_t)n, "%s", s ? s : "err");
}

/* reply_err answers "err <what> (<GetLastError>)" and also logs it, so a failed
   command can be diagnosed from svc.log. */
static void reply_err(char *resp, int n, const char *what) {
    char b[192];
    snprintf(b, sizeof b, "err %s (%lu)", what ? what : "fail", GetLastError());
    slog(b);
    reply(resp, n, b);
}

/* is_admin tells whether this process runs as a member of Administrators.
   --install and --uninstall need that; otherwise they re-run themselves with UAC. */
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

/* pipe_sd builds the security descriptor for both pipes: full access for
   SYSTEM, Administrators and interactive users (the signed-in desktop user who
   runs loft.exe), nobody else. Network logons do not get in, and the pipes also
   reject remote clients. The caller frees *sd with LocalFree. */
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

/* loft_guid fills the fixed GUID of the Loft adapter (its bytes spell
   "loft-wintun-chk"). A fixed GUID makes Windows see the same adapter on every
   start instead of a new network each time. */
static void loft_guid(GUID *g) {
    static const unsigned char b[16] = {0x6c, 0x6f, 0x66, 0x74, 0x2d, 0x77, 0x69, 0x6e,
                                         0x74, 0x75, 0x6e, 0x2d, 0x63, 0x68, 0x6b, 0x01};
    memcpy(g, b, 16);
}

/* load_wintun loads wintun.dll from the directory of this exe (never from the
   search path, so another copy cannot be picked up) and binds every function
   into W. Returns 1 on success; a missing export counts as failure. */
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

/* write_full writes all n bytes to a pipe, looping over partial writes.
   Returns 0 on any error. */
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

/* read_full reads exactly n bytes from a pipe, looping over partial reads.
   Returns 0 on error or when the other side closes. */
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

/* write_client sends bytes to the connected loft-front, if any.
   The handle is duplicated under the lock, so stop_threads may close T.client at
   the same moment without this write landing on a closed handle. */
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

/* rx_thread: packets Windows routes into the Loft adapter -> loft-front.
   Each packet is framed as a 4-byte little-endian length plus the raw IP packet
   and written to the loft-tun pipe. Anything shorter than an IPv4 header
   (20 bytes) is dropped. The thread waits on Wintun's read event when the ring
   is empty and ends when T.run drops or the session is closed. */
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

/* tx_packets: loft-front -> Windows. Reads length-framed packets (same framing as
   rx_thread) from one connected pipe client and hands each to Wintun, which
   delivers it to the Windows network stack as if it arrived on the adapter.
   A bad length or a closed pipe ends the loop; the client must reconnect. */
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

/* tx_thread serves the loft-tun pipe: one client at a time, recreated after each
   disconnect while the tunnel runs. pipe_ready is set once a pipe instance exists
   (or creating it failed), so tun_open answers only when loft-front can connect. */
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

/* remember_gw saves the machine's own IPv4 default route (lowest metric, not on
   the Loft adapter) in T.gw. It runs once per tunnel, before Loft's own default
   route can hide it. Routes that must bypass the tunnel use this gateway. */
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

/* wipe_addrs removes every IPv4 address from the Loft adapter. */
static void wipe_addrs(void) {
    PMIB_UNICASTIPADDRESS_TABLE tab = NULL;
    ULONG i;
    if (GetUnicastIpAddressTable(AF_INET, &tab) != NO_ERROR || !tab) return;
    for (i = 0; i < tab->NumEntries; i++) {
        if (tab->Table[i].InterfaceLuid.Value == T.luid.Value) DeleteUnicastIpAddressEntry(&tab->Table[i]);
    }
    FreeMibTable(tab);
}

/* set_addr gives the Loft adapter a single IPv4 address (the overlay address
   from the session; "overlay" is the private network inside the tunnel).
   Old addresses are removed first. MTU is set to 1280, a conservative size,
   so packets fit inside the tunnel's outer transport.
   Returns 0 with the Windows error in GetLastError when the address cannot be set. */
static int set_addr(const char *ip, int prefix) {
    MIB_UNICASTIPADDRESS_ROW row;
    IN_ADDR a;
    int i;
    if (prefix < 1 || prefix > 32) prefix = 32;
    if (inet_pton(AF_INET, ip, &a) != 1) return 0;
    /* 1. A just-created adapter may not have an interface index yet; wait up to 3 s. */
    for (i = 0; i < 15; i++) {
        if (ConvertInterfaceLuidToIndex(&T.luid, &T.idx) == NO_ERROR) break;
        Sleep(200);
    }
    wipe_addrs();
    /* 2. Add the address. Windows can refuse it for a moment right after the
          adapter appears, so retry for about 3 s. */
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
        /* 3. Interface settings: MTU 1280. */
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

/* track remembers a route this service added, so it can be removed later.
   Past LOFT_RTS entries the route stays in Windows but is not tracked. */
static int track(const MIB_IPFORWARD_ROW2 *row) {
    if (T.nrts >= LOFT_RTS) return 0;
    T.rts[T.nrts++] = *row;
    return 1;
}

/* drop_tracked removes tracked routes of one kind: only_default=1 removes the
   default routes (0.0.0.0/0), only_default=0 removes every other route.
   The remaining routes stay tracked. */
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

/* drop_all_routes removes every route this service added. */
static void drop_all_routes(void) {
    int i;
    for (i = 0; i < T.nrts; i++) DeleteIpForwardEntry2(&T.rts[i]);
    T.nrts = 0;
}

/* add_route adds dest/prefix either through the Loft adapter (via_tun=1, metric 5)
   or around it, through the saved physical gateway (via_tun=0). The second form
   needs a known gateway. The route is tracked even if it already existed. */
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

/* parse_cidr reads "a.b.c.d" or "a.b.c.d/n" (IPv4 only). No prefix means /32. */
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

/* dns_flush clears the Windows DNS cache and tells the TCP/IP stack that the
   adapter settings changed, so a new NameServer takes effect without a reboot.
   Both functions are looked up at run time; a missing one is skipped. */
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

/* guid_key builds the registry path that holds the TCP/IP settings of the adapter
   with GUID g (under HKLM). */
static void guid_key(const GUID *g, WCHAR *dst, int n) {
    _snwprintf(dst, (size_t)n,
               L"SYSTEM\\CurrentControlSet\\Services\\Tcpip\\Parameters\\Interfaces\\{%08lX-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X}",
               g->Data1, (unsigned)g->Data2, (unsigned)g->Data3, (unsigned)g->Data4[0], (unsigned)g->Data4[1],
               (unsigned)g->Data4[2], (unsigned)g->Data4[3], (unsigned)g->Data4[4], (unsigned)g->Data4[5],
               (unsigned)g->Data4[6], (unsigned)g->Data4[7]);
    dst[n - 1] = 0;
}

/* dns_set writes the DNS servers for the Loft adapter (the NameServer value in
   its registry key). The first call saves the old value so dns_restore can put
   it back when the tunnel closes. */
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

/* dns_restore puts back the NameServer value saved by dns_set. */
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

/* stop_threads ends the Wintun session and both packet threads. Ending the
   session wakes rx_thread; cancelling pending I/O and closing the client pipe
   wakes tx_thread. Each thread gets up to 5 s to exit. */
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

/* close_adapter closes the Wintun adapter handle and forgets its identity.
   Closing the handle removes the Loft adapter from Windows. */
static void close_adapter(void) {
    if (T.ad && W.close_ad) W.close_ad(T.ad);
    T.ad = NULL;
    T.up = 0;
    T.idx = 0;
    memset(&T.luid, 0, sizeof T.luid);
}

/* tun_shutdown takes the whole tunnel down: threads, routes, address, DNS
   (when restore_dns is set) and the adapter. It also clears hold and the saved
   gateway, so the next tunnel starts clean. */
static void tun_shutdown(int restore_dns) {
    stop_threads();
    drop_all_routes();
    if (T.up) wipe_addrs();
    if (restore_dns) dns_restore();
    close_adapter();
    T.have_gw = 0;
    T.hold = 0;
}

/* tun_open creates the Loft adapter, starts a Wintun session (4 MiB packet
   ring) and the two packet threads. Answers "ok idx=<interface index>".
   Calling it while the tunnel is already up just repeats that answer. */
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
    /* 1. Create the adapter and start a session. If the session does not start,
          close the adapter and try once more with a fresh one. */
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
    /* 2. Start the packet threads and wait (up to 3 s) until loft-tun exists. */
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
    /* 3. Wait briefly for the interface index; the answer reports it. */
    WaitForSingleObject(T.pipe_ready, 3000);
    for (i = 0; i < 10; i++) {
        if (ConvertInterfaceLuidToIndex(&T.luid, &T.idx) == NO_ERROR) break;
        Sleep(100);
    }
    slog("tun-open");
    snprintf(resp, (size_t)n, "ok idx=%lu", (unsigned long)T.idx);
    return 1;
}

/* on_cmd runs one control-pipe command and writes the text answer to resp.
   Commands (sent by src/win/session.c):
     ping, status          - liveness check and current state
     tun-open, tun-close   - create / remove the adapter (tun-close is ignored
                             while hold is on)
     reset                 - remove the adapter even when hold is on
     hold 0|1              - "no internet without the VPN" switch
     addr IP [prefix]      - set the adapter address
     dns LIST              - set the adapter DNS servers (stored as given)
     route CIDR            - send this range into the tunnel
     direct CIDR           - send this range around the tunnel
     exclude IP            - send this host around the tunnel (always /32; used
                             for the server address, so the tunnel's own traffic
                             does not loop back into the tunnel)
     route-default on|off  - add / remove the 0.0.0.0/0 route into the tunnel
     routes-clear          - remove all non-default routes
   Commands from addr on need the tunnel to be up. */
static void on_cmd(char *line, char *resp, int n) {
    char *arg;
    /* Split "name arg" and trim spaces and line ends. */
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

/* ctl_pipe creates one instance of the loft-ctl control pipe (message mode,
   local clients only). */
static HANDLE ctl_pipe(SECURITY_ATTRIBUTES *sa) {
    return CreateNamedPipeW(LOFT_PIPE_CTL, PIPE_ACCESS_DUPLEX, PIPE_TYPE_MESSAGE | PIPE_READMODE_MESSAGE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS, PIPE_UNLIMITED_INSTANCES, 65536, 65536, 0, sa);
}

/* ctrl_thread is the control-pipe server. For each client it reads one command,
   answers it and disconnects. Commands run one after another on this thread,
   so the tunnel state never sees two commands at once. */
static DWORD WINAPI ctrl_thread(LPVOID unused) {
    SECURITY_ATTRIBUTES sa;
    PSECURITY_DESCRIPTOR sd = NULL;
    HANDLE next = INVALID_HANDLE_VALUE;
    (void)unused;
    if (!pipe_sd(&sa, &sd)) return 0;
    while (SvcRun) {
        HANDLE p = next;
        char req[512], resp[256];
        DWORD got = 0;
        next = INVALID_HANDLE_VALUE;
        if (p == INVALID_HANDLE_VALUE) p = ctl_pipe(&sa);
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
        /* The next instance exists before this command runs, so the client
           does not see a missing pipe between hold and addr. */
        next = ctl_pipe(&sa);
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
    if (next != INVALID_HANDLE_VALUE) CloseHandle(next);
    LocalFree(sd);
    return 0;
}

/* report tells the Service Control Manager (SCM, the part of Windows that starts
   and stops services) the current state. Stop is accepted only while running. */
static void report(DWORD state) {
    Svc.dwServiceType = SERVICE_WIN32_OWN_PROCESS;
    Svc.dwCurrentState = state;
    Svc.dwControlsAccepted = (state == SERVICE_RUNNING) ? (SERVICE_ACCEPT_STOP | SERVICE_ACCEPT_SHUTDOWN) : 0;
    Svc.dwWin32ExitCode = NO_ERROR;
    Svc.dwCheckPoint = 0;
    Svc.dwWaitHint = (state == SERVICE_START_PENDING || state == SERVICE_STOP_PENDING) ? 4000 : 0;
    SetServiceStatus(SvcStatus, &Svc);
}

/* handler receives stop and shutdown requests from the SCM. It only signals;
   the cleanup itself runs in ServiceMain. */
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

/* ServiceMain is the service body, called by the SCM on start. It starts the
   control-pipe thread, waits for a stop request, then stops that thread and
   removes the tunnel (adapter, routes, DNS) so nothing is left behind. */
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
    /* Stop requested: end the control thread first, so no command races the cleanup. */
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

/* elevate re-runs this exe with params through UAC ("runas") and waits up to
   2 minutes. Returns the child's exit code, or 1 when UAC was refused. */
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

/* do_install registers loft-svc as the auto-start service "loft" (it depends on
   Tcpip) and starts it. An existing registration is updated to point at this
   exe. Returns 0 on success; the process exit code is what loft-setup checks. */
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

/* do_uninstall stops and deletes the "loft" service. With del_driver it also
   removes the Wintun driver from Windows (other Wintun users would lose it too,
   so plain --uninstall keeps it). */
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

/* wWinMain: --install, --uninstall and --uninstall-driver do that job and exit.
   With no flag, the exe hands itself to the SCM as the "loft" service. If it was
   not started by the SCM (for example, double-clicked), it installs itself. */
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
