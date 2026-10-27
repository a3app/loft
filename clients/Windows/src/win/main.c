#include "common.h"

#include <wchar.h>

static int click_pending;
static DWORD click_ms;

static void on_tray_left(void) {
    DWORD now = GetTickCount();
    if (now - click_ms < 40) return;
    click_ms = now;
    if (click_pending) {
        click_pending = 0;
        KillTimer(g.msg, T_CLICK);
        open_main();
        return;
    }
    click_pending = 1;
    SetTimer(g.msg, T_CLICK, GetDoubleClickTime(), NULL);
}

static void handle_cmd(const wchar_t *cmd, int other) {
    int argc = 0, i, tray = 0, opened = 0;
    wchar_t **argv;
    if (!cmd) cmd = L"";
    argv = CommandLineToArgvW(cmd, &argc);
    if (!argv) return;
    for (i = 1; i < argc; i++) {
        if (wcscmp(argv[i], L"--tray") == 0) tray = 1;
        else {
            import_path(argv[i]);
            opened = 1;
        }
    }
    LocalFree(argv);
    if (other) {
        if (!opened) open_main();
        return;
    }
    if (!opened) {
        if (!tray && !g.prefs.first_done && g.nprof == 0) open_first();
        else if (!tray) open_main();
    } else if (!tray && !chrome_find(WK_MAIN)) open_main();
}

void quit_app(void) {
    if (g.quit) return;
    g.quit = 1;
    pop_close();
    if (g.msg) DestroyWindow(g.msg);
}

static LRESULT CALLBACK msg_proc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    switch (msg) {
    case WM_LOFT_TRAY:
        if (lp == WM_RBUTTONUP || lp == WM_CONTEXTMENU) {
            click_pending = 0;
            KillTimer(hwnd, T_CLICK);
            menu_popup();
        } else if (lp == WM_LBUTTONDBLCLK) {
            click_pending = 0;
            KillTimer(hwnd, T_CLICK);
            open_main();
        } else if (lp == NIN_KEYSELECT) open_main();
        else if (lp == WM_LBUTTONUP || lp == NIN_SELECT) on_tray_left();
        return 0;
    case WM_TIMER:
        if (wp == T_CLICK) {
            KillTimer(hwnd, T_CLICK);
            if (click_pending) {
                click_pending = 0;
                fly_toggle();
            }
        } else if (wp == T_BLINK) {
            g.blink ^= 1;
            tray_update();
        } else if (wp == T_FOCUS) {
            HWND fg = GetForegroundWindow();
            Win *fw = chrome_win(fg);
            if (!fw || !pop_kind(fw->kind)) {
                KillTimer(hwnd, T_FOCUS);
                pop_close();
            }
        } else if (wp >= T_RETRY && wp < T_RETRY + LOFT_SESS) {
            KillTimer(hwnd, wp);
            session_retry((int)wp - T_RETRY);
        }
        return 0;
    case WM_LOFT_STATUS: {
        char *line = (char *)lp;
        session_on_status((int)wp, line);
        free(line);
        return 0;
    }
    case WM_LOFT_EXIT:
        session_on_exit((int)wp);
        return 0;
    case WM_SETTINGCHANGE:
        if (lp && lstrcmpiW((const wchar_t *)lp, L"ImmersiveColorSet") == 0) tray_theme();
        return 0;
    case WM_LOFT_EXTIP: {
        char *ip = (char *)lp;
        session_extip((int)wp, ip ? ip : "");
        free(ip);
        return 0;
    }
    case WM_LOFT_UPD:
        if (wp == 3) {
            if (lp) journal_add((char *)lp);
            free((void *)lp);
            quit_app();
            return 0;
        }
        if (wp == 0 && lp) {
            loft_strlcpy(g.update_ver, sizeof g.update_ver, (char *)lp);
            open_update();
        } else if (lp) journal_add((char *)lp);
        free((void *)lp);
        return 0;
    case WM_LOFT_TUN: {
        int packed = (int)wp;
        char *resp = (char *)lp;
        session_tun_ready(packed >> 8, packed & 1, resp);
        free(resp);
        return 0;
    }
    case WM_LOFT_PULL:
        routes_pulled((int)wp, (char *)lp);
        return 0;
    case WM_COPYDATA: {
        COPYDATASTRUCT *cd = (COPYDATASTRUCT *)lp;
        if (cd && cd->lpData) handle_cmd((const wchar_t *)cd->lpData, 1);
        return TRUE;
    }
    case WM_DESTROY:
        g.quit = 1;
        app_shutdown();
        PostQuitMessage(0);
        return 0;
    default:
        break;
    }
    return DefWindowProcW(hwnd, msg, wp, lp);
}

int WINAPI wWinMain(HINSTANCE inst, HINSTANCE prev, LPWSTR cmdline, int show) {
    WNDCLASSW wc;
    MSG msg;
    HANDLE mx;
    WSADATA wsa;
    int argc = 0, ai;
    wchar_t **argv;
    (void)prev;
    (void)cmdline;
    (void)show;
    argv = CommandLineToArgvW(GetCommandLineW(), &argc);
    if (argv) {
        for (ai = 1; ai + 3 < argc; ai++) {
            if (wcscmp(argv[ai], L"--apply-update") == 0) {
                unsigned long pid = wcstoul(argv[ai + 2], NULL, 10);
                int ok = upd_apply(argv[ai + 1], pid, argv[ai + 3]);
                LocalFree(argv);
                ExitProcess(ok ? 0 : 1);
            }
        }
        LocalFree(argv);
    }
    mx = CreateMutexW(NULL, TRUE, L"Local\\LoftClient");
    if (mx && GetLastError() == ERROR_ALREADY_EXISTS) {
        HWND h = FindWindowW(L"LoftMsg", L"Loft");
        wchar_t *line = GetCommandLineW();
        if (h && line) {
            COPYDATASTRUCT cd;
            cd.dwData = 1;
            cd.cbData = (DWORD)((wcslen(line) + 1) * sizeof(wchar_t));
            cd.lpData = line;
            SendMessageW(h, WM_COPYDATA, 0, (LPARAM)&cd);
        }
        CloseHandle(mx);
        return 0;
    }
    g.inst = inst;
    SetProcessDPIAware();
    WSAStartup(MAKEWORD(2, 2), &wsa);
    memset(&wc, 0, sizeof wc);
    wc.lpfnWndProc = msg_proc;
    wc.hInstance = inst;
    wc.lpszClassName = L"LoftMsg";
    RegisterClassW(&wc);
    g.msg = CreateWindowExW(WS_EX_TOOLWINDOW, L"LoftMsg", L"Loft", WS_POPUP, 0, 0, 0, 0, NULL, NULL, inst, NULL);
    app_boot();
#ifndef LOFT_STORE
    assoc_apply();
    login_apply();
#endif
    tray_init();
    if (g.nprof > 0 && !g.prefs.first_done) {
        g.prefs.first_done = 1;
        store_save_prefs();
    }
    handle_cmd(GetCommandLineW(), 0);
    if (g.prefs.auto_connect && g.prefs.first_done && g.active >= 0) session_connect(g.active);
#ifndef LOFT_STORE
    if (g.prefs.update_check) net_update_check();
#endif
    while (GetMessageW(&msg, NULL, 0, 0) > 0) {
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }
    if (mx) CloseHandle(mx);
    return 0;
}
