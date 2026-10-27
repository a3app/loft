#include "common.h"

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

int svc_call(const char *cmd, char *resp, int n) {
    DWORD ms = 8000;
    if (cmd && strncmp(cmd, "tun-open", 8) == 0) ms = 30000;
    return svc_call_to(cmd, resp, n, ms);
}

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
