/* Windows client GUI: the borderless custom windows (main window, flyout,
   popups, settings and so on). Registers the window class, places windows,
   handles painting, hit tests and the custom close button, and hands layout
   and clicks to ui.c. Drawing primitives live in draw.c. */
#include "common.h"

static HWND hot_hw;
static int hot_on;
static int cls_ok;

int chrome_close_hot(HWND hwnd) { return hot_hw == hwnd && hot_on; }

void chrome_set_close_hot(HWND hwnd, int on) {
    hot_hw = hwnd;
    hot_on = on;
}

Win *chrome_win(HWND hwnd) {
    if (!hwnd) return NULL;
    return (Win *)GetWindowLongPtrW(hwnd, GWLP_USERDATA);
}

static int is_pop(int kind) { return pop_kind(kind); }

static void round_hwnd(HWND hwnd) {
    RECT rc;
    int rad = sc(16);
    GetWindowRect(hwnd, &rc);
    SetWindowRgn(hwnd, CreateRoundRectRgn(0, 0, (rc.right - rc.left) + 1, (rc.bottom - rc.top) + 1, rad, rad), TRUE);
}

void redraw_all(void) {
    int i;
    for (i = 0; i < g.nwin; i++) {
        if (g.wins[i] && IsWindow(g.wins[i])) InvalidateRect(g.wins[i], NULL, FALSE);
    }
}

void place_center(HWND hwnd, int w, int h) {
    RECT work;
    int x, y;
    SystemParametersInfoW(SPI_GETWORKAREA, 0, &work, 0);
    x = work.left + ((work.right - work.left) - w) / 2;
    y = work.top + ((work.bottom - work.top) - h) / 2;
    SetWindowPos(hwnd, HWND_TOP, x, y, w, h, SWP_NOACTIVATE);
}

void place_fly(HWND hwnd, int w, int h) {
    NOTIFYICONIDENTIFIER id;
    RECT ir, work;
    MONITORINFO mi;
    HMONITOR mon;
    int x, y, edge;
    memset(&id, 0, sizeof id);
    id.cbSize = sizeof id;
    id.hWnd = g.msg;
    id.uID = 1;
    if (!Shell_NotifyIconGetRect(&id, &ir)) {
        ir.left = GetSystemMetrics(SM_CXSCREEN) - 48;
        ir.right = ir.left + 24;
        ir.top = GetSystemMetrics(SM_CYSCREEN) - 48;
        ir.bottom = ir.top + 24;
    }
    mon = MonitorFromRect(&ir, MONITOR_DEFAULTTONEAREST);
    mi.cbSize = sizeof mi;
    if (!GetMonitorInfoW(mon, &mi)) SystemParametersInfoW(SPI_GETWORKAREA, 0, &mi.rcWork, 0);
    work = mi.rcWork;
    edge = ABE_BOTTOM;
    if (ir.bottom <= work.top + 8) edge = ABE_TOP;
    else if (ir.left <= work.left + 8 && ir.right < work.left + (work.right - work.left) / 3) edge = ABE_LEFT;
    else if (ir.right >= work.right - 8 && ir.left > work.right - (work.right - work.left) / 3) edge = ABE_RIGHT;
    x = ir.right - w;
    y = ir.top - h - 4;
    if (edge == ABE_TOP) y = ir.bottom + 4;
    else if (edge == ABE_LEFT) {
        x = ir.right + 4;
        y = ir.bottom - h;
    } else if (edge == ABE_RIGHT) {
        x = ir.left - w - 4;
        y = ir.bottom - h;
    }
    if (x < work.left + 4) x = work.left + 4;
    if (y < work.top + 4) y = work.top + 4;
    if (x + w > work.right - 4) x = work.right - w - 4;
    if (y + h > work.bottom - 4) y = work.bottom - h - 4;
    SetWindowPos(hwnd, HWND_TOPMOST, x, y, w, h, SWP_NOACTIVATE);
}

void chrome_hide(HWND hwnd) {
    Win *w = chrome_win(hwnd);
    if (!w) {
        ShowWindow(hwnd, SW_HIDE);
        return;
    }
    ui_capture(w);
    if (!is_pop(w->kind) && loft_streq(g.prefs.taskbar, "always")) {
        ShowWindow(hwnd, SW_MINIMIZE);
        return;
    }
    ShowWindow(hwnd, SW_HIDE);
}

void chrome_taskbar(void) {
    int i;
    int tool = loft_streq(g.prefs.taskbar, "never");
    LONG_PTR ex = tool ? (WS_EX_TOOLWINDOW) : (WS_EX_APPWINDOW);
    for (i = 0; i < g.nwin; i++) {
        Win *w = chrome_win(g.wins[i]);
        if (!w || is_pop(w->kind)) continue;
        SetWindowLongPtrW(g.wins[i], GWL_EXSTYLE, ex);
        SetWindowPos(g.wins[i], NULL, 0, 0, 0, 0, SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED);
    }
}

static LRESULT CALLBACK wnd_proc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    Win *w = chrome_win(hwnd);
    switch (msg) {
    case WM_CREATE: {
        CREATESTRUCTW *cs = (CREATESTRUCTW *)lp;
        w = (Win *)cs->lpCreateParams;
        SetWindowLongPtrW(hwnd, GWLP_USERDATA, (LONG_PTR)w);
        if (w) ui_create(hwnd, w);
        if (w && (w->kind == WK_MAIN || w->kind == WK_FIRST || w->kind == WK_EDIT)) DragAcceptFiles(hwnd, TRUE);
        return 0;
    }
    case WM_SIZE:
        round_hwnd(hwnd);
        return 0;
    case WM_ERASEBKGND:
        return 1;
    case WM_PAINT: {
        PAINTSTRUCT ps;
        HDC dc, mem;
        HBITMAP bmp, old;
        RECT rc;
        int ww, hh;
        HBRUSH bg;
        HPEN pen;
        dc = BeginPaint(hwnd, &ps);
        GetClientRect(hwnd, &rc);
        ww = rc.right;
        hh = rc.bottom;
        mem = CreateCompatibleDC(dc);
        bmp = CreateCompatibleBitmap(dc, ww > 0 ? ww : 1, hh > 0 ? hh : 1);
        old = (HBITMAP)SelectObject(mem, bmp);
        bg = (w && is_pop(w->kind)) ? g.br_fly : g.br_ink;
        FillRect(mem, &rc, bg ? bg : g.br_ink);
        paint_begin(hwnd, mem);
        if (w) ui_layout(hwnd, w, ww, hh);
        pen = (HPEN)SelectObject(mem, g.pen_edge);
        SelectObject(mem, GetStockObject(NULL_BRUSH));
        Rectangle(mem, 0, 0, ww, hh);
        SelectObject(mem, pen);
        BitBlt(dc, 0, 0, ww, hh, mem, 0, 0, SRCCOPY);
        SelectObject(mem, old);
        DeleteObject(bmp);
        DeleteDC(mem);
        EndPaint(hwnd, &ps);
        return 0;
    }
    case WM_CTLCOLOREDIT:
    case WM_CTLCOLORSTATIC: {
        HDC dc = (HDC)wp;
        SetTextColor(dc, C_SNOW);
        SetBkColor(dc, C_FIELD);
        return (LRESULT)g.br_field;
    }
    case WM_MOUSEMOVE: {
        int x = GET_X_LPARAM(lp);
        int y = GET_Y_LPARAM(lp);
        RECT rc;
        int ww, on;
        TRACKMOUSEEVENT ev;
        GetClientRect(hwnd, &rc);
        ww = rc.right;
        on = w && !is_pop(w->kind) && y < sc(32) && x >= ww - sc(46);
        if (on != chrome_close_hot(hwnd)) {
            chrome_set_close_hot(on ? hwnd : NULL, on);
            InvalidateRect(hwnd, NULL, FALSE);
        }
        ev.cbSize = sizeof ev;
        ev.dwFlags = TME_LEAVE;
        ev.hwndTrack = hwnd;
        ev.dwHoverTime = 0;
        TrackMouseEvent(&ev);
        return 0;
    }
    case WM_MOUSELEAVE:
        if (hot_hw == hwnd) {
            hot_hw = NULL;
            hot_on = 0;
            InvalidateRect(hwnd, NULL, FALSE);
        }
        return 0;
    case WM_LBUTTONDOWN:
        if (w && !is_pop(w->kind)) {
            int x = GET_X_LPARAM(lp);
            int y = GET_Y_LPARAM(lp);
            RECT rc;
            GetClientRect(hwnd, &rc);
            if (y < sc(32) && x < rc.right - sc(92)) {
                ReleaseCapture();
                SendMessageW(hwnd, WM_NCLBUTTONDOWN, HTCAPTION, 0);
                return 0;
            }
        }
        return 0;
    case WM_LBUTTONUP:
        if (w) {
            RECT rc;
            int id;
            GetClientRect(hwnd, &rc);
            id = ui_click(hwnd, w, GET_X_LPARAM(lp), GET_Y_LPARAM(lp), rc.right, rc.bottom);
            if (id == HIT_MIN) ShowWindow(hwnd, SW_MINIMIZE);
            else if (id == HIT_CLOSE) chrome_hide(hwnd);
            else if (id) ui_command(hwnd, w, id);
        }
        return 0;
    case WM_KEYDOWN:
        if (wp == VK_ESCAPE && w && is_pop(w->kind)) {
            pop_close();
            return 0;
        }
        break;
    case WM_ACTIVATE:
        if (LOWORD(wp) == WA_INACTIVE && w && is_pop(w->kind)) {
            HWND other = (HWND)lp;
            Win *o = chrome_win(other);
            if (!o || !is_pop(o->kind)) SetTimer(g.msg, T_FOCUS, 80, NULL);
        }
        return 0;
    case WM_COMMAND:
        if (w && HIWORD(wp) == EN_KILLFOCUS) ui_capture(w);
        return 0;
    case WM_DROPFILES: {
        HDROP drop = (HDROP)wp;
        UINT i, n = DragQueryFileW(drop, 0xFFFFFFFF, NULL, 0);
        for (i = 0; i < n; i++) {
            wchar_t path[MAX_PATH];
            if (DragQueryFileW(drop, i, path, MAX_PATH)) import_path(path);
        }
        DragFinish(drop);
        return 0;
    }
    case WM_CLOSE:
        chrome_hide(hwnd);
        return 0;
    case WM_DESTROY:
        DragAcceptFiles(hwnd, FALSE);
        return 0;
    default:
        break;
    }
    return DefWindowProcW(hwnd, msg, wp, lp);
}

static void register_cls(void) {
    WNDCLASSW wc;
    if (cls_ok) return;
    memset(&wc, 0, sizeof wc);
    wc.lpfnWndProc = wnd_proc;
    wc.hInstance = g.inst;
    wc.lpszClassName = L"LoftWnd";
    wc.hCursor = LoadCursor(NULL, IDC_ARROW);
    wc.style = CS_DBLCLKS | CS_HREDRAW | CS_VREDRAW | CS_DROPSHADOW;
    wc.hIcon = LoadIconW(g.inst, MAKEINTRESOURCEW(1));
    cls_ok = RegisterClassW(&wc) != 0;
}

HWND chrome_find(int kind) {
    int i;
    for (i = 0; i < g.nwin; i++) {
        Win *w = chrome_win(g.wins[i]);
        if (w && w->kind == kind && IsWindow(g.wins[i])) return g.wins[i];
    }
    return NULL;
}

HWND chrome_open(int kind, int w, int h) {
    Win *wp;
    HWND hwnd;
    DWORD ex, style;
    int ww = w, hh = h;
    register_cls();
    if (hh == 0) {
        Win tmp;
        memset(&tmp, 0, sizeof tmp);
        tmp.kind = kind;
        hh = ui_measure(&tmp, ww);
        if (hh < sc(48)) hh = sc(48);
    }
    if (g.nwin >= LOFT_WINS) return NULL;
    wp = (Win *)calloc(1, sizeof *wp);
    if (!wp) return NULL;
    wp->kind = kind;
    wp->extra[0] = -1;
    if (is_pop(kind)) ex = WS_EX_TOOLWINDOW | WS_EX_TOPMOST;
    else if (loft_streq(g.prefs.taskbar, "never")) ex = WS_EX_TOOLWINDOW;
    else ex = WS_EX_APPWINDOW;
    style = WS_POPUP | WS_CLIPCHILDREN | WS_CLIPSIBLINGS;
    hwnd = CreateWindowExW(ex, L"LoftWnd", L"Loft", style, 0, 0, ww, hh, NULL, NULL, g.inst, wp);
    if (!hwnd) {
        free(wp);
        return NULL;
    }
    g.wins[g.nwin++] = hwnd;
    if (is_pop(kind)) place_fly(hwnd, ww, hh);
    else place_center(hwnd, ww, hh);
    return hwnd;
}
