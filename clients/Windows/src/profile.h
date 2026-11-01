/* One connection. The key is not stored in the profile file. */
#ifndef LOFT_PROFILE_H
#define LOFT_PROFILE_H

#include <stddef.h>

/* Buffer sizes for profile fields, the profile table and loft-front's argv. */
#define LOFT_ID 40
#define LOFT_NAME 128
#define LOFT_URL 512
#define LOFT_PATH 256
#define LOFT_KEY 512
#define LOFT_PROFILES 32
#define LOFT_ARGS 40
#define LOFT_ARGN 700

/* LoftProfile is one saved connection: server URL, kind (front or foreign
   protocol), paths per front, SNI, DNS and local listener options. has_key
   says whether a secret exists for it in the DPAPI key store. */
typedef struct LoftProfile {
    char id[LOFT_ID];
    char name[LOFT_NAME];
    char kind[16];
    char url[LOFT_URL];
    char path[LOFT_PATH];
    char user[LOFT_NAME];
    char sni[LOFT_NAME];
    char socks[64];
    char dns[128];
    char edge[LOFT_URL];
    char cdn[32];
    char inner[16];
    char mask_pub[80];
    char instance[80];
    char ws_path[LOFT_PATH];
    char h2_path[LOFT_PATH];
    char h3_path[LOFT_PATH];
    char fronts[80];
    int tun;
    int mux;
    int quic;
    int tls_check;
    int pap;
    int dns_replace;
    int dns_system;
    int has_key;
} LoftProfile;

/* LoftParsed is a profile read from a file or link, together with its secret. */
typedef struct LoftParsed {
    LoftProfile p;
    char key[LOFT_KEY];
} LoftParsed;

/* LoftArgv holds the argument strings for starting loft-front. */
typedef struct LoftArgv {
    char store[LOFT_ARGS][LOFT_ARGN];
    int n;
} LoftArgv;

/* Profile defaults, YAML import/export and the loft-front command line. */
void loft_profile_clear(LoftProfile *p);
int loft_parse_blob(const char *text, const char *file_name, LoftParsed *out, int max);
/* Canonical YAML. include_key writes the secret; the on-disk profile passes 0. */
int loft_profile_yaml(const LoftProfile *p, const char *key, char *dst, size_t n);
int loft_build_argv(LoftArgv *v, const char *bin, const LoftProfile *p, const char *key,
                    const char *socks, const char *tun_pipe, const char *instance_file);

/* LoftPrefs is the app-wide settings file (prefs.yaml): language, active
   profile, and the switches from the settings window. */
typedef struct LoftPrefs {
    char lang[8];
    char active[LOFT_ID];
    char ext_ip_url[LOFT_URL];
    char taskbar[16]; /* always, window, never */
    int multi, split, simple, login;
    int assoc_file, assoc_link;
    int hold, auto_connect, failover, defroute, reconnect;
    int socks_share, ext_ip, diag, update_check, first_done;
    int socks_port;
    int rate_bits; /* 1 = kbit/s */
} LoftPrefs;

void loft_prefs_default(LoftPrefs *p);
void loft_prefs_load(LoftPrefs *p, const char *yaml);
int loft_prefs_save(const LoftPrefs *p, char *dst, size_t n);

/* STATUS line from loft-front. msg keeps its spaces. */
typedef struct LoftStatus {
    char state[24];
    char ip[64];
    char gw[64];
    char dns[128];
    char tun[8];
    char front[16];
    char fronts[80];
    char msg[256];
    char routes[256];
    int mtu;
    int mux; /* -1 omitted */
    long long up, down;
    int has_up, has_down;
} LoftStatus;

int loft_parse_status(const char *line, LoftStatus *st);

#endif
