#include "common.h"

typedef int(WINAPI *GpStartup)(ULONG_PTR *, const void *, void *);
typedef int(WINAPI *GpFromFile)(const wchar_t *, void **);
typedef int(WINAPI *GpHicon)(void *, HICON *);
typedef int(WINAPI *GpDispose)(void *);

typedef struct GpIn {
    UINT32 GdiplusVersion;
    void *DebugEventCallback;
    BOOL SuppressBackgroundThread;
    BOOL SuppressExternalCodecs;
} GpIn;

static HICON png_icon(const wchar_t *path) {
    static HMODULE mod;
    static GpStartup pStart;
    static GpFromFile pFile;
    static GpHicon pIcon;
    static GpDispose pDispose;
    static ULONG_PTR token;
    static int ready;
    GpIn in;
    void *img = NULL;
    HICON ic = NULL;
    if (!path) return NULL;
    if (!ready) {
        mod = LoadLibraryW(L"gdiplus.dll");
        if (!mod) return NULL;
        pStart = (GpStartup)(void *)GetProcAddress(mod, "GdiplusStartup");
        pFile = (GpFromFile)(void *)GetProcAddress(mod, "GdipCreateBitmapFromFile");
        pIcon = (GpHicon)(void *)GetProcAddress(mod, "GdipCreateHICONFromBitmap");
        pDispose = (GpDispose)(void *)GetProcAddress(mod, "GdipDisposeImage");
        if (!pStart || !pFile || !pIcon || !pDispose) return NULL;
        memset(&in, 0, sizeof in);
        in.GdiplusVersion = 1;
        if (pStart(&token, &in, NULL) != 0) return NULL;
        ready = 1;
    }
    if (pFile(path, &img) != 0 || !img) return NULL;
    if (pIcon(img, &ic) != 0) ic = NULL;
    pDispose(img);
    return ic;
}

static HICON icon_dib(int px, int state) {
    BITMAPINFO bi;
    HDC dc, mem;
    HBITMAP color;
    HBITMAP mask;
    DWORD *pix;
    unsigned char *mbit;
    ICONINFO ii;
    HICON ic = NULL;
    int x, y, s = px;
    int lr = 0xC6, lg = 0x7B, lb = 0x3A;
    if (s < 16) s = 16;
    if (state == 1) {
        lr = 0xE8;
        lg = 0xB8;
        lb = 0x4A;
    } else if (state == 2) {
        lr = 0xE2;
        lg = 0x4B;
        lb = 0x4B;
    }
    memset(&bi, 0, sizeof bi);
    bi.bmiHeader.biSize = sizeof(BITMAPINFOHEADER);
    bi.bmiHeader.biWidth = s;
    bi.bmiHeader.biHeight = -s;
    bi.bmiHeader.biPlanes = 1;
    bi.bmiHeader.biBitCount = 32;
    bi.bmiHeader.biCompression = BI_RGB;
    dc = GetDC(NULL);
    mem = CreateCompatibleDC(dc);
    color = CreateDIBSection(mem, &bi, DIB_RGB_COLORS, (void **)&pix, NULL, 0);
    if (!color || !pix) {
        if (color) DeleteObject(color);
        DeleteDC(mem);
        ReleaseDC(NULL, dc);
        return NULL;
    }
    for (y = 0; y < s; y++) {
        for (x = 0; x < s; x++) {
            int m = s / 8;
            int body, lamp, r = 0, g = 0, b = 0, a = 0;
            int x0, y0, x1, y1;
            if (m < 1) m = 1;
            body = x >= m && y >= m && x < s - m && y < s - m;
            x0 = s * 3 / 8;
            y0 = s * 5 / 16;
            x1 = x0 + s / 4;
            y1 = y0 + s / 5;
            if (x1 <= x0) x1 = x0 + 2;
            if (y1 <= y0) y1 = y0 + 2;
            lamp = x >= x0 && x < x1 && y >= y0 && y < y1;
            if (lamp) {
                r = lr;
                g = lg;
                b = lb;
                a = 255;
            } else if (body) {
                r = 0xC6;
                g = 0x7B;
                b = 0x3A;
                a = 255;
            }
            pix[y * s + x] = ((DWORD)a << 24) | ((DWORD)r << 16) | ((DWORD)g << 8) | (DWORD)b;
        }
    }
    mbit = (unsigned char *)calloc((size_t)((s * s + 7) / 8), 1);
    mask = CreateBitmap(s, s, 1, 1, mbit);
    free(mbit);
    memset(&ii, 0, sizeof ii);
    ii.fIcon = TRUE;
    ii.hbmColor = color;
    ii.hbmMask = mask;
    ic = CreateIconIndirect(&ii);
    DeleteObject(color);
    if (mask) DeleteObject(mask);
    DeleteDC(mem);
    ReleaseDC(NULL, dc);
    return ic;
}

static int icon_px(void) {
    if (g.dpi >= 192) return 32;
    if (g.dpi >= 144) return 24;
    if (g.dpi >= 120) return 20;
    return 16;
}

static HICON load_png(int px, int off) {
    wchar_t dir[MAX_PATH], sub[MAX_PATH], path[MAX_PATH], file[64];
    char name[64];
    snprintf(name, sizeof name, "loft-tray%s-%d.png", off ? "-off" : "", px);
    widen(name, file, 64);
    exe_dir(dir, MAX_PATH);
    path_join(sub, MAX_PATH, dir, L"tray");
    path_join(path, MAX_PATH, sub, file);
    return png_icon(path);
}

static void tip_set(void) {
    wchar_t line[128], a[80], name[80];
    Sess *s = session_primary();
    const wchar_t *pfx = L"Loft \x2014 ";
    int i = 0, j = 0;
    name[0] = 0;
    if (s && s->phase == PH_ON && s->profile >= 0 && s->profile < g.nprof) {
        widen(loft_t("pill_on"), a, 80);
        widen(g.prof[s->profile].name[0] ? g.prof[s->profile].name : g.prof[s->profile].id, name, 80);
    } else {
        widen(loft_t("pop_off"), a, 80);
    }
    while (pfx[j] && i < 120) line[i++] = pfx[j++];
    j = 0;
    while (a[j] && i < 120) line[i++] = a[j++];
    if (name[0]) {
        if (i < 126) line[i++] = L' ';
        j = 0;
        while (name[j] && i < 127) line[i++] = name[j++];
    }
    line[i] = 0;
    lstrcpynW(g.nid.szTip, line, 128);
}

static int icon_which(void) {
    int ph = session_phase();
    if (ph == PH_ON) return 1;
    if (ph == PH_CONNECTING) return g.blink ? 1 : 0;
    if (ph == PH_ERR || ph == PH_HELD) return 2;
    return 0;
}

void tray_init(void) {
    int px = icon_px();
    g.icon[0] = load_png(px, 1);
    g.icon[1] = load_png(px, 0);
    g.icon[2] = icon_dib(px, 2);
    if (!g.icon[0]) g.icon[0] = icon_dib(px, 0);
    if (!g.icon[1]) g.icon[1] = icon_dib(px, 1);
    memset(&g.nid, 0, sizeof g.nid);
    g.nid.cbSize = NOTIFYICONDATAW_V3_SIZE;
    g.nid.hWnd = g.msg;
    g.nid.uID = 1;
    g.nid.uFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP;
    g.nid.uCallbackMessage = WM_LOFT_TRAY;
    g.nid.hIcon = g.icon[0] ? g.icon[0] : g.icon[1];
    tip_set();
    g.tray_on = Shell_NotifyIconW(NIM_ADD, &g.nid) ? 1 : 0;
    if (g.tray_on) {
        g.nid.uVersion = NOTIFYICON_VERSION;
        Shell_NotifyIconW(NIM_SETVERSION, &g.nid);
    }
}

void tray_update(void) {
    if (!g.tray_on) return;
    g.nid.cbSize = NOTIFYICONDATAW_V3_SIZE;
    g.nid.uFlags = NIF_ICON | NIF_TIP;
    g.nid.hIcon = g.icon[icon_which()];
    if (!g.nid.hIcon) g.nid.hIcon = g.icon[0];
    tip_set();
    Shell_NotifyIconW(NIM_MODIFY, &g.nid);
    if (session_phase() == PH_CONNECTING) SetTimer(g.msg, T_BLINK, 450, NULL);
    else KillTimer(g.msg, T_BLINK);
}

void tray_remove(void) {
    if (!g.tray_on) return;
    g.nid.cbSize = NOTIFYICONDATAW_V3_SIZE;
    g.nid.uFlags = 0;
    Shell_NotifyIconW(NIM_DELETE, &g.nid);
    g.tray_on = 0;
}

int pop_kind(int kind) {
    return kind == WK_FLY || kind == WK_LIST || kind == WK_MENU || kind == WK_LANG;
}

void pop_close(void) {
    int i;
    int kinds[4] = {WK_FLY, WK_LIST, WK_MENU, WK_LANG};
    for (i = 0; i < 4; i++) {
        HWND h = chrome_find(kinds[i]);
        if (h) ShowWindow(h, SW_HIDE);
    }
    if (g.msg) KillTimer(g.msg, T_FOCUS);
}

void fly_close(void) {
    HWND h = chrome_find(WK_FLY);
    HWND list = chrome_find(WK_LIST);
    if (h) ShowWindow(h, SW_HIDE);
    if (list) ShowWindow(list, SW_HIDE);
}

static void show_pop(HWND h) {
    if (!h) return;
    ShowWindow(h, SW_SHOW);
    SetForegroundWindow(h);
    if (g.msg) SetTimer(g.msg, T_FOCUS, 250, NULL);
}

void fly_toggle(void) {
    HWND h = chrome_find(WK_FLY);
    int w = sc(300), hh;
    if (h && IsWindowVisible(h)) {
        pop_close();
        return;
    }
    pop_close();
    if (!h) h = chrome_open(WK_FLY, w, 0);
    if (!h) return;
    hh = ui_measure(chrome_win(h), w);
    if (hh < sc(80)) hh = sc(80);
    place_fly(h, w, hh);
    show_pop(h);
}

void menu_popup(void) {
    HWND h;
    POINT pt;
    RECT work;
    int w = sc(260), hh;
    pop_close();
    h = chrome_find(WK_MENU);
    if (!h) h = chrome_open(WK_MENU, w, 0);
    if (!h) return;
    hh = ui_measure(chrome_win(h), w);
    GetCursorPos(&pt);
    SystemParametersInfoW(SPI_GETWORKAREA, 0, &work, 0);
    if (pt.x + w > work.right) pt.x = work.right - w;
    if (pt.y + hh > work.bottom) pt.y = pt.y - hh;
    if (pt.y < work.top) pt.y = work.top;
    if (pt.x < work.left) pt.x = work.left;
    SetWindowPos(h, HWND_TOPMOST, pt.x, pt.y, w, hh, SWP_NOACTIVATE);
    show_pop(h);
}

void list_popup(HWND anchor) {
    HWND h;
    RECT fr, work;
    int w = sc(240), hh, x, y;
    if (!anchor) anchor = chrome_find(WK_FLY);
    if (!anchor) anchor = chrome_find(WK_MENU);
    h = chrome_find(WK_LIST);
    if (!h) h = chrome_open(WK_LIST, w, 0);
    if (!h) return;
    hh = ui_measure(chrome_win(h), w);
    if (anchor) GetWindowRect(anchor, &fr);
    else fr.left = fr.right = fr.top = 80;
    SystemParametersInfoW(SPI_GETWORKAREA, 0, &work, 0);
    x = fr.left - w - 4;
    y = fr.top;
    if (x < work.left) x = fr.right + 4;
    if (y + hh > work.bottom) y = work.bottom - hh;
    if (y < work.top) y = work.top;
    SetWindowPos(h, HWND_TOPMOST, x, y, w, hh, SWP_NOACTIVATE);
    show_pop(h);
}

void lang_popup(HWND anchor) {
    HWND h;
    RECT fr, work;
    int w = sc(180), hh, x, y;
    h = chrome_find(WK_LANG);
    if (!h) h = chrome_open(WK_LANG, w, 0);
    if (!h) return;
    hh = ui_measure(chrome_win(h), w);
    if (anchor) GetWindowRect(anchor, &fr);
    else {
        POINT pt;
        GetCursorPos(&pt);
        fr.left = fr.right = pt.x;
        fr.top = fr.bottom = pt.y;
    }
    SystemParametersInfoW(SPI_GETWORKAREA, 0, &work, 0);
    x = fr.left;
    y = fr.bottom + 4;
    if (y + hh > work.bottom) y = fr.top - hh - 4;
    if (x + w > work.right) x = work.right - w;
    if (x < work.left) x = work.left;
    if (y < work.top) y = work.top;
    SetWindowPos(h, HWND_TOPMOST, x, y, w, hh, SWP_NOACTIVATE);
    show_pop(h);
}
