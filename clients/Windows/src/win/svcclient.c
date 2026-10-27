/* Windows client GUI: the client side of the loft service (loft-svc.exe, a
   Windows service running as LocalSystem that owns the Wintun tunnel adapter,
   its address, routes and DNS). loft.exe runs as the normal user and cannot
   change network settings itself, so it sends one-line text commands such as
   "tun-open" or "route CIDR" over the control pipe \\.\pipe\loft-ctl and
   reads one text reply. The command list is in svc/loft-svc.c (on_cmd).
   Also installs the service on demand. Callers: session.c and app.c. */
#include "common.h"

/* svc_call_to sends cmd to the service and copies its reply into resp (n
   bytes), waiting at most ms milliseconds (0 means 8 s). Returns 1 when the
   reply starts with "ok" or "status". When the pipe cannot be reached at all
   the reply is "err pipe <code>"; callers use that to tell "service not
   installed or not running" apart from a refused command. */
int svc_call_to(const char *cmd, char *resp, int n, DWORD ms) {
    char buf[512];
    DWORD got = 0;
    BOOL ok;
    if (resp && n > 0) resp[0] = 0;
    if (!cmd) cmd = "";
    {
        /* The service recreates the pipe between commands. CallNamedPipe
           returns at once with ERROR_FILE_NOT_FOUND in that gap, so a burst
           (hold, addr, routes) drops the first command and the tunnel never
           gets its address. Wait out the gap inside the same budget. */
        DWORD budget = ms ? ms : 8000;
        DWORD t0 = GetTickCount();
        DWORD err = 0;
        ok = FALSE;
        for (;;) {
            DWORD elapsed = GetTickCount() - t0;
            DWORD left = budget - elapsed;
            if (elapsed >= budget) break;
            if (!WaitNamedPipeA("\\\\.\\pipe\\loft-ctl", left > 200 ? 200 : left) && GetLastError() == ERROR_FILE_NOT_FOUND) {
                if (GetTickCount() - t0 >= budget) break;
                Sleep(20);
                continue;
            }
            elapsed = GetTickCount() - t0;
            if (elapsed >= budget) break;
            ok = CallNamedPipeA("\\\\.\\pipe\\loft-ctl", (LPVOID)cmd, (DWORD)strlen(cmd), buf, sizeof buf - 1, &got, budget - elapsed);
            if (ok) break;
            err = GetLastError();
            if (err != ERROR_PIPE_BUSY && err != ERROR_FILE_NOT_FOUND) break;
            if (GetTickCount() - t0 >= budget) break;
        }
        if (!ok) {
            if (resp && n > 0) snprintf(resp, (size_t)n, "err pipe %lu", err ? (unsigned long)err : (unsigned long)GetLastError());
            return 0;
        }
    }
    if (got >= sizeof buf) got = sizeof buf - 1;
    buf[got] = 0;
    if (resp && n > 0) snprintf(resp, (size_t)n, "%s", buf);
    return strncmp(buf, "ok", 2) == 0 || strncmp(buf, "status", 6) == 0;
}

/* svc_call is svc_call_to with the usual timeout: 8 s, or 30 s for
   tun-open, because creating the adapter can take a while on first use. */
int svc_call(const char *cmd, char *resp, int n) {
    DWORD ms = 8000;
    if (cmd && strncmp(cmd, "tun-open", 8) == 0) ms = 30000;
    return svc_call_to(cmd, resp, n, ms);
}

/* svc_install_interactive runs "loft-svc.exe --install" from the app folder
   with the "runas" verb, so Windows shows the administrator (UAC) prompt.
   Waits up to two minutes and returns 1 when the installer exited with 0.
   Returns 0 when loft-svc.exe is missing or the user declined the prompt. */
int svc_install_interactive(void) {
    wchar_t path[MAX_PATH];
    SHELLEXECUTEINFOW sei;
    DWORD code = 1;
    exe_file(path, MAX_PATH, L"loft-svc.exe");
    if (GetFileAttributesW(path) == INVALID_FILE_ATTRIBUTES) return 0;
    memset(&sei, 0, sizeof sei);
    sei.cbSize = sizeof sei;
    sei.fMask = SEE_MASK_NOCLOSEPROCESS;
    sei.lpVerb = L"runas";
    sei.lpFile = path;
    sei.lpParameters = L"--install";
    sei.nShow = SW_SHOWNORMAL;
    if (!ShellExecuteExW(&sei)) return 0;
    if (sei.hProcess) {
        WaitForSingleObject(sei.hProcess, 120000);
        GetExitCodeProcess(sei.hProcess, &code);
        CloseHandle(sei.hProcess);
    }
    return code == 0;
}
