/* Windows client GUI: drawing primitives (text, fills, lines, buttons, toggles,
   rows). The same layout code in ui.c runs in paint mode to draw and in hit
   mode to find which control is under the mouse, so the two can never
   disagree. chrome.c switches the mode. */
#include "common.h"

/* State of the current pass. mode: 0 = paint into pdc, 1 = hit test at
   (hx, hy) and remember the first control id found in hid, 2 = measure only
   (bottom grows to the lowest edge drawn, giving the content height).
   chrome.c starts paint passes; ui.c starts hit and measure passes. */
static HDC pdc;
static HWND phwnd;
static int mode, hx, hy, hid, bottom;

/* paint_begin starts a paint pass of window hwnd into device context dc. */
void paint_begin(HWND hwnd, HDC dc) {
    phwnd = hwnd;
    pdc = dc;
    mode = 0;
    bottom = 0;
    hid = 0;
}

/* hit_begin starts a hit-test pass for a click at x, y; nothing is drawn. */
void hit_begin(int x, int y) {
    mode = 1;
    hx = x;
    hy = y;
    hid = 0;
    pdc = NULL;
}

/* draw_set_mode starts a pass in mode m with nothing drawn and no hit yet. */
void draw_set_mode(int m) {
    mode = m;
    bottom = 0;
    pdc = NULL;
    hid = 0;
}

/* draw_bottom returns the lowest y reached in this pass (used to size windows). */
int draw_bottom(void) { return bottom; }

/* hit_id returns the id of the control under the click, or 0. */
int hit_id(void) { return hid; }

/* span records that something occupies rows y to y+h. */
static void span(int y, int h) {
    if (y + h > bottom) bottom = y + h;
}

/* inside reports whether the hit-test point lies in the rectangle. */
static int inside(int x, int y, int w, int h) {
    return hx >= x && hy >= y && hx < x + w && hy < y + h;
}

/* text_w draws wide text in the box with DrawText flags fmt. & is shown as
   is (DT_NOPREFIX) rather than marking a keyboard shortcut. */
static void text_w(int x, int y, int w, int h, const wchar_t *s, COLORREF c, HFONT f, unsigned fmt) {
    RECT r;
    HFONT old;
    if (!s) s = L"";
    span(y, h);
    if (mode != 0 || !pdc) return;
    SetBkMode(pdc, TRANSPARENT);
    SetTextColor(pdc, c);
    old = (HFONT)SelectObject(pdc, f ? f : g.f_ui);
    r.left = x;
    r.top = y;
    r.right = x + w;
    r.bottom = y + h;
    DrawTextW(pdc, s, -1, &r, fmt | DT_NOPREFIX);
    SelectObject(pdc, old);
}

/* text_u is text_w for UTF-8 text. */
static void text_u(int x, int y, int w, int h, const char *s, COLORREF c, HFONT f, unsigned fmt) {
    wchar_t buf[768];
    widen(s ? s : "", buf, 768);
    text_w(x, y, w, h, buf, c, f, fmt);
}

/* ui_text draws UTF-8 text s; colour c, font f (NULL = UI font). */
void ui_text(int x, int y, int w, int h, const char *s, COLORREF c, HFONT f, unsigned fmt) {
    text_u(x, y, w, h, s, c, f, fmt);
}

/* ui_text_key draws the translated text of a language-pack key. */
void ui_text_key(int x, int y, int w, int h, const char *key, COLORREF c, HFONT f, unsigned fmt) {
    ui_text(x, y, w, h, loft_t(key), c, f, fmt);
}

/* ui_fill fills a rectangle with brush b. */
void ui_fill(int x, int y, int w, int h, HBRUSH b) {
    RECT r;
    span(y, h);
    if (mode != 0 || !pdc || !b || w <= 0 || h <= 0) return;
    r.left = x;
    r.top = y;
    r.right = x + w;
    r.bottom = y + h;
    FillRect(pdc, &r, b);
}

/* ui_line draws a 1 px horizontal divider in the edge colour. */
void ui_line(int x, int y, int w) {
    HPEN old;
    span(y, 1);
    if (mode != 0 || !pdc) return;
    old = (HPEN)SelectObject(pdc, g.pen_edge);
    MoveToEx(pdc, x, y, NULL);
    LineTo(pdc, x + w, y);
    SelectObject(pdc, old);
}

/* mark draws the small Loft sign, s pixels square: a copper block with a
   window. lamp picks the window colour: 0 copper (off), 1 lamp yellow,
   2 error red. */
static void mark(int x, int y, int s, int lamp) {
    RECT body, win;
    HBRUSH lamp_br;
    int m;
    span(y, s);
    if (mode != 0 || !pdc || s <= 0) return;
    m = s / 8;
    if (m < 1) m = 1;
    body.left = x + m;
    body.top = y + m;
    body.right = x + s - m;
    body.bottom = y + s - m;
    FillRect(pdc, &body, g.br_copper);
    win.left = x + s * 3 / 8;
    win.top = y + s * 5 / 16;
    win.right = win.left + s / 4;
    win.bottom = win.top + s / 5;
    if (win.right <= win.left) win.right = win.left + 2;
    if (win.bottom <= win.top) win.bottom = win.top + 2;
    lamp_br = g.br_copper;
    if (lamp == 1) lamp_br = g.br_lamp;
    if (lamp == 2) lamp_br = g.br_err;
    FillRect(pdc, &win, lamp_br);
}

/* ui_bar draws the custom title bar across width ww: sign, the word Loft,
   and with buttons set the minimise and close buttons (close turns red under
   the mouse). In hit mode it reports HIT_MIN or HIT_CLOSE. Returns the bar
   height. */
int ui_bar(int ww, int buttons) {
    int h = sc(32);
    int bw = sc(46);
    wchar_t title[32];
    span(0, h);
    if (buttons && mode == 1 && hid == 0) {
        if (inside(ww - bw, 0, bw, h)) hid = HIT_CLOSE;
        else if (inside(ww - 2 * bw, 0, bw, h)) hid = HIT_MIN;
    }
    if (mode != 0 || !pdc) return h;
    ui_fill(0, 0, ww, h, g.br_ink2);
    mark(sc(10), sc(8), sc(16), 1);
    widen("Loft", title, 32);
    text_w(sc(32), 0, ww - sc(130), h, title, C_SNOW, g.f_semi, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
    if (buttons) {
        RECT r;
        int hot = chrome_close_hot(phwnd);
        HFONT old = (HFONT)SelectObject(pdc, g.f_ui);
        SetBkMode(pdc, TRANSPARENT);
        if (hot) ui_fill(ww - bw, 0, bw, h, g.br_close);
        SetTextColor(pdc, hot ? C_SNOW : C_STONE);
        r.left = ww - bw;
        r.top = 0;
        r.right = ww;
        r.bottom = h;
        DrawTextW(pdc, L"\x00D7", -1, &r, DT_CENTER | DT_VCENTER | DT_SINGLELINE | DT_NOPREFIX);
        SetTextColor(pdc, C_STONE);
        r.left = ww - 2 * bw;
        r.right = ww - bw;
        DrawTextW(pdc, L"\x2013", -1, &r, DT_CENTER | DT_VCENTER | DT_SINGLELINE | DT_NOPREFIX);
        SelectObject(pdc, old);
    }
    return h;
}

/* ui_button draws a button and makes it clickable as id. style: 0 field
   grey, 1 copper (main action), 2 quiet text, 4 red (danger), 7 left-aligned
   lamp-coloured text; anything else is left-aligned plain text (a menu row).
   Returns id. */
int ui_button(int id, int x, int y, int w, int h, const char *label, int style) {
    HBRUSH br = NULL;
    COLORREF fg = C_SNOW;
    unsigned fmt = DT_CENTER | DT_VCENTER | DT_SINGLELINE | DT_END_ELLIPSIS;
    span(y, h);
    if (mode == 1 && hid == 0 && inside(x, y, w, h)) hid = id;
    if (mode != 0 || !pdc) return id;
    if (style == 1) {
        br = g.br_copper;
        fg = C_INK;
    } else if (style == 0) {
        br = g.br_field;
        fg = C_SNOW;
    } else if (style == 4) {
        br = g.br_close;
        fg = C_SNOW;
    } else if (style == 2) {
        fg = C_STONE;
    } else if (style == 7) {
        fg = C_LAMP;
        fmt = DT_LEFT | DT_VCENTER | DT_SINGLELINE | DT_END_ELLIPSIS;
    } else {
        fg = C_SNOW;
        fmt = DT_LEFT | DT_VCENTER | DT_SINGLELINE | DT_END_ELLIPSIS;
    }
    if (br) ui_fill(x, y, w, h, br);
    ui_text(x + sc(8), y, w - sc(16), h, label, fg, g.f_med, fmt);
    return id;
}

/* ui_toggle draws a settings row: title, an optional grey sub line, and an
   on/off switch at the right. The whole row is clickable as id. */
int ui_toggle(int id, int x, int y, int w, int on, const char *title, const char *sub) {
    int h = sc(46);
    int sw = sc(36);
    int sh = sc(18);
    RECT track, knob;
    span(y, h);
    if (mode == 1 && hid == 0 && inside(x, y, w, h)) hid = id;
    if (mode != 0 || !pdc) return id;
    ui_text(x, y + sc(4), w - sw - sc(12), sc(18), title, C_SNOW, g.f_ui, DT_LEFT | DT_SINGLELINE | DT_END_ELLIPSIS);
    if (sub && sub[0]) ui_text(x, y + sc(22), w - sw - sc(12), sc(18), sub, C_MUTE, g.f_small, DT_LEFT | DT_SINGLELINE | DT_END_ELLIPSIS);
    track.left = x + w - sw;
    track.top = y + sc(12);
    track.right = track.left + sw;
    track.bottom = track.top + sh;
    FillRect(pdc, &track, on ? g.br_copper : g.br_slate);
    knob.top = track.top + sc(2);
    knob.bottom = track.bottom - sc(2);
    knob.left = on ? track.right - (knob.bottom - knob.top) - sc(2) : track.left + sc(2);
    knob.right = knob.left + (knob.bottom - knob.top);
    FillRect(pdc, &knob, g.br_ink);
    return id;
}

/* ui_hit_row makes a rectangle clickable as id without drawing anything, for
   rows whose look is drawn by other calls. */
int ui_hit_row(int id, int x, int y, int w, int h) {
    span(y, h);
    if (mode == 1 && hid == 0 && inside(x, y, w, h)) hid = id;
    return id;
}
