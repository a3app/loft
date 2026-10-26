/* Shared Win32 state. Minimum OS is Windows 7: no Win8.1 DPI APIs. */
#ifndef LOFT_WIN_COMMON_H
#define LOFT_WIN_COMMON_H

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
#include <windowsx.h>
#include <shellapi.h>
#include <commdlg.h>
#include <shlobj.h>
#include <wincrypt.h>
#include <winhttp.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "util.h"
#include "l10n.h"
#include "profile.h"
#include "share.h"
#include "routes.h"

#ifndef LOFT_VERSION
#define LOFT_VERSION "1.0.0"
#endif

#define LOFT_SESS 8
#define LOFT_JOURNAL 400
#define LOFT_WINS 24

#define C_INK RGB(0x1A, 0x17, 0x14)
#define C_INK2 RGB(0x24, 0x1F, 0x1B)
#define C_SLATE RGB(0x3A, 0x33, 0x2E)
#define C_STONE RGB(0xB7, 0xA9, 0x9A)
#define C_COPPER RGB(0xC6, 0x7B, 0x3A)
#define C_LAMP RGB(0xE8, 0xB8, 0x4A)
#define C_LAMPHI RGB(0xF3, 0xD2, 0x7A)
#define C_SNOW RGB(0xF3, 0xED, 0xE4)
#define C_PAPER RGB(0xE8, 0xDF, 0xD2)
#define C_MUTE RGB(0x8A, 0x7E, 0x72)
#define C_OK RGB(0x7D, 0xBA, 0x6A)
#define C_ERR RGB(0xE2, 0x4B, 0x4B)
#define C_FIELD RGB(0x21, 0x1C, 0x19)
#define C_FLY RGB(0x20, 0x1C, 0x19)
#define C_EDGE RGB(0x2E, 0x28, 0x24)
#define C_CLOSE RGB(0xC4, 0x2B, 0x1C)

#define WM_LOFT_TRAY (WM_APP + 5)
#define WM_LOFT_STATUS (WM_APP + 10)
#define WM_LOFT_EXIT (WM_APP + 11)
#define WM_LOFT_EXTIP (WM_APP + 12)
#define WM_LOFT_UPD (WM_APP + 13)
#define WM_LOFT_TUN (WM_APP + 14)
#define WM_LOFT_SHOW (WM_APP + 15)
#define WM_LOFT_PULL (WM_APP + 16)

#define HIT_MIN 1001
#define HIT_CLOSE 1002
#define WM_LOFT_PULL (WM_APP + 16)

#define HIT_MIN 1001
#define HIT_CLOSE 1002

#define T_CLICK 1
#define T_BLINK 2
#define T_FOCUS 3
#define T_STATUS 4
#define T_RETRY 10

enum { PH_OFF = 0, PH_CONNECTING, PH_ON, PH_ERR, PH_HELD };
enum {
    WK_MAIN = 1,
    WK_FLY,
    WK_LIST,
    WK_MENU,
    WK_LANG,
    WK_SET,
    WK_EDIT,
    WK_ABOUT,
    WK_JOURNAL,
    WK_ROUTES,
    WK_UPDATE,
    WK_FIRST
};

enum {
    ID_CONNECT = 1,
    ID_RESET,
    ID_OPEN,
    ID_SETTINGS,
    ID_QUIT,
    ID_IMPORT,
    ID_EXPORT,
    ID_SAVE,
    ID_DELETE,
    ID_INSTALL,
    ID_LATER,
    ID_DOWNLOAD,
    ID_DEFAULTS,
    ID_ABOUT,
    ID_JOURNAL,
    ID_ROUTES,
    ID_SPLIT_GO,
    ID_ADD,
    ID_PULL,
    ID_BAT,
    ID_TAB0,
    ID_TAB1,
    ID_TAB2,
    ID_LANG,
    ID_DOCK,
    ID_RATE,
    ID_KIND,
    ID_TUN,
    ID_CONT,
    ID_QUIET,
    ID_DIR,
    ID_LOGIN,
    ID_SIMPLE,
    ID_UPD,
    ID_DIAG,
    ID_ASSOC_F,
    ID_ASSOC_L,
    ID_DEFROUTE,
    ID_FAILOVER,
    ID_HOLD,
    ID_AUTO,
    ID_RECONN,
    ID_MULTI,
    ID_SOCKSHR,
    ID_EXTIP,
    ID_CONN_ROW,
    ID_PROF = 200,
    ID_ZONE = 260,
    ID_MENU = 300,
    ID_LANGI = 400
};

typedef struct Sess {
    int used;
    int profile;
    int phase;
    int owns_tun;
    int routed;
    int user_stop;
    int restart_socks;
    int want_spawn;
    int want_tun;
    int tun_wait;
    int socks_slot;
    int retry_n;
    int nexcl;
    int ext_busy;
    DWORD up_since;
    HANDLE proc;
    HANDLE job;
    HANDLE rd_thread;
    HANDLE out_r;
    char overlay[64];
    char dns[128];
    char front[24];
    char msg[256];
    char excl[4][48];
    char ext_ip[48];
    long long up, down;
} Sess;

typedef struct Win {
    int kind;
    HWND ed[8];
    int tab;
    int extra[8];
    wchar_t title[96];
} Win;

typedef struct App {
    HINSTANCE inst;
    HWND msg;
    int dpi;
    int tray_on;
    int blink;
    int need_svc;
    int held;
    int quit;
    int nj;
    int nwin;
    int nprof;
    int active;
    HWND wins[LOFT_WINS];
    HICON icon[4];
    NOTIFYICONDATAW nid;
    HFONT f_ui, f_med, f_semi, f_mono, f_small;
    HBRUSH br_ink, br_ink2, br_field, br_fly, br_copper, br_lamp, br_err, br_slate, br_edge, br_close;
    HPEN pen_edge;
    LoftPrefs prefs;
    LoftProfile prof[LOFT_PROFILES];
    char key[LOFT_PROFILES][LOFT_KEY];
    LoftRoutes routes;
    Sess sess[LOFT_SESS];
    char journal[LOFT_JOURNAL][240];
    char update_ver[32];
} App;

extern App g;

int sc(int px);
void app_boot(void);
void app_shutdown(void);
void journal_add(const char *line);
void exe_dir(wchar_t *d, int n);
void exe_file(wchar_t *d, int n, const wchar_t *name);
char *dupstr(const char *s);

int widen(const char *u, wchar_t *d, int n);
int narrow(const wchar_t *w, char *d, int n);
char *file_read(const wchar_t *path);
int file_write(const wchar_t *path, const char *s);
int file_write_bin(const wchar_t *path, const void *p, int n);
int file_read_bin(const wchar_t *path, unsigned char **out, int *n);
void path_join(wchar_t *d, int n, const wchar_t *a, const wchar_t *b);

int store_root(wchar_t *d, int n);
void store_load(void);
int store_save_prefs(void);
int store_save_profile(int i);
int store_save_active(void);
int store_save_routes(void);
int store_add_parsed(LoftParsed *p);
void store_delete_profile(int i);

int key_save(const char *id, const char *key);
int key_load(const char *id, char *key, int n);
void key_delete(const char *id);

int svc_call(const char *cmd, char *resp, int n);
int svc_call_to(const char *cmd, char *resp, int n, DWORD ms);
int svc_install_interactive(void);

void session_connect(int profile);
void session_disconnect_slot(int slot);
void session_disconnect_active(void);
void session_reset_routes(void);
void session_on_status(int slot, char *line);
void session_on_exit(int slot);
void session_tun_ready(int slot, int ok, char *resp);
void session_retry(int slot);
void session_extip(int slot, const char *ip);
int session_phase(void);
Sess *session_primary(void);
void session_apply_split(void);

void assoc_apply(void);
void login_apply(void);

void paint_begin(HWND hwnd, HDC dc);
void hit_begin(int x, int y);
int hit_id(void);
int ui_bar(int ww, int buttons);
int ui_button(int id, int x, int y, int w, int h, const char *label, int style);
int ui_toggle(int id, int x, int y, int w, int on, const char *title, const char *sub);
void ui_text(int x, int y, int w, int h, const char *s, COLORREF c, HFONT f, unsigned fmt);
void ui_text_key(int x, int y, int w, int h, const char *key, COLORREF c, HFONT f, unsigned fmt);
void ui_fill(int x, int y, int w, int h, HBRUSH b);
void ui_line(int x, int y, int w);
int ui_hit_row(int id, int x, int y, int w, int h);

HWND chrome_open(int kind, int w, int h);
HWND chrome_find(int kind);
void chrome_hide(HWND hwnd);
void redraw_all(void);
void place_center(HWND hwnd, int w, int h);

void tray_init(void);
void tray_update(void);
void tray_remove(void);
void fly_toggle(void);
void fly_close(void);
void menu_popup(void);
void list_popup(HWND anchor);
void lang_popup(HWND anchor);
void pop_close(void);
int pop_kind(int kind);

void open_main(void);
void open_settings(void);
void open_about(void);
void open_journal(void);
void open_routes(void);
void open_edit(int profile);
void open_first(void);
void open_update(void);
void import_dialog(HWND owner);
void import_path(const wchar_t *path);
void export_profile(HWND owner, int profile);

void ui_create(HWND hwnd, Win *w);
void ui_layout(HWND hwnd, Win *w, int ww, int hh);
void ui_command(HWND hwnd, Win *w, int id);
int ui_click(HWND hwnd, Win *w, int x, int y, int ww, int hh);
int ui_measure(Win *w, int ww);
void ui_capture(Win *w);

Win *chrome_win(HWND hwnd);
void place_fly(HWND hwnd, int w, int h);
int chrome_close_hot(HWND hwnd);
void chrome_set_close_hot(HWND hwnd, int on);
void chrome_taskbar(void);
void draw_set_mode(int mode);
int draw_bottom(void);

void journal_sync(void);
void routes_pulled(int zone, char *body);
void quit_app(void);
int ui_measure(Win *w, int ww);
void ui_capture(Win *w);

Win *chrome_win(HWND hwnd);
void place_fly(HWND hwnd, int w, int h);
int chrome_close_hot(HWND hwnd);
void chrome_set_close_hot(HWND hwnd, int on);
void chrome_taskbar(void);
void draw_set_mode(int mode);
int draw_bottom(void);

void journal_sync(void);
void routes_pulled(int zone, char *body);
void quit_app(void);

void net_update_check(void);
void net_download(void);
int upd_sha256_hex(const wchar_t *path, char *dst, int n);
int upd_sums_match(const char *text, const char *name, const char *hex);
int upd_stage(const wchar_t *zip);
int upd_apply(const wchar_t *src, unsigned long pid, const wchar_t *dst);
void net_extip(int slot);
int http_get(const char *url, char **body, DWORD timeout_ms);

void fonts_make(void);
void brushes_make(void);
void fonts_free(void);

#endif
