#include "common.h"

int svc_call_to(const char *cmd, char *resp, int n, DWORD ms) {
    char buf[512];
    DWORD got = 0;
    BOOL ok;
    if (resp && n > 0) resp[0] = 0;
    if (!cmd) cmd = "";
    ok = CallNamedPipeA("\\\\.\\pipe\\loft-ctl", (LPVOID)cmd, (DWORD)strlen(cmd), buf, sizeof buf - 1, &got, ms ? ms : 8000);
    if (!ok) {
        if (resp && n > 0) snprintf(resp, (size_t)n, "err pipe");
        return 0;
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
