/* Host-side checks for the pieces that do not need Windows: the portable
   core in src/ (version compare, translations, profiles, share links,
   STATUS lines from loft-front, prefs, split routes, quoting). Built and run
   with the host clang by "make test", so no Windows machine is needed. */
#include "l10n.h"
#include "profile.h"
#include "routes.h"
#include "share.h"
#include "util.h"

#include <stdio.h>
#include <string.h>

/* failed counts failed checks; main returns non-zero when it is above 0. */
static int failed;

/* expect records one check: when cond is false it prints msg and counts a
   failure, but keeps running so one run shows every broken check. */
static void expect(int cond, const char *msg) {
    if (!cond) {
        fprintf(stderr, "fail: %s\n", msg);
        failed++;
    }
}

/* test_semver pins down version comparison for the update check: a "v"
   prefix is ignored and a missing part counts as zero. */
static void test_semver(void) {
    expect(loft_newer("1.2.0", "1.1.9"), "newer minor");
    expect(!loft_newer("v1.0.0", "1.0.0"), "same");
    expect(loft_newer("1.0.3", "1.0"), "missing part");
    expect(!loft_newer("0.9.0", "1.0.0"), "older");
}

/* test_l10n checks how texts are merged: the Windows pack wins over the
   macOS pack for the same key, a missing translation falls back to English,
   an unknown language uses English, and an unknown key shows the key. */
static void test_l10n(void) {
    const char *win =
        "var WIN_PACKS = {};\n"
        "WIN_PACKS[\"en\"] = { \"login\": \"Start with Windows\", \"dock\": \"Taskbar button\" };\n"
        "WIN_PACKS[\"ru\"] = { \"login\": \"Запускать вместе с Windows\" };\n";
    const char *mac =
        "L10N_PACKS[\"en\"] = { \"login\": \"Open at login\", \"connect\": \"Connect\" };\n"
        "L10N_PACKS[\"ru\"] = { \"login\": \"Вход\", \"connect\": \"Подключить\" };\n";
    loft_l10n_reset();
    expect(loft_l10n_add_win("en", win), "win en");
    expect(loft_l10n_add_win("ru", win), "win ru");
    expect(loft_l10n_add_macos("en", mac), "mac en");
    expect(loft_l10n_add_macos("ru", mac), "mac ru");
    loft_l10n_set("ru");
    expect(loft_streq(loft_t("login"), "Запускать вместе с Windows"), "ru win wins");
    expect(loft_streq(loft_t("dock"), "Taskbar button"), "win en fallback");
    expect(loft_streq(loft_t("connect"), "Подключить"), "macos ru");
    loft_l10n_set("de");
    expect(loft_streq(loft_t("login"), "Start with Windows"), "unknown lang uses en win");
    expect(loft_streq(loft_t("missing"), "missing"), "key itself");
}

/* test_profile parses a loft-front YAML config into a profile and checks
   that the stored YAML never contains the key, the export does, the
   loft-front argument list carries the token, and wiping clears the key. */
static void test_profile(void) {
    const char *yaml =
        "name: Office\n"
        "token: \"lt_abc\"\n"
        "edge: \"https://cdn.example\"\n"
        "fronts: [get, ws]\n"
        "listen:\n"
        "  socks5: \"127.0.0.1:1080\"\n"
        "  tun: auto\n"
        "mux: true\n"
        "outbounds:\n"
        "  - name: Office\n"
        "    type: get\n"
        "    url: \"https://node.example:444\"\n"
        "    path: \"/static/v3/build\"\n"
        "    cdn: yandex\n";
    LoftParsed p[4];
    char out[2048];
    LoftArgv argv;
    int n = loft_parse_blob(yaml, "office", p, 4);
    expect(n == 1, "one profile");
    expect(loft_streq(p[0].key, "lt_abc"), "token");
    expect(loft_streq(p[0].p.url, "https://node.example:444"), "url");
    expect(loft_streq(p[0].p.kind, "get"), "kind");
    expect(loft_streq(p[0].p.path, "/static/v3/build"), "path");
    expect(loft_streq(p[0].p.cdn, "yandex"), "cdn");
    expect(p[0].p.tun == 1, "tun");
    expect(strstr(p[0].p.fronts, "get") && strstr(p[0].p.fronts, "ws"), "fronts");
    expect(loft_profile_yaml(&p[0].p, 0, out, sizeof out), "yaml");
    expect(!strstr(out, "lt_abc"), "stored yaml has no key");
    expect(strstr(out, "name: \"Office\"") != 0, "name quoted");
    expect(loft_profile_yaml(&p[0].p, p[0].key, out, sizeof out), "export");
    expect(strstr(out, "lt_abc") != 0, "export has the key");
    expect(loft_build_argv(&argv, "loft-front", &p[0].p, "lt_abc", "127.0.0.1:1080",
                           "\\\\.\\pipe\\loft-tun", "C:\\s\\id") > 4, "argv");
    expect(loft_streq(argv.store[1], "-token"), "argv token flag");
    expect(loft_streq(argv.store[2], "lt_abc"), "argv token");
    loft_wipe(p[0].key, sizeof p[0].key);
    expect(p[0].key[0] == 0, "wiped");
}

/* test_share checks share links: a loft:// link with key, name, type and
   public key; a loft:// link listing several fronts; and a vless:// link. */
static void test_share(void) {
    LoftParsed p[4];
    int n = loft_parse_share("loft://lt_abc@cdn.example:444/static/v3/build?type=get&pbk=abcd#Office", "f", p, 4);
    expect(n == 1, "loft link");
    expect(loft_streq(p[0].key, "lt_abc"), "link key");
    expect(loft_streq(p[0].p.name, "Office"), "fragment name");
    expect(loft_streq(p[0].p.kind, "get"), "type");
    expect(strstr(p[0].p.url, "cdn.example:444") != 0, "host port");
    expect(loft_streq(p[0].p.mask_pub, "abcd"), "pbk");
    n = loft_parse_share("loft://lt_abc@cdn.example?get=/g&ws=/w&quic#Lab", "f", p, 4);
    expect(n == 1 && strstr(p[0].p.fronts, "quic") && strstr(p[0].p.fronts, "ws"), "listed fronts");
    n = loft_parse_share("vless://11111111-1111-1111-1111-111111111111@ex.com:443#Home", "f", p, 4);
    expect(n == 1 && loft_streq(p[0].p.kind, "vless"), "vless kind");
    expect(loft_streq(p[0].p.name, "Home"), "vless name");
}

/* test_status parses a loft-front STATUS line; msg is the last field and
   keeps its spaces. */
static void test_status(void) {
    LoftStatus st;
    expect(loft_parse_status("STATUS state=on ip=10.8.0.2 gw=10.8.0.1 tun=1 dns=10.8.0.1 mtu=1280 up=10 down=20 front=get msg=hello there", &st), "status");
    expect(loft_streq(st.state, "on") && loft_streq(st.ip, "10.8.0.2"), "fields");
    expect(loft_streq(st.msg, "hello there"), "msg keeps spaces");
    expect(st.up == 10 && st.down == 20 && st.mtu == 1280, "numbers");
    expect(loft_streq(st.front, "get"), "front");
}

/* test_prefs loads prefs YAML (keys not given keep their defaults) and
   saves it back. */
static void test_prefs(void) {
    LoftPrefs p;
    char buf[1024];
    loft_prefs_load(&p, "lang: en\nhold: true\nsocks_port: 1088\ntaskbar: never\nassoc_file: false\n");
    expect(loft_streq(p.lang, "en") && p.hold == 1 && p.socks_port == 1088, "prefs");
    expect(loft_streq(p.taskbar, "never") && p.assoc_file == 0 && p.assoc_link == 1, "defaults stay");
    expect(loft_prefs_save(&p, buf, sizeof buf) && strstr(buf, "lang: en") != 0, "save");
}

/* test_routes loads split-routing zones and sets from routes.txt text, adds
   a set and saves the result. */
static void test_routes(void) {
    LoftRoutes r;
    char buf[2048];
    const char *text =
        "zone: local\nname: Local\nbuiltin: true\ndef: false\nrepo:\nauto: true\n\n"
        "zone: global\nname: Global\nbuiltin: true\ndef: true\nrepo:\nauto: true\n\n"
        "set: lan\nname: LAN\nzone: local\nsrc: me\ncidr: 10.0.0.0/8\n";
    expect(loft_routes_load(&r, text), "load");
    expect(r.nz == 2 && r.ns == 1, "counts");
    expect(loft_streq(r.sets[0].cidr, "10.0.0.0/8") && loft_streq(r.sets[0].zone, "local"), "set");
    expect(!r.zones[0].def_via && r.zones[1].def_via, "def");
    expect(loft_routes_add(&r, "global", "YouTube", "142.250.0.0/16", "me"), "add");
    expect(loft_routes_save(&r, buf, sizeof buf) && strstr(buf, "142.250.0.0/16") != 0, "save");

}

/* test_quote checks that YAML string quoting and unquoting round-trip an
   embedded quote. */
static void test_quote(void) {
    char b[64];
    loft_quote("a\"b", b, sizeof b);
    expect(loft_streq(b, "\"a\\\"b\""), "quote");
    loft_unquote(b);
    expect(loft_streq(b, "a\"b"), "unquote");
}

/* main runs every test and prints "ok" when all checks pass. */
int main(void) {
    test_semver();
    test_quote();
    test_l10n();
    test_profile();
    test_share();
    test_status();
    test_prefs();
    test_routes();
    if (failed) {
        fprintf(stderr, "%d failed\n", failed);
        return 1;
    }
    printf("ok\n");
    return 0;
}

