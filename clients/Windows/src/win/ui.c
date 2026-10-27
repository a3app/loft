/* Windows client GUI: the layout and command handling of every window kind
   (main, flyout, menu, settings, edit, routes, journal, about, update, first
   run). Layouts are drawn with draw.c inside chrome.c windows; commands call
   into session.c and store.c.
   Each layout_* function is immediate-mode: it is run again for every paint,
   every click (to find the control under the mouse) and every size
   measurement, so drawing and hit areas always agree. ui_button style codes
   used here: 1 primary (copper), 0 plain field, 2 quiet link, 3 left-aligned
   row, 7 selected row. Per-window state lives in Win.tab and Win.extra[]. */
#include "common.h"

/* kinds lists the protocol kinds the edit window cycles through with its
   "Protocol" button. The first six are loft transports; sstp, vless and
   trojan are other protocols that loft-front also speaks. */
static const char *kinds[9] = {"get", "ws", "h2", "quic", "h3", "l3", "sstp", "vless", "trojan"};

/* show_wnd brings a window to the front, restoring it if it is minimized. */
static void show_wnd(HWND h) {
    if (!h) return;
    if (IsIconic(h)) ShowWindow(h, SW_RESTORE);
    else ShowWindow(h, SW_SHOW);
    SetForegroundWindow(h);
}

/* pname returns the display name of profile i: its name, or its id when the
   name is empty. */
static const char *pname(int i) {
    if (i < 0 || i >= g.nprof) return "";
    return g.prof[i].name[0] ? g.prof[i].name : g.prof[i].id;
}

/* ed_get reads the text of an edit box as trimmed UTF-8. */
static void ed_get(HWND ed, char *d, int n) {
    wchar_t w[1024];
    if (!d || n <= 0) return;
    d[0] = 0;
    if (!ed) return;
    w[0] = 0;
    GetWindowTextW(ed, w, 1024);
    narrow(w, d, n);
    loft_trim(d);
}

/* ed_set puts UTF-8 text into an edit box (NULL means empty). */
static void ed_set(HWND ed, const char *s) {
    wchar_t w[1024];
    if (!ed) return;
    widen(s ? s : "", w, 1024);
    SetWindowTextW(ed, w);
}

/* make_edit creates a hidden single-line edit box (the only native child
   controls in these windows; everything else is drawn). secret masks the
   input for keys. Text is limited to 500 characters. */
static HWND make_edit(HWND parent, int id, int secret) {
    DWORD st = WS_CHILD | ES_AUTOHSCROLL | WS_TABSTOP;
    HWND ed;
    if (secret) st |= ES_PASSWORD;
    ed = CreateWindowExW(0, L"EDIT", L"", st, 0, 0, 0, 0, parent, (HMENU)(INT_PTR)id, g.inst, NULL);
    if (!ed) return NULL;
    SendMessageW(ed, WM_SETFONT, (WPARAM)g.f_ui, TRUE);
    SendMessageW(ed, EM_SETLIMITTEXT, 500, 0);
    return ed;
}

/* move_ed places an edit box and shows or hides it. The layout functions
   call it on every pass, so it only touches the window when something has
   actually changed, to avoid flicker. */
static void move_ed(HWND ed, int x, int y, int w, int h, int show) {
    RECT r;
    if (!ed) return;
    if (w < 1) w = 1;
    if (h < 1) h = 1;
    GetWindowRect(ed, &r);
    MapWindowPoints(NULL, GetParent(ed), (POINT *)&r, 2);
    if (r.left != x || r.top != y || (r.right - r.left) != w || (r.bottom - r.top) != h)
        SetWindowPos(ed, NULL, x, y, w, h, SWP_NOZORDER | SWP_NOACTIVATE);
    if (show && !IsWindowVisible(ed)) ShowWindow(ed, SW_SHOW);
    if (!show && IsWindowVisible(ed)) ShowWindow(ed, SW_HIDE);
}

/* nobr copies a translated string and turns each "<br>" into a newline.
   The shared texts use <br> as a line break. */
static void nobr(const char *s, char *d, int n) {
    int i = 0;
    if (!s) s = "";
    while (*s && i < n - 1) {
        if (s[0] == '<' && s[1] == 'b' && s[2] == 'r' && s[3] == '>') {
            d[i++] = '\n';
            s += 4;
        } else d[i++] = *s++;
    }
    d[i] = 0;
}

/* phase_name returns the translated label for a session phase (PH_*). */
static const char *phase_name(int ph) {
    if (ph == PH_ON) return loft_t("pill_on");
    if (ph == PH_CONNECTING) return loft_t("connecting");
    if (ph == PH_ERR) return loft_t("err");
    if (ph == PH_HELD) return loft_t("route.held");
    return loft_t("pill_off");
}

/* action_name returns the label of the main connect button for the current
   overall phase: Disconnect, Cancel, Retry or Connect. */
static const char *action_name(void) {
    int ph = session_phase();
    if (ph == PH_ON) return loft_t("disconnect");
    if (ph == PH_CONNECTING) return loft_t("cancel");
    if (ph == PH_ERR) return loft_t("retry");
    return loft_t("connect");
}

/* fmt_rate formats the session's up and down traffic counters as
   "up  down unit", in kilobytes or kilobits per the user's preference. */
static void fmt_rate(char *d, int n, Sess *s) {
    long long up = s ? s->up : 0;
    long long down = s ? s->down : 0;
    const char *unit = loft_t(g.prefs.rate_bits ? "u_kbit" : "u_kb");
    if (g.prefs.rate_bits) {
        up = up * 8 / 1000;
        down = down * 8 / 1000;
    } else {
        up /= 1000;
        down /= 1000;
    }
    snprintf(d, (size_t)n, "%lld  %lld %s", up, down, unit);
}

/* fmt_up formats how long the session has been up as m:ss. Empty unless the
   session is on. */
static void fmt_up(char *d, int n, Sess *s) {
    unsigned sec;
    if (!d || n <= 0) return;
    d[0] = 0;
    if (!s || !s->up_since || s->phase != PH_ON) return;
    sec = (GetTickCount() - s->up_since) / 1000u;
    snprintf(d, (size_t)n, "%u:%02u", sec / 60u, sec % 60u);
}

/* row_kv draws one "label ... value" info row (label from a translation key)
   and moves *y down past it. */
static void row_kv(int x, int *y, int w, const char *key, const char *val) {
    int h = sc(26);
    ui_text(x, *y, w / 2, h, loft_t(key), C_MUTE, g.f_small, DT_LEFT | DT_VCENTER | DT_SINGLELINE | DT_END_ELLIPSIS);
    ui_text(x + w / 2, *y, w / 2, h, val ? val : "", C_SNOW, g.f_ui, DT_RIGHT | DT_VCENTER | DT_SINGLELINE | DT_END_ELLIPSIS);
    *y += h;
}

/* field lays out a labelled text field: the caption, then the edit box
   moved into place under it. Moves *y down past the field. */
static int field(HWND ed, int x, int *y, int w, const char *label) {
    ui_text(x, *y, w, sc(16), label, C_MUTE, g.f_small, DT_LEFT | DT_VCENTER | DT_SINGLELINE | DT_END_ELLIPSIS);
    *y += sc(16);
    ui_fill(x, *y, w, sc(24), NULL);
    move_ed(ed, x, *y, w, sc(24), 1);
    *y += sc(30);
    return 0;
}

/* journal_text joins the journal (the in-app log, a ring buffer of
   LOFT_JOURNAL lines in g.journal) into one CRLF-separated string, oldest
   line first. The caller frees the result. */
static char *journal_text(void) {
    int count = g.nj < LOFT_JOURNAL ? g.nj : LOFT_JOURNAL;
    int start = g.nj < LOFT_JOURNAL ? 0 : g.nj % LOFT_JOURNAL;
    char *b;
    int i, o = 0;
    b = (char *)malloc((size_t)count * 242u + 1u);
    if (!b) return NULL;
    for (i = 0; i < count; i++) {
        const char *line = g.journal[(start + i) % LOFT_JOURNAL];
        int L = (int)strlen(line);
        memcpy(b + o, line, (size_t)L);
        o += L;
        b[o++] = '\r';
        b[o++] = '\n';
    }
    b[o] = 0;
    return b;
}

/* journal_sync refreshes the journal window, if it is open, with the
   current log and scrolls to the last line. */
void journal_sync(void) {
    HWND h = chrome_find(WK_JOURNAL);
    Win *w;
    char *t;
    wchar_t *wide;
    int nw;
    if (!h) return;
    w = chrome_win(h);
    if (!w || !w->ed[0]) return;
    t = journal_text();
    if (!t) return;
    nw = MultiByteToWideChar(CP_UTF8, 0, t, -1, NULL, 0);
    wide = nw > 0 ? (wchar_t *)calloc((size_t)nw, sizeof(wchar_t)) : NULL;
    if (wide) {
        MultiByteToWideChar(CP_UTF8, 0, t, -1, wide, nw);
        SetWindowTextW(w->ed[0], wide);
        SendMessageW(w->ed[0], EM_SETSEL, (WPARAM)(nw > 0 ? nw - 1 : 0), (LPARAM)(nw > 0 ? nw - 1 : 0));
        SendMessageW(w->ed[0], EM_SCROLLCARET, 0, 0);
        free(wide);
    }
    free(t);
}

/* copy_utf puts UTF-8 text on the Windows clipboard as Unicode text. */
static void copy_utf(const char *s) {
    int nw;
    HGLOBAL mem;
    wchar_t *w;
    if (!s) return;
    nw = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
    if (nw <= 0) return;
    mem = GlobalAlloc(GMEM_MOVEABLE, (SIZE_T)nw * sizeof(wchar_t));
    if (!mem) return;
    w = (wchar_t *)GlobalLock(mem);
    if (!w) {
        GlobalFree(mem);
        return;
    }
    MultiByteToWideChar(CP_UTF8, 0, s, -1, w, nw);
    GlobalUnlock(mem);
    if (!OpenClipboard(NULL)) {
        GlobalFree(mem);
        return;
    }
    EmptyClipboard();
    if (!SetClipboardData(CF_UNICODETEXT, mem)) GlobalFree(mem);
    CloseClipboard();
}

/* layout_status draws the status block shared by the main window and the
   tray flyout: the connection name, phase and uptime, traffic, the connect
   button, the hold and service notices, the connection picker row, the info
   rows (external IP, overlay address, front, route, SOCKS) and, in multi
   mode, one row per other session. Moves *y down past the block. */
static void layout_status(int x, int *y, int w, int buttons_footer) {
    Sess *s = session_primary();
    char line[180], rate[80], up[32], socks[64];
    int ph = session_phase();
    int i;
    (void)buttons_footer;
    snprintf(line, sizeof line, "%s", (s && s->profile >= 0) ? pname(s->profile) : (g.active >= 0 ? pname(g.active) : loft_t("pop_off")));
    ui_text(x, *y, w, sc(22), line, C_SNOW, g.f_semi, DT_LEFT | DT_VCENTER | DT_SINGLELINE | DT_END_ELLIPSIS);
    *y += sc(24);
    fmt_up(up, (int)sizeof up, s);
    snprintf(line, sizeof line, "%s%s%s", phase_name(ph), up[0] ? "  " : "", up);
    ui_text(x, *y, w, sc(18), line, (ph == PH_ERR || ph == PH_HELD) ? C_ERR : (ph == PH_ON ? C_OK : C_STONE), g.f_small, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
    *y += sc(20);
    if (s && (s->phase == PH_ON || s->phase == PH_CONNECTING)) {
        fmt_rate(rate, (int)sizeof rate, s);
        ui_text(x, *y, w, sc(16), rate, C_STONE, g.f_small, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        *y += sc(18);
    }
    ui_button(ID_CONNECT, x, *y, w, sc(32), action_name(), 1);
    *y += sc(40);
    /* "Reset routes" is offered while hold keeps the network closed. */
    if (ph == PH_HELD || g.held) {
        ui_button(ID_RESET, x, *y, w, sc(28), loft_t("route.reset"), 0);
        *y += sc(32);
        ui_text(x, *y, w, sc(32), loft_t("held.note"), C_ERR, g.f_small, DT_LEFT | DT_WORDBREAK);
        *y += sc(34);
    }
#ifdef LOFT_STORE
    if (g.active >= 0 && g.active < g.nprof && g.prof[g.active].tun) {
        ui_text(x, *y, w, sc(48), loft_t("store.tun"), C_STONE, g.f_small, DT_LEFT | DT_WORDBREAK);
        *y += sc(50);
    }
#else
    if (g.need_svc) {
        ui_text(x, *y, w, sc(36), loft_t("svc.need"), C_STONE, g.f_small, DT_LEFT | DT_WORDBREAK);
        *y += sc(38);
        ui_button(ID_INSTALL, x, *y, w, sc(28), loft_t("svc.install"), 0);
        *y += sc(34);
    }
#endif
    /* Info rows; simple mode hides the technical ones. */
    snprintf(line, sizeof line, "%s    %s  >", loft_t("conn"), g.active >= 0 ? pname(g.active) : "");
    ui_button(ID_CONN_ROW, x, *y, w, sc(26), line, 3);
    *y += sc(28);
    if (g.prefs.ext_ip) {
        const char *v = "";
        if (s && s->ext_ip[0]) v = s->ext_ip;
        else if (s && s->ext_busy) v = "...";
        row_kv(x, y, w, "ext_ip_lab", v);
    }
    if (!g.prefs.simple) {
        row_kv(x, y, w, "overlay", (s && s->overlay[0]) ? s->overlay : "");
        row_kv(x, y, w, "front", (s && s->front[0]) ? s->front : "");
        row_kv(x, y, w, "route", loft_t(g.prefs.defroute ? "full" : (g.prefs.split ? "split" : "off")));
    }
    snprintf(socks, sizeof socks, "127.0.0.1:%d", g.prefs.socks_port > 0 ? g.prefs.socks_port : 1080);
    row_kv(x, y, w, "socks", socks);
    /* Other sessions get clickable rows with ids 100 + slot (see ui_command). */
    if (g.prefs.multi) {
        for (i = 0; i < LOFT_SESS; i++) {
            if (!g.sess[i].used || &g.sess[i] == s) continue;
            snprintf(line, sizeof line, "%s   %s", pname(g.sess[i].profile), phase_name(g.sess[i].phase));
            ui_button(100 + i, x, *y, w, sc(24), line, 3);
            *y += sc(26);
        }
    }
}

/* layout_fly draws the tray flyout: the status block and an
   Open / Settings / Quit footer. */
static void layout_fly(int ww) {
    int x = sc(14);
    int y = sc(10);
    int w = ww - 2 * x;
    int tw;
    layout_status(x, &y, w, 0);
    y += sc(6);
    ui_line(x, y, w);
    y += sc(8);
    tw = w / 3;
    ui_button(ID_OPEN, x, y, tw, sc(26), loft_t("open"), 2);
    ui_button(ID_SETTINGS, x + tw, y, tw, sc(26), loft_t("settings"), 2);
    ui_button(ID_QUIT, x + 2 * tw, y, tw, sc(26), loft_t("quit"), 2);
}

/* layout_menu draws the tray right-click menu as a custom popup. */
static void layout_menu(int ww) {
    int x = sc(8), y = sc(8), w = ww - sc(16);
    char lab[200];
    const char *nm = g.active >= 0 ? pname(g.active) : "";
    Sess *s = session_primary();
    if (s && s->profile >= 0) nm = pname(s->profile);
    snprintf(lab, sizeof lab, "%s   %s", action_name(), nm);
    ui_button(ID_CONNECT, x, y, w, sc(28), lab, 3);
    y += sc(30);
    snprintf(lab, sizeof lab, "%s  >", loft_t("conns"));
    ui_button(ID_CONN_ROW, x, y, w, sc(28), lab, 3);
    y += sc(32);
    ui_line(x, y, w);
    y += sc(6);
    ui_button(ID_OPEN, x, y, w, sc(26), loft_t("open"), 3);
    y += sc(28);
    ui_button(ID_SETTINGS, x, y, w, sc(26), loft_t("settings"), 3);
    y += sc(28);
    snprintf(lab, sizeof lab, "%s%s", g.prefs.split ? "\xE2\x9C\x93 " : "    ", loft_t("split"));
    ui_button(ID_SPLIT_GO, x, y, w, sc(26), lab, 3);
    y += sc(28);
    ui_button(ID_ROUTES, x, y, w, sc(26), loft_t("nav.split"), 3);
    y += sc(28);
    ui_button(ID_JOURNAL, x, y, w, sc(26), loft_t("journal"), 3);
    y += sc(32);
    ui_line(x, y, w);
    y += sc(6);
    snprintf(lab, sizeof lab, "%s  >", loft_t("menu_lang"));
    ui_button(ID_LANG, x, y, w, sc(26), lab, 3);
    y += sc(28);
    ui_button(ID_ABOUT, x, y, w, sc(26), loft_t("menu_about"), 3);
    y += sc(32);
    ui_line(x, y, w);
    y += sc(6);
    ui_button(ID_QUIT, x, y, w, sc(26), loft_t("menu_quit"), 3);
}

/* layout_list draws the connection picker popup: up to 12 profiles with a
   check mark on the active one and its live state, then Import, Edit and
   Add buttons. */
static void layout_list(int ww) {
    int x = sc(8), y = sc(8), w = ww - sc(16), i, n;
    n = g.nprof > 12 ? 12 : g.nprof;
    for (i = 0; i < n; i++) {
        char lab[180];
        const char *st = loft_t("conn.down");
        int j, on = 0;
        for (j = 0; j < LOFT_SESS; j++) {
            if (g.sess[j].used && g.sess[j].profile == i && (g.sess[j].phase == PH_ON || g.sess[j].phase == PH_CONNECTING)) {
                st = phase_name(g.sess[j].phase);
                on = g.sess[j].phase == PH_ON;
            }
        }
        snprintf(lab, sizeof lab, "%s%s", g.active == i ? "\xE2\x9C\x93 " : "   ", pname(i));
        ui_button(ID_PROF + i, x, y, w - sc(72), sc(24), lab, g.active == i ? 7 : 3);
        ui_text(x + w - sc(72), y, sc(72), sc(24), st, on ? C_OK : C_MUTE, g.f_small, DT_RIGHT | DT_VCENTER | DT_SINGLELINE | DT_END_ELLIPSIS);
        y += sc(26);
    }
    if (g.nprof == 0) {
        ui_text(x, y, w, sc(40), loft_t("import.body"), C_MUTE, g.f_small, DT_LEFT | DT_WORDBREAK);
        y += sc(44);
    }
    y += sc(4);
    ui_button(ID_IMPORT, x, y, w, sc(24), loft_t("import"), 2);
    y += sc(26);
    ui_button(ID_MENU, x, y, w, sc(24), loft_t("edit"), 2);
    y += sc(26);
    ui_button(ID_ADD, x, y, w, sc(24), loft_t("conn.add"), 2);
}

/* layout_lang draws the language picker popup. */
static void layout_lang(int ww) {
    int i, x = sc(8), y = sc(8), w = ww - sc(16);
    int cur = loft_lang_index(g.prefs.lang);
    for (i = 0; i < LOFT_LANGS; i++) {
        char lab[80];
        snprintf(lab, sizeof lab, "%s%s", i == cur ? "\xE2\x9C\x93 " : "   ", loft_lang_native(i));
        ui_button(ID_LANGI + i, x, y, w, sc(24), lab, i == cur ? 7 : 3);
        y += sc(26);
    }
}

/* layout_main draws the main window: title bar, status block, the profile
   list (up to 6, hidden in simple mode) with Import / Add, and a footer with
   Journal, Split routing and Settings. */
static void layout_main(HWND hwnd, Win *w, int ww, int hh) {
    int x = sc(16);
    int y = ui_bar(ww, 1) + sc(12);
    int bw = ww - 2 * x;
    int i, n;
    (void)hwnd;
    (void)w;
    (void)hh;
    layout_status(x, &y, bw, 0);
    if (!g.prefs.simple) {
        y += sc(8);
        ui_line(x, y, bw);
        y += sc(8);
        n = g.nprof > 6 ? 6 : g.nprof;
        for (i = 0; i < n; i++) {
            ui_button(ID_PROF + i, x, y, bw, sc(24), pname(i), g.active == i ? 7 : 3);
            y += sc(26);
        }
        ui_button(ID_IMPORT, x, y, bw / 2 - sc(4), sc(26), loft_t("import"), 2);
        ui_button(ID_ADD, x + bw / 2 + sc(4), y, bw / 2 - sc(4), sc(26), loft_t("conn.add"), 2);
        y += sc(34);
    }
    y += sc(6);
    ui_line(x, y, bw);
    y += sc(8);
    ui_button(ID_JOURNAL, x, y, bw / 3, sc(26), loft_t("journal"), 2);
    ui_button(ID_ROUTES, x + bw / 3, y, bw / 3, sc(26), loft_t("nav.split"), 2);
    ui_button(ID_SETTINGS, x + 2 * bw / 3, y, bw / 3, sc(26), loft_t("settings"), 2);
}

/* toggle_row draws a switch with title and subtitle and moves *y past it. */
static int toggle_row(int id, int x, int *y, int w, int on, const char *title, const char *sub) {
    ui_toggle(id, x, *y, w, on, title, sub);
    *y += sc(48);
    return id;
}

/* dock_label returns the label for the "taskbar button" preference:
   always, never, or only while a window is open. */
static const char *dock_label(void) {
    if (loft_streq(g.prefs.taskbar, "always")) return loft_t("dock_always");
    if (loft_streq(g.prefs.taskbar, "never")) return loft_t("dock_never");
    return loft_t("dock_window");
}

/* layout_settings draws the settings window with three tabs: general
   (start with Windows, taskbar, simple mode, units, language, updates,
   file associations), route (default route, failover, hold, split routing)
   and client (auto-connect, reconnect, multi mode, shared SOCKS and its
   port, external IP lookup). The two edit boxes belong to the client tab
   and are hidden on the others. */
static void layout_settings(HWND hwnd, Win *w, int ww, int hh) {
    int x = sc(16);
    int y = ui_bar(ww, 1) + sc(8);
    int bw = ww - 2 * x;
    int tw = bw / 3;
    (void)hwnd;
    (void)hh;
    ui_button(ID_TAB0, x, y, tw, sc(28), loft_t("set_gen"), w->tab == 0 ? 1 : 0);
    ui_button(ID_TAB1, x + tw, y, tw, sc(28), loft_t("route"), w->tab == 1 ? 1 : 0);
    ui_button(ID_TAB2, x + 2 * tw, y, tw, sc(28), loft_t("set_client"), w->tab == 2 ? 1 : 0);
    y += sc(40);
    if (w->tab == 0) {
#ifndef LOFT_STORE
        toggle_row(ID_LOGIN, x, &y, bw, g.prefs.login, loft_t("login"), loft_t("login_d"));
#endif
        ui_text(x, y, bw / 2, sc(28), loft_t("dock"), C_SNOW, g.f_ui, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        ui_button(ID_DOCK, x + bw / 2, y, bw / 2, sc(28), dock_label(), 0);
        y += sc(30);
        ui_text(x, y, bw, sc(16), loft_t("dock_d"), C_MUTE, g.f_small, DT_LEFT | DT_SINGLELINE);
        y += sc(22);
        toggle_row(ID_SIMPLE, x, &y, bw, g.prefs.simple, loft_t("simple"), loft_t("simple_d"));
        ui_text(x, y, bw / 2, sc(28), loft_t("rate_unit"), C_SNOW, g.f_ui, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        ui_button(ID_RATE, x + bw / 2, y, bw / 2, sc(28), loft_t(g.prefs.rate_bits ? "u_kbit" : "u_kb"), 0);
        y += sc(34);
        ui_text(x, y, bw / 2, sc(28), loft_t("lang"), C_SNOW, g.f_ui, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        ui_button(ID_LANG, x + bw / 2, y, bw / 2, sc(28), loft_lang_native(loft_lang_index(g.prefs.lang) < 0 ? 5 : loft_lang_index(g.prefs.lang)), 0);
        y += sc(36);
#ifndef LOFT_STORE
        toggle_row(ID_UPD, x, &y, bw, g.prefs.update_check, loft_t("pref.upd"), loft_t("pref.upd.d"));
#endif
        toggle_row(ID_DIAG, x, &y, bw, g.prefs.diag, loft_t("diag"), loft_t("diag_d"));
#ifndef LOFT_STORE
        toggle_row(ID_ASSOC_F, x, &y, bw, g.prefs.assoc_file, loft_t("assoc_file"), loft_t("assoc_file_d"));
        toggle_row(ID_ASSOC_L, x, &y, bw, g.prefs.assoc_link, loft_t("assoc_link"), loft_t("assoc_link_d"));
#endif
        move_ed(w->ed[0], 0, 0, 1, 1, 0);
        move_ed(w->ed[1], 0, 0, 1, 1, 0);
    } else if (w->tab == 1) {
        toggle_row(ID_DEFROUTE, x, &y, bw, g.prefs.defroute, loft_t("default_route"), loft_t("default_route_d"));
        toggle_row(ID_FAILOVER, x, &y, bw, g.prefs.failover, loft_t("failover"), loft_t("failover_d"));
        toggle_row(ID_HOLD, x, &y, bw, g.prefs.hold, loft_t("hold"), loft_t("hold_d"));
        toggle_row(ID_SPLIT_GO, x, &y, bw, g.prefs.split, loft_t("split"), loft_t("split_d"));
        ui_button(ID_ROUTES, x, y, bw, sc(28), loft_t("nav.split"), 0);
        y += sc(36);
        move_ed(w->ed[0], 0, 0, 1, 1, 0);
        move_ed(w->ed[1], 0, 0, 1, 1, 0);
    } else {
        char portlab[64];
        toggle_row(ID_AUTO, x, &y, bw, g.prefs.auto_connect, loft_t("auto"), loft_t("auto_d"));
        toggle_row(ID_RECONN, x, &y, bw, g.prefs.reconnect, loft_t("reconnect"), loft_t("reconnect_d"));
        toggle_row(ID_MULTI, x, &y, bw, g.prefs.multi, loft_t("multi"), loft_t("multi_d"));
        toggle_row(ID_SOCKSHR, x, &y, bw, g.prefs.socks_share, loft_t("socks_share"), loft_t("socks_share_d"));
        snprintf(portlab, sizeof portlab, "%s", loft_t("socks_port"));
        field(w->ed[0], x, &y, bw, portlab);
        toggle_row(ID_EXTIP, x, &y, bw, g.prefs.ext_ip, loft_t("ext_ip"), loft_t("ext_ip_d"));
        field(w->ed[1], x, &y, bw, loft_t("ext_ip_url"));
    }
    y += sc(8);
    ui_line(x, y, bw);
    y += sc(8);
    ui_button(ID_DEFAULTS, x, y, bw / 3, sc(26), loft_t("defaults"), 2);
    ui_button(ID_JOURNAL, x + bw / 3, y, bw / 3, sc(26), loft_t("journal"), 2);
    ui_button(ID_ABOUT, x + 2 * bw / 3, y, bw / 3, sc(26), loft_t("about"), 2);
}

/* layout_edit draws the profile editor. Window state: extra[0] is the
   profile index (-1 for a new one), extra[1] the index into kinds, extra[2]
   the TUN switch. Edit boxes 0-7 hold name, URL, path, key, SOCKS, DNS,
   SNI and user. */
static void layout_edit(HWND hwnd, Win *w, int ww, int hh) {
    int x = sc(16);
    int y = ui_bar(ww, 1) + sc(8);
    int bw = ww - 2 * x;
    int ki = w->extra[1];
    (void)hwnd;
    (void)hh;
    if (ki < 0 || ki > 8) ki = 0;
    field(w->ed[0], x, &y, bw, loft_t("name"));
    field(w->ed[1], x, &y, bw, loft_t("url"));
    field(w->ed[2], x, &y, bw, loft_t("path"));
    field(w->ed[3], x, &y, bw, loft_t("key"));
    ui_text(x, y, bw / 2, sc(28), loft_t("proto"), C_SNOW, g.f_ui, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
    ui_button(ID_KIND, x + bw / 2, y, bw / 2, sc(28), kinds[ki], 0);
    y += sc(34);
#ifdef LOFT_STORE
    w->extra[2] = 0;
    ui_text(x, y, bw, sc(48), loft_t("store.tun"), C_STONE, g.f_small, DT_LEFT | DT_WORDBREAK);
#else
    ui_toggle(ID_TUN, x, y, bw, w->extra[2] ? 1 : 0, loft_t("edit.tun"), loft_t("tun"));
#endif
    y += sc(48);
    field(w->ed[4], x, &y, bw, loft_t("socks"));
    field(w->ed[5], x, &y, bw, loft_t("edit.sstp.dns"));
    field(w->ed[6], x, &y, bw, loft_t("edit.vless.sni"));
    field(w->ed[7], x, &y, bw, loft_t("sstp_user"));
    ui_button(ID_SAVE, x, y, bw / 2 - sc(4), sc(30), loft_t("save"), 1);
    ui_button(ID_DELETE, x + bw / 2 + sc(4), y, bw / 2 - sc(4), sc(30), loft_t("delete"), 0);
    y += sc(36);
    ui_button(ID_EXPORT, x, y, bw, sc(26), loft_t("export"), 2);
}

/* layout_first draws the first-run window: name, URL and key fields, an
   Import button and Continue. */
static void layout_first(HWND hwnd, Win *w, int ww, int hh) {
    int x = sc(20);
    int y = ui_bar(ww, 1) + sc(16);
    int bw = ww - 2 * x;
    (void)hwnd;
    (void)hh;
    ui_text(x, y, bw, sc(24), loft_t("key.title"), C_SNOW, g.f_semi, DT_LEFT | DT_SINGLELINE);
    y += sc(28);
    ui_text(x, y, bw, sc(40), loft_t("key.body"), C_STONE, g.f_ui, DT_LEFT | DT_WORDBREAK);
    y += sc(44);
    field(w->ed[0], x, &y, bw, loft_t("name"));
    field(w->ed[1], x, &y, bw, loft_t("url"));
    field(w->ed[2], x, &y, bw, loft_t("key"));
    ui_text(x, y, bw, sc(32), loft_t("key.hint"), C_MUTE, g.f_small, DT_LEFT | DT_WORDBREAK);
    y += sc(36);
    ui_button(ID_IMPORT, x, y, bw, sc(28), loft_t("import.pick"), 0);
    y += sc(36);
    ui_button(ID_CONT, x, y, bw, sc(32), loft_t("continue"), 1);
}

/* layout_about draws the About window: name, version, supported protocols,
   the blurb, and a button that checks for updates (or downloads one already
   found). */
static void layout_about(int ww) {
    int x = sc(20);
    int y = ui_bar(ww, 1) + sc(16);
    int bw = ww - 2 * x;
    char blurb[400];
    char ver[64];
    nobr(loft_t("about.blurb"), blurb, (int)sizeof blurb);
    ui_text(x, y, bw, sc(28), "loft", C_LAMP, g.f_semi, DT_CENTER | DT_SINGLELINE);
    y += sc(30);
    snprintf(ver, sizeof ver, "%s", LOFT_VERSION);
    ui_text(x, y, bw, sc(18), ver, C_STONE, g.f_small, DT_CENTER | DT_SINGLELINE);
    y += sc(28);
    ui_text(x, y, bw, sc(16), loft_t("about.loft"), C_SNOW, g.f_small, DT_CENTER | DT_SINGLELINE | DT_END_ELLIPSIS);
    y += sc(18);
    ui_text(x, y, bw, sc(16), loft_t("about.sstp"), C_STONE, g.f_small, DT_CENTER | DT_SINGLELINE | DT_END_ELLIPSIS);
    y += sc(18);
    ui_text(x, y, bw, sc(16), loft_t("about.vless"), C_STONE, g.f_small, DT_CENTER | DT_SINGLELINE | DT_END_ELLIPSIS);
    y += sc(18);
    ui_text(x, y, bw, sc(16), loft_t("about.trojan"), C_STONE, g.f_small, DT_CENTER | DT_SINGLELINE | DT_END_ELLIPSIS);
    y += sc(28);
    ui_text(x, y, bw, sc(48), blurb, C_PAPER, g.f_ui, DT_CENTER | DT_WORDBREAK);
    y += sc(56);
    ui_button(ID_DOWNLOAD, x, y, bw, sc(30), g.update_ver[0] ? g.update_ver : loft_t("pref.upd"), g.update_ver[0] ? 1 : 0);
    y += sc(48);
    ui_text(x, y, bw, sc(20), loft_t("about.sig"), C_MUTE, g.f_small, DT_CENTER | DT_SINGLELINE);
}

/* layout_update draws the "new version available" offer with Download,
   Later and "don't check again" buttons. */
static void layout_update(int ww) {
    int x = sc(16);
    int y = ui_bar(ww, 1) + sc(12);
    int bw = ww - 2 * x;
    ui_text(x, y, bw, sc(22), loft_t("upd.offer.title"), C_SNOW, g.f_semi, DT_LEFT | DT_SINGLELINE);
    y += sc(26);
    ui_text(x, y, bw, sc(20), g.update_ver, C_LAMP, g.f_ui, DT_LEFT | DT_SINGLELINE);
    y += sc(24);
    ui_text(x, y, bw, sc(40), loft_t("upd.offer.body"), C_STONE, g.f_small, DT_LEFT | DT_WORDBREAK);
    y += sc(48);
    ui_button(ID_DOWNLOAD, x, y, bw, sc(30), loft_t("upd.offer.body"), 1);
    y += sc(36);
    ui_button(ID_LATER, x, y, bw, sc(26), loft_t("upd.later"), 2);
    y += sc(28);
    ui_button(ID_QUIET, x, y, bw, sc(26), loft_t("upd.quiet"), 2);
}

/* layout_journal draws the journal window: a read-only multi-line edit box
   that fills the window, with Copy and Clear buttons below. */
static void layout_journal(HWND hwnd, Win *w, int ww, int hh) {
    int y = ui_bar(ww, 1);
    int x = sc(12);
    int eh;
    (void)hwnd;
    eh = hh - y - sc(48);
    if (eh < sc(40)) eh = sc(40);
    ui_fill(x, y + sc(8), ww - 2 * x, eh, NULL);
    move_ed(w->ed[0], x, y + sc(8), ww - 2 * x, eh, 1);
    ui_button(ID_EXPORT, x, hh - sc(36), sc(110), sc(26), loft_t("copy"), 2);
    ui_button(ID_DELETE, x + sc(118), hh - sc(36), sc(110), sc(26), loft_t("clear_log"), 2);
}

/* layout_routes draws the split-routing editor. A zone is a named group of
   address sets with one default direction (through the tunnel or direct);
   a set is one named CIDR. The window shows the zone tabs, the direction
   button, a "pull" button when the zone has a repository URL, the zone's
   sets (up to 8, ids 520 + set index), and fields to add a set. Window
   state: extra[0] is the selected zone, extra[1] the selected set. */
static void layout_routes(HWND hwnd, Win *w, int ww, int hh) {
    int x = sc(12);
    int y = ui_bar(ww, 1) + sc(8);
    int bw = ww - 2 * x;
    int zi = w->extra[0];
    int i, shown = 0;
    (void)hwnd;
    (void)hh;
    if (zi < 0 || zi >= g.routes.nz) zi = 0;
    w->extra[0] = zi;
    for (i = 0; i < g.routes.nz && i < 8; i++) {
        ui_button(ID_ZONE + i, x, y, bw, sc(24), g.routes.zones[i].name[0] ? g.routes.zones[i].name : g.routes.zones[i].id, i == zi ? 1 : 0);
        y += sc(26);
    }
    if (g.routes.nz > 0) {
        ui_button(ID_DIR, x, y, bw, sc(26), loft_t(g.routes.zones[zi].def_via ? "full" : "split.bypass"), 0);
        y += sc(30);
        if (g.routes.zones[zi].repo[0]) {
            ui_text(x, y, bw, sc(16), g.routes.zones[zi].repo, C_MUTE, g.f_small, DT_LEFT | DT_SINGLELINE | DT_END_ELLIPSIS);
            y += sc(18);
            ui_button(ID_PULL, x, y, bw, sc(24), loft_t("split.repo.pull"), 2);
            y += sc(28);
        }
    }
    for (i = 0; g.routes.nz > 0 && i < g.routes.ns && shown < 8; i++) {
        char lab[160];
        if (!loft_streq(g.routes.sets[i].zone, g.routes.zones[zi].id)) continue;
        snprintf(lab, sizeof lab, "%s  %s", g.routes.sets[i].name, g.routes.sets[i].cidr);
        ui_button(520 + i, x, y, bw, sc(22), lab, w->extra[1] == i ? 7 : 3);
        y += sc(24);
        shown++;
    }
    y += sc(6);
    field(w->ed[0], x, &y, bw, loft_t("split.add.name"));
    field(w->ed[1], x, &y, bw, loft_t("split.add.net"));
    ui_button(ID_ADD, x, y, bw / 2 - sc(4), sc(28), loft_t("split.add.go"), 1);
    ui_button(ID_DELETE, x + bw / 2 + sc(4), y, bw / 2 - sc(4), sc(28), loft_t("split.del"), 0);
    y += sc(34);
    ui_button(ID_BAT, x, y, bw, sc(26), loft_t("split.import.title"), 2);
}

/* ui_create makes the native edit boxes a window kind needs, right after
   chrome.c creates the window. Ids start at 710. The settings window is
   filled with the current SOCKS port and external IP URL. */
void ui_create(HWND hwnd, Win *w) {
    int i;
    if (!w) return;
    if (w->kind == WK_EDIT) {
        for (i = 0; i < 8; i++) w->ed[i] = make_edit(hwnd, 710 + i, i == 3);
    } else if (w->kind == WK_FIRST) {
        for (i = 0; i < 3; i++) w->ed[i] = make_edit(hwnd, 710 + i, i == 2);
    } else if (w->kind == WK_SET) {
        char port[16];
        w->ed[0] = make_edit(hwnd, 710, 0);
        w->ed[1] = make_edit(hwnd, 711, 0);
        snprintf(port, sizeof port, "%d", g.prefs.socks_port > 0 ? g.prefs.socks_port : 1080);
        ed_set(w->ed[0], port);
        ed_set(w->ed[1], g.prefs.ext_ip_url);
    } else if (w->kind == WK_JOURNAL) {
        w->ed[0] = CreateWindowExW(0, L"EDIT", L"", WS_CHILD | ES_MULTILINE | ES_READONLY | ES_AUTOVSCROLL | WS_VSCROLL, 0, 0, 0, 0, hwnd, (HMENU)(INT_PTR)710, g.inst, NULL);
        if (w->ed[0]) SendMessageW(w->ed[0], WM_SETFONT, (WPARAM)g.f_mono, TRUE);
    } else if (w->kind == WK_ROUTES) {
        w->ed[0] = make_edit(hwnd, 710, 0);
        w->ed[1] = make_edit(hwnd, 711, 0);
        w->extra[0] = 0;
        w->extra[1] = -1;
    }
}

/* ui_layout runs the layout function for the window's kind. The same call
   paints, hit-tests or measures, depending on the mode set in draw.c. */
void ui_layout(HWND hwnd, Win *w, int ww, int hh) {
    if (!w || ww <= 0) return;
    switch (w->kind) {
    case WK_MAIN: layout_main(hwnd, w, ww, hh); break;
    case WK_FLY: layout_fly(ww); break;
    case WK_LIST: layout_list(ww); break;
    case WK_MENU: layout_menu(ww); break;
    case WK_LANG: layout_lang(ww); break;
    case WK_SET: layout_settings(hwnd, w, ww, hh); break;
    case WK_EDIT: layout_edit(hwnd, w, ww, hh); break;
    case WK_ABOUT: layout_about(ww); break;
    case WK_JOURNAL: layout_journal(hwnd, w, ww, hh); break;
    case WK_ROUTES: layout_routes(hwnd, w, ww, hh); break;
    case WK_UPDATE: layout_update(ww); break;
    case WK_FIRST: layout_first(hwnd, w, ww, hh); break;
    default: break;
    }
}

/* ui_measure runs the layout in measure mode and returns the content
   height plus a margin, so popups can be sized to fit. */
int ui_measure(Win *w, int ww) {
    int b;
    draw_set_mode(2);
    ui_layout(NULL, w, ww, 800);
    b = draw_bottom();
    draw_set_mode(0);
    return b + sc(10);
}

/* ui_click runs the layout in hit mode and returns the id of the control
   under (x, y), or 0. */
int ui_click(HWND hwnd, Win *w, int x, int y, int ww, int hh) {
    int id;
    hit_begin(x, y);
    ui_layout(hwnd, w, ww, hh);
    id = hit_id();
    draw_set_mode(0);
    return id;
}

/* ui_capture saves the settings window's text fields (SOCKS port and
   external IP URL) into prefs. Called when a field loses focus and before
   any settings command, so typed values are not lost. Invalid ports and an
   empty URL are ignored. */
void ui_capture(Win *w) {
    char b[64], u[LOFT_URL];
    int p;
    if (!w || w->kind != WK_SET) return;
    ed_get(w->ed[0], b, (int)sizeof b);
    p = atoi(b);
    if (p > 0 && p < 65536) g.prefs.socks_port = p;
    ed_get(w->ed[1], u, (int)sizeof u);
    if (u[0]) loft_strlcpy(g.prefs.ext_ip_url, sizeof g.prefs.ext_ip_url, u);
    store_save_prefs();
}

/* cycle_dock steps the taskbar preference always -> window -> never and
   applies it. */
static void cycle_dock(void) {
    if (loft_streq(g.prefs.taskbar, "always")) loft_strlcpy(g.prefs.taskbar, sizeof g.prefs.taskbar, "window");
    else if (loft_streq(g.prefs.taskbar, "window")) loft_strlcpy(g.prefs.taskbar, sizeof g.prefs.taskbar, "never");
    else loft_strlcpy(g.prefs.taskbar, sizeof g.prefs.taskbar, "always");
    store_save_prefs();
    chrome_taskbar();
}

/* do_connect is the main button: it disconnects when something is up or
   connecting, otherwise connects the active profile (or the first one), or
   opens the editor when there are no profiles. */
static void do_connect(void) {
    int ph = session_phase();
    if (ph == PH_ON || ph == PH_CONNECTING) {
        session_disconnect_active();
        return;
    }
    if (g.active < 0 && g.nprof > 0) g.active = 0;
    if (g.active >= 0) session_connect(g.active);
    else open_edit(-1);
}

/* drop_profile deletes profile i after a confirmation. Its sessions are
   stopped and detached, and sessions of later profiles have their index
   shifted down to match the shorter list. */
static void drop_profile(HWND owner, int i) {
    wchar_t msg[256], title[64];
    int s;
    if (i < 0 || i >= g.nprof) return;
    widen(loft_t("delete"), title, 64);
    widen(pname(i), msg, 256);
    if (MessageBoxW(owner, msg, title, MB_OKCANCEL | MB_ICONWARNING) != IDOK) return;
    for (s = 0; s < LOFT_SESS; s++) {
        if (!g.sess[s].used) continue;
        if (g.sess[s].profile == i) {
            session_disconnect_slot(s);
            g.sess[s].profile = -1;
        } else if (g.sess[s].profile > i) g.sess[s].profile--;
    }
    store_delete_profile(i);
    redraw_all();
}

/* save_edit stores the profile editor. A new profile needs a URL and a key
   and is added through store_add_parsed. For an existing profile the fields
   are copied over; the key box is shown empty, so a key is only replaced
   when the user typed a new one. An empty SOCKS field means "auto". */
static void save_edit(Win *w) {
    int idx = w->extra[0];
    int ki = w->extra[1];
    char key[LOFT_KEY];
    if (ki < 0 || ki > 8) ki = 0;
    if (idx < 0) {
        LoftParsed ps;
        memset(&ps, 0, sizeof ps);
        loft_profile_clear(&ps.p);
        ed_get(w->ed[0], ps.p.name, (int)sizeof ps.p.name);
        ed_get(w->ed[1], ps.p.url, (int)sizeof ps.p.url);
        ed_get(w->ed[2], ps.p.path, (int)sizeof ps.p.path);
        ed_get(w->ed[3], ps.key, (int)sizeof ps.key);
        ed_get(w->ed[4], ps.p.socks, (int)sizeof ps.p.socks);
        ed_get(w->ed[5], ps.p.dns, (int)sizeof ps.p.dns);
        ed_get(w->ed[6], ps.p.sni, (int)sizeof ps.p.sni);
        ed_get(w->ed[7], ps.p.user, (int)sizeof ps.p.user);
        if (!ps.p.socks[0]) loft_strlcpy(ps.p.socks, sizeof ps.p.socks, "auto");
        loft_strlcpy(ps.p.kind, sizeof ps.p.kind, kinds[ki]);
        ps.p.tun = w->extra[2] ? 1 : 0;
        if (!ps.p.name[0]) loft_strlcpy(ps.p.name, sizeof ps.p.name, "loft");
        if (!ps.p.url[0] || !ps.key[0]) {
            journal_add(loft_t("need_key"));
            loft_wipe(ps.key, sizeof ps.key);
            return;
        }
        idx = store_add_parsed(&ps);
        loft_wipe(ps.key, sizeof ps.key);
        if (idx >= 0) w->extra[0] = idx;
        return;
    }
    if (idx >= g.nprof) return;
    ed_get(w->ed[0], g.prof[idx].name, (int)sizeof g.prof[idx].name);
    ed_get(w->ed[1], g.prof[idx].url, (int)sizeof g.prof[idx].url);
    ed_get(w->ed[2], g.prof[idx].path, (int)sizeof g.prof[idx].path);
    ed_get(w->ed[4], g.prof[idx].socks, (int)sizeof g.prof[idx].socks);
    ed_get(w->ed[5], g.prof[idx].dns, (int)sizeof g.prof[idx].dns);
    ed_get(w->ed[6], g.prof[idx].sni, (int)sizeof g.prof[idx].sni);
    ed_get(w->ed[7], g.prof[idx].user, (int)sizeof g.prof[idx].user);
    if (!g.prof[idx].socks[0]) loft_strlcpy(g.prof[idx].socks, sizeof g.prof[idx].socks, "auto");
    loft_strlcpy(g.prof[idx].kind, sizeof g.prof[idx].kind, kinds[ki]);
    g.prof[idx].tun = w->extra[2] ? 1 : 0;
    ed_get(w->ed[3], key, (int)sizeof key);
    if (key[0]) {
        key_save(g.prof[idx].id, key);
        loft_strlcpy(g.key[idx], LOFT_KEY, key);
        g.prof[idx].has_key = 1;
        loft_wipe(key, sizeof key);
        ed_set(w->ed[3], "");
    }
    store_save_profile(idx);
}

/* first_continue finishes the first-run window: when URL and key are
   filled in it adds the profile, then marks first run done and opens the
   main window. */
static void first_continue(HWND hwnd, Win *w) {
    char name[LOFT_NAME], url[LOFT_URL], key[LOFT_KEY];
    ed_get(w->ed[0], name, (int)sizeof name);
    ed_get(w->ed[1], url, (int)sizeof url);
    ed_get(w->ed[2], key, (int)sizeof key);
    if (key[0] && url[0]) {
        LoftParsed ps;
        memset(&ps, 0, sizeof ps);
        loft_profile_clear(&ps.p);
        loft_strlcpy(ps.p.name, sizeof ps.p.name, name[0] ? name : "loft");
        loft_strlcpy(ps.p.url, sizeof ps.p.url, url);
        loft_strlcpy(ps.key, sizeof ps.key, key);
        store_add_parsed(&ps);
        loft_wipe(ps.key, sizeof ps.key);
    }
    loft_wipe(key, sizeof key);
    g.prefs.first_done = 1;
    store_save_prefs();
    chrome_hide(hwnd);
    open_main();
}

/* delete_set removes the selected address set from the split lists, saves
   them and re-applies routes. */
static void delete_set(Win *w) {
    int idx = w->extra[1];
    int i;
    if (idx < 0 || idx >= g.routes.ns) return;
    for (i = idx; i < g.routes.ns - 1; i++) g.routes.sets[i] = g.routes.sets[i + 1];
    g.routes.ns--;
    memset(&g.routes.sets[g.routes.ns], 0, sizeof g.routes.sets[0]);
    w->extra[1] = -1;
    store_save_routes();
    session_apply_split();
}

/* Pull is the job for pull_worker: which zone to refresh and from what URL. */
typedef struct Pull {
    int zone;
    char url[512];
} Pull;

/* pull_worker downloads a zone's address list from its repository URL on a
   background thread and posts the body (NULL on failure) back as
   WM_LOFT_PULL. */
static DWORD WINAPI pull_worker(LPVOID arg) {
    Pull *p = (Pull *)arg;
    char *body = NULL;
    if (!http_get(p->url, &body, 20000)) {
        free(body);
        body = NULL;
    }
    if (!PostMessageW(g.msg, WM_LOFT_PULL, (WPARAM)p->zone, (LPARAM)body)) free(body);
    free(p);
    return 0;
}

/* routes_pulled receives a pulled list on the UI thread and imports it into
   the zone. The list is a .bat file of "route ADD ... MASK ..." lines; it
   replaces the zone's earlier repository entries. Then the lists are saved,
   routes re-applied and the import result (1 or 0) logged. Frees body. */
void routes_pulled(int zone, char *body) {
    int n = 0;
    char line[64];
    if (body && zone >= 0 && zone < g.routes.nz) {
        n = loft_routes_import_bat(&g.routes, g.routes.zones[zone].id, body);
        store_save_routes();
        session_apply_split();
    } else journal_add(loft_t("err_no_reply"));
    free(body);
    snprintf(line, sizeof line, "routes %d", n);
    journal_add(line);
}

/* import_bat imports a .bat file of "route ADD" commands into the selected
   zone, replacing its earlier repository entries, then saves and re-applies
   routes. */
static void import_bat(HWND owner, Win *w) {
    wchar_t file[MAX_PATH];
    OPENFILENAMEW ofn;
    char *text;
    int zi;
    file[0] = 0;
    memset(&ofn, 0, sizeof ofn);
    ofn.lStructSize = sizeof ofn;
    ofn.hwndOwner = owner;
    ofn.lpstrFilter = L"bat\0*.bat\0All\0*.*\0";
    ofn.lpstrFile = file;
    ofn.nMaxFile = MAX_PATH;
    ofn.Flags = OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST;
    if (!GetOpenFileNameW(&ofn)) return;
    text = file_read(file);
    if (!text) return;
    zi = w->extra[0];
    if (zi < 0 || zi >= g.routes.nz) zi = 0;
    loft_routes_import_bat(&g.routes, g.routes.zones[zi].id, text);
    free(text);
    store_save_routes();
    session_apply_split();
    redraw_all();
}

/* defaults_apply resets prefs to defaults but keeps the language and the
   active profile and leaves first run marked as done, then re-applies the settings that
   touch Windows (file associations, start with Windows, taskbar). */
static void defaults_apply(void) {
    char lang[8], active[LOFT_ID];
    loft_strlcpy(lang, sizeof lang, g.prefs.lang);
    loft_strlcpy(active, sizeof active, g.prefs.active);
    loft_prefs_default(&g.prefs);
    loft_strlcpy(g.prefs.lang, sizeof g.prefs.lang, lang[0] ? lang : "ru");
    loft_strlcpy(g.prefs.active, sizeof g.prefs.active, active);
    g.prefs.first_done = 1;
    store_save_prefs();
    assoc_apply();
    login_apply();
    chrome_taskbar();
    loft_l10n_set(g.prefs.lang);
}

/* ui_command performs the action of a clicked control in any window.
   Ranged ids are handled first (session rows, profiles, languages, zones,
   address sets), then the fixed ID_* commands. Most toggles flip one pref
   and save it; ones that change routing call session_apply_split. Ends by
   repainting the window and the tray icon. */
void ui_command(HWND hwnd, Win *w, int id) {
    if (!w) return;
    if (w->kind == WK_SET) ui_capture(w);
    /* A session row in multi mode: retry a failed one, stop any other. */
    if (id >= 100 && id < 100 + LOFT_SESS) {
        int slot = id - 100;
        if (g.sess[slot].phase == PH_ERR && g.sess[slot].profile >= 0) session_connect(g.sess[slot].profile);
        else session_disconnect_slot(slot);
        return;
    }
    if (id >= ID_PROF && id < ID_PROF + LOFT_PROFILES) {
        g.active = id - ID_PROF;
        store_save_active();
        session_connect(g.active);
        pop_close();
        return;
    }
    if (id >= ID_LANGI && id < ID_LANGI + LOFT_LANGS) {
        loft_strlcpy(g.prefs.lang, sizeof g.prefs.lang, loft_lang_code(id - ID_LANGI));
        loft_l10n_set(g.prefs.lang);
        store_save_prefs();
        pop_close();
        redraw_all();
        return;
    }
    if (id >= ID_ZONE && id < ID_ZONE + LOFT_ZONES) {
        w->extra[0] = id - ID_ZONE;
        w->extra[1] = -1;
        InvalidateRect(hwnd, NULL, FALSE);
        return;
    }
    if (id >= 520 && id < 520 + LOFT_SETS) {
        w->extra[1] = id - 520;
        InvalidateRect(hwnd, NULL, FALSE);
        return;
    }
    switch (id) {
    case ID_CONNECT: do_connect(); pop_close(); break;
    case ID_RESET: session_reset_routes(); break;
    case ID_OPEN: pop_close(); open_main(); break;
    case ID_SETTINGS: pop_close(); open_settings(); break;
    case ID_QUIT: quit_app(); break;
    case ID_IMPORT: import_dialog(hwnd); break;
    case ID_EXPORT:
        if (w->kind == WK_JOURNAL) {
            char *t = journal_text();
            copy_utf(t);
            free(t);
        } else export_profile(hwnd, w->kind == WK_EDIT ? w->extra[0] : g.active);
        break;
    case ID_SAVE: save_edit(w); redraw_all(); break;
    case ID_DELETE:
        if (w->kind == WK_JOURNAL) {
            g.nj = 0;
            memset(g.journal, 0, sizeof g.journal);
            journal_sync();
        } else if (w->kind == WK_ROUTES) delete_set(w);
        else if (w->kind == WK_EDIT) {
            drop_profile(hwnd, w->extra[0]);
            chrome_hide(hwnd);
        }
        redraw_all();
        break;
    case ID_INSTALL:
        if (svc_install_interactive()) {
            g.need_svc = 0;
            if (g.active >= 0) session_connect(g.active);
        }
        break;
    case ID_LATER: chrome_hide(hwnd); break;
    case ID_DOWNLOAD:
        if (w->kind == WK_ABOUT && !g.update_ver[0]) net_update_check();
        else net_download();
        break;
    case ID_DEFAULTS: defaults_apply(); break;
    case ID_ABOUT: open_about(); break;
    case ID_JOURNAL: open_journal(); break;
    case ID_ROUTES: pop_close(); open_routes(); break;
    case ID_SPLIT_GO:
        g.prefs.split = !g.prefs.split;
        store_save_prefs();
        session_apply_split();
        break;
    case ID_ADD:
        if (w->kind == WK_ROUTES) {
            char name[64], cidr[80];
            int zi = w->extra[0];
            ed_get(w->ed[0], name, (int)sizeof name);
            ed_get(w->ed[1], cidr, (int)sizeof cidr);
            if (zi < 0 || zi >= g.routes.nz) zi = 0;
            if (cidr[0]) {
                loft_routes_add(&g.routes, g.routes.zones[zi].id, name[0] ? name : cidr, cidr, "me");
                store_save_routes();
                session_apply_split();
                ed_set(w->ed[0], "");
                ed_set(w->ed[1], "");
            }
        } else {
            pop_close();
            open_edit(-1);
        }
        break;
    case ID_PULL: {
        Pull *p;
        int zi = w->extra[0];
        HANDLE th;
        if (zi < 0 || zi >= g.routes.nz || !g.routes.zones[zi].repo[0]) break;
        p = (Pull *)calloc(1, sizeof *p);
        if (!p) break;
        p->zone = zi;
        loft_strlcpy(p->url, sizeof p->url, g.routes.zones[zi].repo);
        th = CreateThread(NULL, 0, pull_worker, p, 0, NULL);
        if (th) CloseHandle(th);
        else free(p);
        break;
    }
    case ID_BAT: import_bat(hwnd, w); break;
    case ID_TAB0: w->tab = 0; break;
    case ID_TAB1: w->tab = 1; break;
    case ID_TAB2: w->tab = 2; break;
    case ID_LANG: lang_popup(hwnd); break;
    case ID_DOCK: cycle_dock(); break;
    case ID_RATE:
        g.prefs.rate_bits = !g.prefs.rate_bits;
        store_save_prefs();
        break;
    case ID_KIND:
        w->extra[1] = (w->extra[1] + 1) % 9;
        break;
    case ID_TUN: w->extra[2] = w->extra[2] ? 0 : 1; break;
    case ID_CONT:
        if (w->kind == WK_FIRST) first_continue(hwnd, w);
        break;
    case ID_QUIET:
        g.prefs.update_check = 0;
        store_save_prefs();
        chrome_hide(hwnd);
        break;
    case ID_DIR:
        if (w->extra[0] >= 0 && w->extra[0] < g.routes.nz) {
            g.routes.zones[w->extra[0]].def_via = !g.routes.zones[w->extra[0]].def_via;
            store_save_routes();
            session_apply_split();
        }
        break;
    case ID_LOGIN:
        g.prefs.login = !g.prefs.login;
        store_save_prefs();
        login_apply();
        break;
    case ID_SIMPLE: g.prefs.simple = !g.prefs.simple; store_save_prefs(); break;
    case ID_UPD: g.prefs.update_check = !g.prefs.update_check; store_save_prefs(); break;
    case ID_DIAG: g.prefs.diag = !g.prefs.diag; store_save_prefs(); break;
    case ID_ASSOC_F:
        g.prefs.assoc_file = !g.prefs.assoc_file;
        store_save_prefs();
        assoc_apply();
        break;
    case ID_ASSOC_L:
        g.prefs.assoc_link = !g.prefs.assoc_link;
        store_save_prefs();
        assoc_apply();
        break;
    case ID_DEFROUTE:
        g.prefs.defroute = !g.prefs.defroute;
        store_save_prefs();
        session_apply_split();
        break;
    case ID_FAILOVER: g.prefs.failover = !g.prefs.failover; store_save_prefs(); break;
    case ID_HOLD:
        g.prefs.hold = !g.prefs.hold;
        if (!g.prefs.hold && (g.held || session_phase() == PH_HELD)) session_reset_routes();
        else store_save_prefs();
        break;
    case ID_AUTO: g.prefs.auto_connect = !g.prefs.auto_connect; store_save_prefs(); break;
    case ID_RECONN: g.prefs.reconnect = !g.prefs.reconnect; store_save_prefs(); break;
    case ID_MULTI: g.prefs.multi = !g.prefs.multi; store_save_prefs(); break;
    case ID_SOCKSHR: g.prefs.socks_share = !g.prefs.socks_share; store_save_prefs(); break;
    case ID_EXTIP: g.prefs.ext_ip = !g.prefs.ext_ip; store_save_prefs(); break;
    case ID_CONN_ROW: list_popup(hwnd); break;
    case ID_MENU:
        pop_close();
        open_edit(g.active);
        break;
    default: break;
    }
    InvalidateRect(hwnd, NULL, FALSE);
    tray_update();
}

/* open_main shows the main window, creating it if needed. The open_*
   functions below follow the same pattern for the other window kinds. */
void open_main(void) {
    HWND h = chrome_find(WK_MAIN);
    if (!h) h = chrome_open(WK_MAIN, sc(400), sc(g.prefs.simple ? 460 : 592));
    show_wnd(h);
}

/* open_settings shows the settings window. */
void open_settings(void) {
    HWND h = chrome_find(WK_SET);
    if (!h) h = chrome_open(WK_SET, sc(480), sc(640));
    show_wnd(h);
}

/* open_about shows the About window. */
void open_about(void) {
    HWND h = chrome_find(WK_ABOUT);
    if (!h) h = chrome_open(WK_ABOUT, sc(360), sc(480));
    show_wnd(h);
}

/* open_journal shows the journal window with the current log. */
void open_journal(void) {
    HWND h = chrome_find(WK_JOURNAL);
    if (!h) h = chrome_open(WK_JOURNAL, sc(520), sc(512));
    journal_sync();
    show_wnd(h);
}

/* open_routes shows the split-routing editor. */
void open_routes(void) {
    HWND h = chrome_find(WK_ROUTES);
    if (!h) h = chrome_open(WK_ROUTES, sc(480), sc(640));
    show_wnd(h);
}

/* open_edit opens the profile editor for a profile, or empty for a new one
   (profile -1, TUN on by default). The key box always starts empty because
   stored keys are never shown. */
void open_edit(int profile) {
    HWND h = chrome_find(WK_EDIT);
    Win *w;
    int ki = 0, i;
    if (!h) h = chrome_open(WK_EDIT, sc(440), sc(700));
    w = chrome_win(h);
    if (!w) return;
    w->extra[0] = profile;
    w->extra[1] = 0;
    w->extra[2] = 1;
    if (profile >= 0 && profile < g.nprof) {
        LoftProfile *p = &g.prof[profile];
        for (i = 0; i < 9; i++)
            if (loft_streq(p->kind, kinds[i])) ki = i;
        w->extra[1] = ki;
        w->extra[2] = p->tun ? 1 : 0;
        ed_set(w->ed[0], p->name);
        ed_set(w->ed[1], p->url);
        ed_set(w->ed[2], p->path);
        ed_set(w->ed[3], "");
        ed_set(w->ed[4], p->socks);
        ed_set(w->ed[5], p->dns);
        ed_set(w->ed[6], p->sni);
        ed_set(w->ed[7], p->user);
    } else {
        for (i = 0; i < 8; i++) ed_set(w->ed[i], "");
    }
    show_wnd(h);
    InvalidateRect(h, NULL, FALSE);
}

/* open_first shows the first-run window. */
void open_first(void) {
    HWND h = chrome_find(WK_FIRST);
    if (!h) h = chrome_open(WK_FIRST, sc(440), sc(552));
    show_wnd(h);
}

/* open_update shows the update offer window. */
void open_update(void) {
    HWND h = chrome_find(WK_UPDATE);
    if (!h) h = chrome_open(WK_UPDATE, sc(360), sc(300));
    show_wnd(h);
}
