/* Setuid helper: open utun, ifconfig, split default, pass fd via SCM_RIGHTS.
   fork/exec /sbin/ifconfig and /sbin/route. system() from setuid drops euid. */
#include "utun.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stddef.h>
#include <unistd.h>
#include <errno.h>
#include <signal.h>
#include <sys/socket.h>
#include <sys/sysctl.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <sys/stat.h>
#include <dirent.h>
#include <fcntl.h>
#include <libproc.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <net/if_dl.h>
#include <net/route.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <stdint.h>

static int gw_on_link(const char *gw);
static void discover_phys_gw(char *out, size_t n);
static int first_phys_if(char *out, size_t n);

/* runv: fork/execv `path`; keep euid 0. ignore non-zero if `ignore`. */
static int runv(int ignore, const char *path, char *const argv[]) {
    pid_t pid;
    int st;
    pid = fork();
    if (pid < 0) {
        perror("fork");
        return -1;
    }
    if (pid == 0) {
        if (geteuid() != 0) (void)setuid(0);
        execv(path, argv);
        _exit(127);
    }
    if (waitpid(pid, &st, 0) < 0) return -1;
    if (WIFEXITED(st) && WEXITSTATUS(st) == 0) return 0;
    if (!ignore) {
        fprintf(stderr, "loft-priv: %s -> %d\n", path, WIFEXITED(st) ? WEXITSTATUS(st) : -1);
        return -1;
    }
    return 0;
}

#define TUN_LOCAL "169.254.8.1"

/* ifconfig_tun: p2p IPv4 on utun. NEPacketTunnel / SSTP Connect style is
   overlay IP as both ends (A --> A) so the kernel sources from that address. */
static int ifconfig_tun(const char *name, const char *local, const char *peer, const char *mtu) {
    char *argv[] = {
        "ifconfig", (char *)name, "inet", (char *)local, (char *)peer,
        "netmask", "255.255.255.255", "mtu", (char *)mtu, "up", NULL
    };
    return runv(0, "/sbin/ifconfig", argv);
}

/* IPv4-only until overlay IPv6 is assigned. Do not install ::/0. */
static int ifconfig_no_inet6(const char *name) {
    char *argv[] = { "ifconfig", (char *)name, "inet6", "ifdisabled", NULL };
    return runv(1, "/sbin/ifconfig", argv);
}

/* lo_alias: drop a leftover overlay IP on lo0 (old dummy-local layout). */
static int lo_alias(int add, const char *ip) {
    if (add) {
        char *argv[] = {
            "ifconfig", "lo0", "alias", (char *)ip, "netmask", "255.255.255.255", NULL
        };
        return runv(0, "/sbin/ifconfig", argv);
    } else {
        char *argv[] = { "ifconfig", "lo0", "inet", (char *)ip, "-alias", NULL };
        return runv(1, "/sbin/ifconfig", argv);
    }
}

/* route_del_net: `route -n delete -inet net`. */
static int route_del_net(const char *net) {
    char *argv[] = { "route", "-n", "delete", "-inet", (char *)net, NULL };
    return runv(1, "/sbin/route", argv);
}

/* is_split_dest: Darwin prints 0/1 or 0.0.0.0/1 (and the 128.0 half). */
static int is_split_dest(const char *d) {
    return d && (!strcmp(d, "0/1") || !strcmp(d, "0.0.0.0/1")
        || !strcmp(d, "128.0/1") || !strcmp(d, "128.0.0.0/1"));
}

/* drop_split_default: remove 0/1 and 128/1. Kernel then stops injecting into utun.
   Must run BEFORE killing the utun reader and BEFORE wiping /32 pins. */
static void drop_split_default(void) {
    FILE *fp;
    char line[256];
    route_del_net("0.0.0.0/1");
    route_del_net("0/1");
    route_del_net("128.0.0.0/1");
    route_del_net("128.0/1");
    fp = popen("/usr/sbin/netstat -rn -f inet", "r");
    if (!fp) return;
    while (fgets(line, sizeof line, fp)) {
        char dest[64];
        if (sscanf(line, "%63s", dest) != 1) continue;
        if (is_split_dest(dest)) route_del_net(dest);
    }
    pclose(fp);
}

/* ifconfig_down: stop kernel output on iff (leftover 0/1 after a wedged holder). */
static void ifconfig_down(const char *iff) {
    char *argv[] = { "ifconfig", (char *)iff, "down", NULL };
    if (!iff || strncmp(iff, "utun", 4) != 0) return;
    fprintf(stderr, "loft-priv: ifconfig %s down\n", iff);
    runv(1, "/sbin/ifconfig", argv);
}

/* drop_split_force: drop 0/1, and if a split still points at utun, ifconfig it down.
   Reset Routes uses this so a dead reader cannot keep filling mbufs. */
static void drop_split_force(void) {
    FILE *fp;
    char line[256];
    char names[8][IFNAMSIZ];
    int n = 0, i, j;
    drop_split_default();
    fp = popen("/usr/sbin/netstat -rn -f inet", "r");
    if (!fp) return;
    while (fgets(line, sizeof line, fp) && n < 8) {
        char dest[64], via[64], flags[32], netif[32];
        const char *iff = NULL;
        int got = sscanf(line, "%63s %63s %31s %31s", dest, via, flags, netif);
        if (got < 1 || !is_split_dest(dest)) continue;
        if (got >= 4 && !strncmp(netif, "utun", 4)) iff = netif;
        else if (!strncmp(via, "utun", 4)) iff = via;
        if (!iff) continue;
        for (j = 0; j < n; j++) if (!strcmp(names[j], iff)) { iff = NULL; break; }
        if (!iff) continue;
        snprintf(names[n], sizeof names[n], "%s", iff);
        n++;
    }
    pclose(fp);
    for (i = 0; i < n; i++) ifconfig_down(names[i]);
    if (n) drop_split_default();
}

/* route_del_host: `route -n delete -host ip`. */
static int route_del_host(const char *ip) {
    char *argv[] = { "route", "-n", "delete", "-host", (char *)ip, NULL };
    return runv(1, "/sbin/route", argv);
}

/* route_add_host: pin `ip` via an on-link gw, else `-interface` of a live NIC.
   A leftover pin via a dead hotspot hop makes connect() return EADDRNOTAVAIL. */
static int route_add_host(const char *ip, const char *gw) {
    char iff[IFNAMSIZ];
    if (gw && gw[0] && gw_on_link(gw)) {
        char *via_gw[] = { "route", "-n", "add", "-host", (char *)ip, (char *)gw, NULL };
        return runv(1, "/sbin/route", via_gw);
    }
    if (first_phys_if(iff, sizeof iff) == 0) {
        char *via_if[] = {
            "route", "-n", "add", "-host", (char *)ip, "-interface", iff, NULL
        };
        fprintf(stderr, "loft-priv: pin %s via if %s\n", ip, iff);
        return runv(1, "/sbin/route", via_if);
    }
    fprintf(stderr, "loft-priv: pin %s skipped (no gw)\n", ip);
    return -1;
}



/* cidr24: `a.b.c.0/24` of `ip`. */
static void cidr24(const char *ip, char *out, size_t n) {
    unsigned a, b, c, d;
    if (sscanf(ip, "%u.%u.%u.%u", &a, &b, &c, &d) == 4)
        snprintf(out, n, "%u.%u.%u.0/24", a, b, c);
    else
        snprintf(out, n, "%s", ip);
}

/* route_add_split: add `net` via utun `-interface`, else via `peer`. */
static int route_add_split(const char *net, const char *name, const char *peer) {
    char *via_if[] = {
        "route", "-n", "add", "-inet", (char *)net, "-interface", (char *)name, NULL
    };
    if (runv(1, "/sbin/route", via_if) == 0) return 0;
    char *via_gw[] = {
        "route", "-n", "add", "-inet", (char *)net, (char *)peer, NULL
    };
    return runv(0, "/sbin/route", via_gw);
}

/* route_blackhole: hold default via 127.0.0.1 -blackhole. */
static int route_blackhole(void) {
    char *argv[] = {
        "route", "-n", "add", "-inet", "0.0.0.0/0", "127.0.0.1", "-blackhole", NULL
    };
    return runv(1, "/sbin/route", argv);
}

/* route_unhold: delete only the 127.0.0.1 blackhole, not a real 0/0. */
static int route_unhold(void) {
    /* Only the blackhole we installed. A bare delete of 0/0 can drop the real default. */
    char *argv[] = {
        "route", "-n", "delete", "-inet", "0.0.0.0/0", "127.0.0.1", NULL
    };
    return runv(1, "/sbin/route", argv);
}

static char hold_stamp[64];

/* stamp_hold: /tmp/loft-hold.pid so --reap can find this holder. */
static void stamp_hold(void) {
    snprintf(hold_stamp, sizeof hold_stamp, "/tmp/loft-hold.%d", (int)getpid());
    int fd = open(hold_stamp, O_CREAT | O_WRONLY | O_TRUNC, 0644);
    if (fd >= 0) close(fd);
}

/* unstamp_hold: unlink this process's hold stamp. */
static void unstamp_hold(void) {
    if (hold_stamp[0]) unlink(hold_stamp);
    hold_stamp[0] = 0;
}

/* comm_is_priv: true if pid's executable basename is loft-priv. */
static int comm_is_priv(pid_t pid) {
    char path[PROC_PIDPATHINFO_MAXSIZE];
    const char *base;
    if (proc_pidpath(pid, path, sizeof path) <= 0) return 0;
    base = strrchr(path, '/');
    base = base ? base + 1 : path;
    return strcmp(base, "loft-priv") == 0;
}

/* Holders are started with --sock. --down/--reap/--kill are not. */
/* argv_has_sock: true if pid was started with --sock (a holder). */
static int argv_has_sock(pid_t pid) {
    int mib[3] = { CTL_KERN, KERN_PROCARGS2, (int)pid };
    char buf[8192];
    size_t len = sizeof buf;
    char *p, *end;
    if (sysctl(mib, 3, buf, &len, NULL, 0) < 0 || len < sizeof(int) + 4) return 0;
    p = buf + sizeof(int);
    end = buf + len;
    while (p < end && *p) p++;
    if (p < end) p++;
    while (p < end) {
        if (!*p) { p++; continue; }
        if (strcmp(p, "--sock") == 0) return 1;
        p += strlen(p) + 1;
    }
    return 0;
}

/* kill_holder: signal another loft-priv; skip pid 1 and self. */
static void kill_holder(pid_t pid, int sig) {
    if (pid <= 1 || pid == getpid()) return;
    if (!comm_is_priv(pid)) return;
    if (kill(pid, sig) == 0)
        fprintf(stderr, "loft-priv: reap %d\n", (int)pid);
}

/* Holders run as ruid 0; the GUI cannot signal them. */
/* reap_others: SIGTERM leftover holders (hold stamps and --sock argv). */
static void reap_others(int sig) {
    DIR *d;
    struct dirent *e;
    pid_t me = getpid();
    int bytes, n, i;
    pid_t *pids;
    d = opendir("/tmp");
    if (d) {
        while ((e = readdir(d))) {
            int pid = 0;
            if (sscanf(e->d_name, "loft-hold.%d", &pid) != 1) continue;
            if ((pid_t)pid == me) continue;
            kill_holder((pid_t)pid, sig);
            {
                char path[96];
                snprintf(path, sizeof path, "/tmp/%s", e->d_name);
                unlink(path);
            }
        }
        closedir(d);
    }
    bytes = proc_listallpids(NULL, 0);
    if (bytes <= 0) return;
    n = bytes / (int)sizeof(pid_t) + 64;
    pids = calloc((size_t)n, sizeof(pid_t));
    if (!pids) return;
    bytes = proc_listallpids(pids, n * (int)sizeof(pid_t));
    if (bytes <= 0) {
        free(pids);
        return;
    }
    n = bytes / (int)sizeof(pid_t);
    for (i = 0; i < n; i++) {
        pid_t pid = pids[i];
        if (pid <= 1 || pid == me) continue;
        if (!argv_has_sock(pid)) continue;
        kill_holder(pid, sig);
    }
    free(pids);
}

/* After dropping 0/1, Darwin may still have default via utun (or none).
   Put 0/0 back on the pre-loft gateway. */
#define DNS_SAVE "/var/run/loft-dns.prev"
static int g_did_dns;

/* dns_capture: run `path` and copy stdout into `out`. */
static int dns_capture(const char *path, char *const argv[], char *out, size_t n) {
    int p[2];
    pid_t pid;
    size_t off = 0;
    int st;
    if (pipe(p) < 0) return -1;
    pid = fork();
    if (pid < 0) {
        close(p[0]);
        close(p[1]);
        return -1;
    }
    if (pid == 0) {
        if (geteuid() != 0) (void)setuid(0);
        dup2(p[1], 1);
        close(p[0]);
        close(p[1]);
        execv(path, argv);
        _exit(127);
    }
    close(p[1]);
    while (off + 1 < n) {
        ssize_t k = read(p[0], out + off, n - 1 - off);
        if (k <= 0) break;
        off += (size_t)k;
    }
    out[off] = 0;
    close(p[0]);
    if (waitpid(pid, &st, 0) < 0) return -1;
    return (WIFEXITED(st) && WEXITSTATUS(st) == 0) ? 0 : -1;
}

/* dns_set_svc: networksetup -setdnsservers for one service. */
static int dns_set_svc(const char *svc, char *const ips[]) {
    char *argv[16];
    int n = 0;
    argv[n++] = "networksetup";
    argv[n++] = "-setdnsservers";
    argv[n++] = (char *)svc;
    if (ips && ips[0]) {
        int i;
        for (i = 0; ips[i] && n < 15; i++) argv[n++] = ips[i];
    } else {
        argv[n++] = "Empty";
    }
    argv[n] = NULL;
    return runv(1, "/usr/sbin/networksetup", argv);
}

/* dns_restore: replay DNS_SAVE and unlink it. Unused on the live overlay-DNS-off path. */
static void dns_restore(void) {
    FILE *fp;
    char line[512];
    fp = fopen(DNS_SAVE, "r");
    if (!fp) return;
    while (fgets(line, sizeof line, fp)) {
        char *bar, *svc, *rest;
        char *ips[9];
        int n = 0;
        line[strcspn(line, "\r\n")] = 0;
        bar = strchr(line, '|');
        if (!bar) continue;
        *bar = 0;
        svc = line;
        rest = bar + 1;
        if (!svc[0]) continue;
        if (!rest[0] || !strcmp(rest, "Empty")) {
            dns_set_svc(svc, NULL);
            continue;
        }
        ips[n] = strtok(rest, " ");
        while (ips[n] && n < 8) {
            n++;
            ips[n] = strtok(NULL, " ");
        }
        dns_set_svc(svc, ips);
    }
    fclose(fp);
    unlink(DNS_SAVE);
    g_did_dns = 0;
}

/* dns_apply: push csv nameservers onto every Network Service; save previous. */
static int dns_apply(const char *csv) {
    char list[4096];
    char *argv[] = {"networksetup", "-listallnetworkservices", NULL};
    char *ips[9];
    char *dup, *tok;
    int nips = 0;
    char *line, *nl;
    FILE *fp;
    if (!csv || !csv[0]) return 0;
    dup = strdup(csv);
    if (!dup) return -1;
    tok = strtok(dup, ",");
    while (tok && nips < 8) {
        while (*tok == ' ' || *tok == '\t') tok++;
        if (*tok) ips[nips++] = tok;
        tok = strtok(NULL, ",");
    }
    ips[nips] = NULL;
    if (nips == 0) {
        free(dup);
        return 0;
    }
    if (dns_capture("/usr/sbin/networksetup", argv, list, sizeof list) != 0) {
        free(dup);
        return -1;
    }
    /* Keep the first snapshot of pre-loft resolvers across re-apply. */
    fp = (access(DNS_SAVE, F_OK) == 0) ? NULL : fopen(DNS_SAVE, "w");
    line = list;
    /* skip header */
    nl = strchr(line, '\n');
    if (nl) line = nl + 1;
    while (line && line[0]) {
        char svc[256];
        char getbuf[1024];
        char *getargv[4];
        size_t sl;
        nl = strchr(line, '\n');
        if (nl) *nl = 0;
        if (line[0] != '*' && line[0] != 0 && strncmp(line, "An asterisk", 11) != 0) {
            snprintf(svc, sizeof svc, "%s", line);
            sl = strlen(svc);
            if (sl && svc[sl - 1] == '\r') svc[sl - 1] = 0;
            getargv[0] = "networksetup";
            getargv[1] = "-getdnsservers";
            getargv[2] = svc;
            getargv[3] = NULL;
            if (fp) {
                if (dns_capture("/usr/sbin/networksetup", getargv, getbuf, sizeof getbuf) != 0 || strstr(getbuf, "aren't any"))
                    fprintf(fp, "%s|Empty\n", svc);
                else {
                    char *p;
                    for (p = getbuf; *p; p++) if (*p == '\n' || *p == '\r') *p = ' ';
                    fprintf(fp, "%s|%s\n", svc, getbuf);
                }
            }
            dns_set_svc(svc, ips);
            fprintf(stderr, "loft-priv: dns %s -> %s\n", svc, csv);
        }
        line = nl ? nl + 1 : NULL;
    }
    if (fp) fclose(fp);
    free(dup);
    g_did_dns = 1;
    return 0;
}

#define DID_DEFAULT_STAMP "/tmp/loft-did-default"

/* stamp_default: /tmp/loft-did-default so --down knows to restore 0/0. */
static void stamp_default(void) {
    int fd = open(DID_DEFAULT_STAMP, O_CREAT | O_WRONLY | O_TRUNC, 0644);
    if (fd >= 0) close(fd);
}

/* unstamp_default: unlink the split-default stamp. */
static void unstamp_default(void) {
    unlink(DID_DEFAULT_STAMP);
}

/* stamped_default: true if loft installed the 0/1 split. */
static int stamped_default(void) {
    return access(DID_DEFAULT_STAMP, F_OK) == 0;
}

/* route_restore_default: 0/0 via an on-link gw. A remembered hotspot hop
   (172.20.10.1 after lid-close) is skipped; discover the live Wi-Fi/Ethernet
   router, else `-interface` of the first physical NIC. */
static int route_restore_default(const char *gw) {
    char found[64];
    char iff[IFNAMSIZ];
    if (!gw || !gw[0] || !strcmp(gw, TUN_LOCAL) || !strncmp(gw, "link#", 5) || !gw_on_link(gw)) {
        discover_phys_gw(found, sizeof found);
        gw = found[0] ? found : NULL;
    }
    if (gw && gw[0]) {
        char *chg[] = {
            "route", "-n", "change", "-inet", "default", (char *)gw, NULL
        };
        char *add[] = {
            "route", "-n", "add", "-inet", "default", (char *)gw, NULL
        };
        /* Darwin `route change` often prints "not in table" and still exits 0.
           Always try add: File exists is fine, missing default is not. */
        runv(1, "/sbin/route", chg);
        if (runv(1, "/sbin/route", add) == 0)
            fprintf(stderr, "loft-priv: default add %s\n", gw);
        else
            fprintf(stderr, "loft-priv: default via %s\n", gw);
        return 0;
    }
    if (first_phys_if(iff, sizeof iff) == 0) {
        char *addif[] = {
            "route", "-n", "add", "-inet", "default", "-interface", iff, NULL
        };
        fprintf(stderr, "loft-priv: default via if %s\n", iff);
        runv(1, "/sbin/route", addif);
    } else {
        fprintf(stderr, "loft-priv: default skipped (no gw)\n");
    }
    return 0;
}

/* send_fd: connect to unix sock and pass the utun fd via SCM_RIGHTS. */
static int send_fd(const char *path, int tun, const char *name) {
    int fd;
    struct sockaddr_un un;
    struct msghdr msg;
    struct iovec iov;
    char cbuf[CMSG_SPACE(sizeof(int))];
    struct cmsghdr *c;
    char nbuf[16];
    socklen_t slen;
    fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return -1;
    memset(&un, 0, sizeof un);
    un.sun_family = AF_UNIX;
    strncpy(un.sun_path, path, sizeof(un.sun_path) - 1);
    un.sun_len = (unsigned char)(offsetof(struct sockaddr_un, sun_path) + strlen(un.sun_path) + 1);
    slen = un.sun_len;
    if (connect(fd, (struct sockaddr *)&un, slen) < 0) {
        fprintf(stderr, "loft-priv: connect %s: %s\n", path, strerror(errno));
        close(fd);
        return -1;
    }
    memset(&msg, 0, sizeof msg);
    memset(nbuf, 0, sizeof nbuf);
    strncpy(nbuf, name, 15);
    iov.iov_base = nbuf;
    iov.iov_len = sizeof nbuf;
    msg.msg_iov = &iov;
    msg.msg_iovlen = 1;
    msg.msg_control = cbuf;
    msg.msg_controllen = sizeof cbuf;
    c = CMSG_FIRSTHDR(&msg);
    c->cmsg_level = SOL_SOCKET;
    c->cmsg_type = SCM_RIGHTS;
    c->cmsg_len = CMSG_LEN(sizeof(int));
    memcpy(CMSG_DATA(c), &tun, sizeof tun);
    if (sendmsg(fd, &msg, 0) < 0) {
        fprintf(stderr, "loft-priv: sendmsg: %s\n", strerror(errno));
        close(fd);
        return -1;
    }
    close(fd);
    return 0;
}

/* each_ip: call `fn` for each comma-separated exclude IP. */
static void each_ip(const char *exclude, void (*fn)(const char *)) {
    char *dup, *tok;
    if (!exclude || !exclude[0]) return;
    dup = strdup(exclude);
    if (!dup) return;
    tok = strtok(dup, ",");
    while (tok) {
        fn(tok);
        tok = strtok(NULL, ",");
    }
    free(dup);
}

static const char *g_gw;

static char g_ip[64], g_peer[64], g_gwbuf[64];
static int g_tun = -1, g_did_default;
static volatile sig_atomic_t stop_hold;
static void wipe_excl(void);

/* on_hold_sig: SIGTERM/INT/HUP while holding utun. */
static void on_hold_sig(int sig) {
    (void)sig;
    stop_hold = 1;
}

/* hold_cleanup: drop overlay routes, restore 0/0 if we installed it, close utun. */
static void hold_cleanup(void) {
    char net[32];
    drop_split_default();
    if (g_ip[0]) {
        cidr24(g_ip, net, sizeof net);
        route_del_net(net);
        route_del_host(g_ip);
        lo_alias(0, g_ip);
    }
    if (g_peer[0]) route_del_host(g_peer);
    route_del_host(TUN_LOCAL);
    /* --set-default is a one-shot and does not set g_did_default.
       Only rewrite 0/0 if loft actually installed the split default. */
    if (g_did_default || stamped_default()) {
        if (g_gwbuf[0]) route_restore_default(g_gwbuf);
        unstamp_default();
    }
    if (g_did_dns) dns_restore();
    wipe_excl();
    if (g_tun >= 0) {
        loft_utun_close(g_tun);
        g_tun = -1;
    }
    unstamp_hold();
}

#define EXCL_STAMP "/tmp/loft-exclude"

/* stamp_excl: remember a host loft pinned, so --restore can drop it without a list. */
static void stamp_excl(const char *ip) {
    FILE *fp;
    char line[64];
    if (!ip || !ip[0]) return;
    fp = fopen(EXCL_STAMP, "r");
    if (fp) {
        while (fgets(line, sizeof line, fp)) {
            line[strcspn(line, "\r\n")] = 0;
            if (!strcmp(line, ip)) {
                fclose(fp);
                return;
            }
        }
        fclose(fp);
    }
    fp = fopen(EXCL_STAMP, "a");
    if (!fp) return;
    fprintf(fp, "%s\n", ip);
    fclose(fp);
}

/* load_excl_list: host IPs from EXCL_STAMP. */
static int load_excl_list(char ips[][64], int max) {
    FILE *fp;
    int n = 0;
    fp = fopen(EXCL_STAMP, "r");
    if (!fp) return 0;
    while (n < max && fgets(ips[n], 64, fp)) {
        ips[n][strcspn(ips[n], "\r\n")] = 0;
        if (ips[n][0] && ips[n][0] != '#') n++;
    }
    fclose(fp);
    return n;
}

/* add_excl: pin `ip` on a live physical hop (gw or NIC). Replace a leftover pin first.
   Skip on-link LAN: the subnet route already beats 0/1, and `route add -host GW GW`
   returns EADDRNOTAVAIL. */
static void add_excl(const char *ip) {
    if (!ip || !ip[0] || gw_on_link(ip)) return;
    route_del_host(ip);
    route_add_host(ip, g_gw);
    stamp_excl(ip);
}

/* del_excl: drop a host exclude route. */
static void del_excl(const char *ip) { route_del_host(ip); }

/* parse_ip_csv: comma-separated exclude IPs, skipping on-link LAN. */
static int parse_ip_csv(const char *exclude, char ips[][64], int max) {
    char *dup, *tok;
    int n = 0;
    if (!exclude || !exclude[0] || max <= 0) return 0;
    dup = strdup(exclude);
    if (!dup) return 0;
    tok = strtok(dup, ",");
    while (tok && n < max) {
        while (*tok == ' ' || *tok == '\t') tok++;
        if (*tok && !gw_on_link(tok)) {
            snprintf(ips[n], 64, "%s", tok);
            n++;
        }
        tok = strtok(NULL, ",");
    }
    free(dup);
    return n;
}

/* ip_in_list: true if ip is in ips[0..n). */
static int ip_in_list(char ips[][64], int n, const char *ip) {
    int i;
    if (!ip) return 0;
    for (i = 0; i < n; i++) {
        if (!strcmp(ips[i], ip)) return 1;
    }
    return 0;
}

/* rewrite_excl_stamp: stamp file becomes exactly ips (no leftover wipe list). */
static void rewrite_excl_stamp(char ips[][64], int n) {
    FILE *fp;
    int i;
    fp = fopen(EXCL_STAMP, "w");
    if (!fp) return;
    for (i = 0; i < n; i++) {
        if (ips[i][0]) fprintf(fp, "%s\n", ips[i]);
    }
    fclose(fp);
}

/* pin_replace: install exclude hosts without wiping all pins first.
   wipe_excl then add left a window where 0/1 ate packets to the front. */
static void pin_replace(const char *exclude) {
    char old[64][64], neu[64][64];
    int nold, nnew, i;
    nold = load_excl_list(old, 64);
    nnew = parse_ip_csv(exclude, neu, 64);
    for (i = 0; i < nnew; i++) {
        if (!ip_in_list(old, nold, neu[i])) add_excl(neu[i]);
    }
    for (i = 0; i < nold; i++) {
        if (!ip_in_list(neu, nnew, old[i]) && !gw_on_link(old[i]))
            route_del_host(old[i]);
    }
    rewrite_excl_stamp(neu, nnew);
}

/* first_phys_if: name of an UP IPv4 NIC (prefer en*), skipping utun/lo/feth. */
static int first_phys_if(char *out, size_t n) {
    struct ifaddrs *ifa, *p;
    char fallback[IFNAMSIZ];
    fallback[0] = 0;
    if (!out || n == 0) return -1;
    out[0] = 0;
    if (getifaddrs(&ifa) != 0) return -1;
    for (p = ifa; p; p = p->ifa_next) {
        if (!p->ifa_name || !p->ifa_addr) continue;
        if (p->ifa_addr->sa_family != AF_INET) continue;
        if (!(p->ifa_flags & IFF_UP) || (p->ifa_flags & IFF_LOOPBACK)) continue;
        if (strncmp(p->ifa_name, "utun", 4) == 0) continue;
        if (strncmp(p->ifa_name, "lo", 2) == 0) continue;
        if (strncmp(p->ifa_name, "feth", 4) == 0) continue;
        if (strncmp(p->ifa_name, "en", 2) == 0) {
            snprintf(out, n, "%s", p->ifa_name);
            freeifaddrs(ifa);
            return 0;
        }
        if (!fallback[0]) snprintf(fallback, sizeof fallback, "%s", p->ifa_name);
    }
    freeifaddrs(ifa);
    if (fallback[0]) {
        snprintf(out, n, "%s", fallback);
        return 0;
    }
    return -1;
}

/* ipconfig_router: DHCP/static router for `iff`, or 0. */
static int ipconfig_router(const char *iff, char *out, size_t n) {
    char buf[128];
    char *argv[] = { "ipconfig", "getoption", (char *)iff, "router", NULL };
    struct in_addr a;
    if (!iff || !out || n == 0) return 0;
    out[0] = 0;
    if (dns_capture("/usr/sbin/ipconfig", argv, buf, sizeof buf) != 0) return 0;
    buf[strcspn(buf, "\r\n")] = 0;
    if (inet_pton(AF_INET, buf, &a) != 1) return 0;
    snprintf(out, n, "%s", buf);
    return 1;
}

/* discover_phys_gw: on-link IPv4 router even when loft 0/1 is the lookup default.
   Order: netstat `default` on en* that is still on-link, then any on-link default,
   then `ipconfig getoption IF router` on physical ifaces. */
static void discover_phys_gw(char *out, size_t n) {
    char blob[8192];
    char *line, *nl;
    char fallback[64];
    char *nsargv[] = { "netstat", "-rn", "-f", "inet", NULL };
    struct ifaddrs *ifa, *p;
    if (!out || n == 0) return;
    out[0] = 0;
    fallback[0] = 0;
    if (dns_capture("/usr/sbin/netstat", nsargv, blob, sizeof blob) == 0) {
        line = blob;
        while (line && line[0]) {
            char dest[64], via[64], flags[32], netif[32];
            nl = strchr(line, '\n');
            if (nl) *nl = 0;
            if (sscanf(line, "%63s %63s %31s %31s", dest, via, flags, netif) >= 4
                && strcmp(dest, "default") == 0
                && strncmp(netif, "utun", 4) != 0
                && gw_on_link(via)) {
                if (strncmp(netif, "en", 2) == 0) {
                    snprintf(out, n, "%s", via);
                    return;
                }
                if (!fallback[0]) snprintf(fallback, sizeof fallback, "%s", via);
            }
            line = nl ? nl + 1 : NULL;
        }
    }
    if (fallback[0]) {
        snprintf(out, n, "%s", fallback);
        return;
    }
    if (getifaddrs(&ifa) != 0) return;
    for (p = ifa; p; p = p->ifa_next) {
        char r[64];
        if (!p->ifa_name || !p->ifa_addr) continue;
        if (p->ifa_addr->sa_family != AF_INET) continue;
        if (strncmp(p->ifa_name, "utun", 4) == 0) continue;
        if (strncmp(p->ifa_name, "lo", 2) == 0) continue;
        if (strncmp(p->ifa_name, "feth", 4) == 0) continue;
        if (ipconfig_router(p->ifa_name, r, sizeof r) && gw_on_link(r)) {
            snprintf(out, n, "%s", r);
            freeifaddrs(ifa);
            return;
        }
    }
    freeifaddrs(ifa);
}

/* gw_on_link: true if `gw` is on a physical IPv4 subnet (not utun/lo/feth). */
static int gw_on_link(const char *gw) {
    struct ifaddrs *ifa, *p;
    struct in_addr want;
    if (!gw || !gw[0]) return 0;
    if (inet_pton(AF_INET, gw, &want) != 1) return 0;
    if (getifaddrs(&ifa) != 0) return 0;
    for (p = ifa; p; p = p->ifa_next) {
        struct in_addr ip, mask;
        if (!p->ifa_name || !p->ifa_addr || !p->ifa_netmask) continue;
        if (p->ifa_addr->sa_family != AF_INET) continue;
        if (strncmp(p->ifa_name, "utun", 4) == 0) continue;
        if (strncmp(p->ifa_name, "lo", 2) == 0) continue;
        if (strncmp(p->ifa_name, "feth", 4) == 0) continue;
        ip = ((struct sockaddr_in *)p->ifa_addr)->sin_addr;
        mask = ((struct sockaddr_in *)p->ifa_netmask)->sin_addr;
        if (mask.s_addr && (ip.s_addr & mask.s_addr) == (want.s_addr & mask.s_addr)) {
            freeifaddrs(ifa);
            return 1;
        }
    }
    freeifaddrs(ifa);
    return 0;
}

/* scrub_stale_hosts: drop leftover exclude pins whose next hop is a dead hotspot gw.
   Skip overlay / link-local / non-IPv4 (link#) so a live utun stays intact. */
static void scrub_stale_hosts(void) {
    FILE *fp;
    char line[256];
    fp = popen("/usr/sbin/netstat -rn -f inet", "r");
    if (!fp) return;
    while (fgets(line, sizeof line, fp)) {
        char dest[64], via[64], flags[32];
        struct in_addr dip, vip;
        unsigned d;
        if (sscanf(line, "%63s %63s %31s", dest, via, flags) != 3) continue;
        if (strchr(flags, 'H') == NULL || strchr(flags, 'S') == NULL) continue;
        if (inet_pton(AF_INET, dest, &dip) != 1) continue;
        if (inet_pton(AF_INET, via, &vip) != 1) continue;
        d = ntohl(dip.s_addr);
        if ((d & 0xffffff00u) == 0x0a080000u) continue; /* 10.8.0.0/24 overlay */
        if ((d & 0xffff0000u) == 0xa9fe0000u) continue; /* 169.254.0.0/16 dummy */
        if (gw_on_link(via)) continue;
        fprintf(stderr, "loft-priv: drop stale host %s via %s\n", dest, via);
        route_del_host(dest);
    }
    pclose(fp);
}

/* wipe_excl: delete every stamped host pin, even if this call has no --exclude.
   Do not `route delete -host` an on-link router (that is the ARP entry for default). */
static void wipe_excl(void) {
    char ips[64][64];
    int n = load_excl_list(ips, 64);
    int i;
    for (i = 0; i < n; i++) {
        if (gw_on_link(ips[i])) continue;
        route_del_host(ips[i]);
    }
    unlink(EXCL_STAMP);
    scrub_stale_hosts();
}

#define SPLIT_MAX 65536
#define SPLIT_WANT_VIA "/tmp/loft-split-want-via"
#define SPLIT_WANT_DIRECT "/tmp/loft-split-want-direct"
#define SPLIT_HAVE_VIA "/tmp/loft-split-via"
#define SPLIT_HAVE_DIRECT "/tmp/loft-split-direct"
#define SPLIT_META "/tmp/loft-split-meta"
#define RT_ROUNDUP(a) ((a) > 0 ? (1 + (((a) - 1) | (sizeof(uint32_t) - 1))) : sizeof(uint32_t))

struct cidr4 {
    uint32_t net;
    uint8_t bits;
};

static int g_rtfd = -1;
static int g_rtseq;

static int cidr_cmp(const void *a, const void *b) {
    const struct cidr4 *x = a, *y = b;
    if (x->net < y->net) return -1;
    if (x->net > y->net) return 1;
    if (x->bits < y->bits) return -1;
    if (x->bits > y->bits) return 1;
    return 0;
}

/* parse_cidr4: `a.b.c.d` or `a.b.c.d/n` into a masked network. */
static int parse_cidr4(const char *s, struct cidr4 *out) {
    unsigned a, b, c, d, bits = 32;
    int n;
    uint32_t mask, net;
    if (!s || !out) return -1;
    n = sscanf(s, "%u.%u.%u.%u/%u", &a, &b, &c, &d, &bits);
    if (n == 4) bits = 32;
    else if (n != 5) return -1;
    if (a > 255 || b > 255 || c > 255 || d > 255 || bits > 32) return -1;
    net = (a << 24) | (b << 16) | (c << 8) | d;
    mask = bits == 0 ? 0 : (0xffffffffu << (32 - bits));
    out->net = net & mask;
    out->bits = (uint8_t)bits;
    return 0;
}

/* cidr_skip: default halves, loopback, link-local, multicast. */
static int cidr_skip(const struct cidr4 *c) {
    uint32_t net = c->net;
    unsigned bits = c->bits;
    if (bits == 0) return 1;
    if (bits == 1 && (net == 0 || net == 0x80000000u)) return 1;
    if (bits >= 8 && (net & 0xff000000u) == 0x7f000000u) return 1;
    if (bits >= 16 && (net & 0xffff0000u) == 0xa9fe0000u) return 1;
    if (bits >= 4 && (net & 0xf0000000u) == 0xe0000000u) return 1;
    return 0;
}

static void cidr_fmt(const struct cidr4 *c, char *out, size_t n) {
    snprintf(out, n, "%u.%u.%u.%u/%u",
        (c->net >> 24) & 255, (c->net >> 16) & 255,
        (c->net >> 8) & 255, c->net & 255, c->bits);
}

/* load_cidrs: unique IPv4 prefixes from `path`. */
static int load_cidrs(const char *path, struct cidr4 *out, int cap) {
    FILE *fp;
    char line[128];
    int n = 0, i;
    fp = fopen(path, "r");
    if (!fp) return 0;
    while (fgets(line, sizeof line, fp)) {
        struct cidr4 c;
        char *p = line;
        size_t L;
        while (*p == ' ' || *p == '\t') p++;
        if (*p == 0 || *p == '#' || *p == '\n' || *p == '\r') continue;
        L = strlen(p);
        while (L && (p[L - 1] == '\n' || p[L - 1] == '\r' || p[L - 1] == ' ')) p[--L] = 0;
        if (parse_cidr4(p, &c) != 0 || cidr_skip(&c)) continue;
        if (n >= cap) break;
        out[n++] = c;
    }
    fclose(fp);
    if (n > 1) qsort(out, (size_t)n, sizeof out[0], cidr_cmp);
    i = 0;
    {
        int w = 0;
        for (i = 0; i < n; i++) {
            if (w && cidr_cmp(&out[w - 1], &out[i]) == 0) continue;
            out[w++] = out[i];
        }
        n = w;
    }
    return n;
}

static void save_cidrs(const char *path, const struct cidr4 *v, int n) {
    FILE *fp;
    int i;
    char buf[64];
    fp = fopen(path, "w");
    if (!fp) return;
    for (i = 0; i < n; i++) {
        cidr_fmt(&v[i], buf, sizeof buf);
        fprintf(fp, "%s\n", buf);
    }
    fclose(fp);
}

static int rt_fd(void) {
    if (g_rtfd >= 0) return g_rtfd;
    g_rtfd = socket(PF_ROUTE, SOCK_RAW, AF_UNSPEC);
    return g_rtfd;
}

/* rt_mod: RTM_ADD / RTM_DELETE one IPv4 prefix via gw or `-interface` iff. */
static int rt_mod(int cmd, const struct cidr4 *c, const char *gw, const char *iff) {
    uint8_t buf[256];
    struct rt_msghdr *m;
    uint8_t *p;
    struct sockaddr_in sin;
    int addrs = RTA_DST;
    int flags = RTF_UP | RTF_STATIC;
    unsigned idx = 0;
    int fd, n, tries = 0;
    memset(buf, 0, sizeof buf);
    m = (struct rt_msghdr *)buf;
    p = buf + sizeof(*m);
    memset(&sin, 0, sizeof sin);
    sin.sin_len = sizeof sin;
    sin.sin_family = AF_INET;
    sin.sin_addr.s_addr = htonl(c->net);
    memcpy(p, &sin, sizeof sin);
    p += RT_ROUNDUP(sizeof sin);
    if (iff && iff[0]) {
        struct sockaddr_dl sdl;
        size_t nlen;
        idx = if_nametoindex(iff);
        if (!idx) return -1;
        nlen = strlen(iff);
        if (nlen >= sizeof sdl.sdl_data) return -1;
        memset(&sdl, 0, sizeof sdl);
        sdl.sdl_family = AF_LINK;
        sdl.sdl_index = (u_short)idx;
        sdl.sdl_nlen = (u_char)nlen;
        memcpy(sdl.sdl_data, iff, nlen);
        sdl.sdl_len = (u_char)(offsetof(struct sockaddr_dl, sdl_data) + nlen);
        memcpy(p, &sdl, sdl.sdl_len);
        p += RT_ROUNDUP(sdl.sdl_len);
        addrs |= RTA_GATEWAY;
        m->rtm_index = (u_short)idx;
    } else if (gw && gw[0]) {
        memset(&sin, 0, sizeof sin);
        sin.sin_len = sizeof sin;
        sin.sin_family = AF_INET;
        if (inet_pton(AF_INET, gw, &sin.sin_addr) != 1) return -1;
        memcpy(p, &sin, sizeof sin);
        p += RT_ROUNDUP(sizeof sin);
        addrs |= RTA_GATEWAY;
        flags |= RTF_GATEWAY;
    } else {
        return -1;
    }
    if (c->bits == 32) {
        flags |= RTF_HOST;
    } else {
        uint32_t mask = c->bits == 0 ? 0 : (0xffffffffu << (32 - c->bits));
        memset(&sin, 0, sizeof sin);
        sin.sin_len = sizeof sin;
        sin.sin_family = AF_INET;
        sin.sin_addr.s_addr = htonl(mask);
        memcpy(p, &sin, sizeof sin);
        p += RT_ROUNDUP(sizeof sin);
        addrs |= RTA_NETMASK;
    }
    m->rtm_msglen = (u_short)(p - buf);
    m->rtm_version = RTM_VERSION;
    m->rtm_type = (u_char)cmd;
    m->rtm_flags = flags;
    m->rtm_addrs = addrs;
    m->rtm_pid = getpid();
    m->rtm_seq = ++g_rtseq;
    fd = rt_fd();
    if (fd < 0) return -1;
    for (;;) {
        n = (int)write(fd, buf, m->rtm_msglen);
        if (n == (int)m->rtm_msglen) return 0;
        if (errno == EEXIST || errno == ESRCH || errno == EADDRNOTAVAIL) return 0;
        if (errno == ENOBUFS && tries++ < 8) {
            usleep(2000);
            continue;
        }
        return -1;
    }
}

/* split_diff: delete have-not-want, add want-not-have. */
static void split_diff(const struct cidr4 *have, int nh,
    const struct cidr4 *want, int nw,
    const char *gw, const char *iff, int *added, int *deleted) {
    int i = 0, j = 0;
    while (i < nh || j < nw) {
        int cmp;
        if (i == nh) cmp = 1;
        else if (j == nw) cmp = -1;
        else cmp = cidr_cmp(&have[i], &want[j]);
        if (cmp < 0) {
            if (rt_mod(RTM_DELETE, &have[i], gw, iff) == 0) (*deleted)++;
            i++;
        } else if (cmp > 0) {
            if (rt_mod(RTM_ADD, &want[j], gw, iff) == 0) (*added)++;
            j++;
        } else {
            i++;
            j++;
        }
    }
}

static void split_wipe(const struct cidr4 *have, int n, const char *gw, const char *iff, int *deleted) {
    int i;
    for (i = 0; i < n; i++) {
        if (rt_mod(RTM_DELETE, &have[i], gw, iff) == 0) (*deleted)++;
    }
}

static void split_read_meta(char *iff, size_t ni, char *gw, size_t ng) {
    FILE *fp;
    char line[128];
    iff[0] = 0;
    gw[0] = 0;
    fp = fopen(SPLIT_META, "r");
    if (!fp) return;
    while (fgets(line, sizeof line, fp)) {
        if (!strncmp(line, "if=", 3)) {
            snprintf(iff, ni, "%s", line + 3);
            iff[strcspn(iff, "\r\n")] = 0;
        } else if (!strncmp(line, "gw=", 3)) {
            snprintf(gw, ng, "%s", line + 3);
            gw[strcspn(gw, "\r\n")] = 0;
        }
    }
    fclose(fp);
}

static void split_write_meta(const char *iff, const char *gw) {
    FILE *fp = fopen(SPLIT_META, "w");
    if (!fp) return;
    fprintf(fp, "if=%s\ngw=%s\n", iff ? iff : "", gw ? gw : "");
    fclose(fp);
}

/* split_apply: install want-via via utun and want-direct via native gw. */
static void split_apply(const char *iff, const char *gw) {
    static struct cidr4 have_via[SPLIT_MAX], want_via[SPLIT_MAX];
    static struct cidr4 have_dir[SPLIT_MAX], want_dir[SPLIT_MAX];
    char old_if[IFNAMSIZ], old_gw[64];
    int nv_h, nv_w, nd_h, nd_w;
    int add_v = 0, del_v = 0, add_d = 0, del_d = 0;
    int via_ok, dir_ok, rebind_via, rebind_dir;
    split_read_meta(old_if, sizeof old_if, old_gw, sizeof old_gw);
    nv_h = load_cidrs(SPLIT_HAVE_VIA, have_via, SPLIT_MAX);
    nv_w = load_cidrs(SPLIT_WANT_VIA, want_via, SPLIT_MAX);
    nd_h = load_cidrs(SPLIT_HAVE_DIRECT, have_dir, SPLIT_MAX);
    nd_w = load_cidrs(SPLIT_WANT_DIRECT, want_dir, SPLIT_MAX);
    via_ok = iff && iff[0] && if_nametoindex(iff) != 0;
    dir_ok = gw && gw[0] && gw_on_link(gw);
    if (!dir_ok && gw && gw[0]) {
        char found[64];
        discover_phys_gw(found, sizeof found);
        if (found[0]) {
            gw = found;
            dir_ok = gw_on_link(gw);
        }
    }
    if (!via_ok) nv_w = 0;
    if (!dir_ok) nd_w = 0;
    rebind_via = via_ok && old_if[0] && strcmp(old_if, iff) != 0;
    rebind_dir = dir_ok && old_gw[0] && strcmp(old_gw, gw) != 0;
    if (rebind_via || !via_ok) {
        split_wipe(have_via, nv_h, NULL, old_if[0] ? old_if : iff, &del_v);
        nv_h = 0;
    }
    if (rebind_dir || !dir_ok) {
        split_wipe(have_dir, nd_h, old_gw[0] ? old_gw : gw, NULL, &del_d);
        nd_h = 0;
    }
    if (via_ok) split_diff(have_via, nv_h, want_via, nv_w, NULL, iff, &add_v, &del_v);
    if (dir_ok) split_diff(have_dir, nd_h, want_dir, nd_w, gw, NULL, &add_d, &del_d);
    save_cidrs(SPLIT_HAVE_VIA, want_via, nv_w);
    save_cidrs(SPLIT_HAVE_DIRECT, want_dir, nd_w);
    split_write_meta(via_ok ? iff : "", dir_ok ? gw : "");
    fprintf(stderr, "loft-priv: split via +%d -%d n=%d if=%s direct +%d -%d n=%d gw=%s\n",
        add_v, del_v, nv_w, via_ok ? iff : "-",
        add_d, del_d, nd_w, dir_ok ? gw : "-");
}

/* split_clear: drop stamped extra CIDRs. keep_direct leaves around-loft pins. */
static void split_clear(int keep_direct) {
    static struct cidr4 have_via[SPLIT_MAX], have_dir[SPLIT_MAX];
    char old_if[IFNAMSIZ], old_gw[64];
    int nv, nd, del_v = 0, del_d = 0;
    split_read_meta(old_if, sizeof old_if, old_gw, sizeof old_gw);
    nv = load_cidrs(SPLIT_HAVE_VIA, have_via, SPLIT_MAX);
    nd = load_cidrs(SPLIT_HAVE_DIRECT, have_dir, SPLIT_MAX);
    split_wipe(have_via, nv, NULL, old_if, &del_v);
    unlink(SPLIT_HAVE_VIA);
    unlink(SPLIT_WANT_VIA);
    if (!keep_direct) {
        split_wipe(have_dir, nd, old_gw, NULL, &del_d);
        unlink(SPLIT_HAVE_DIRECT);
        unlink(SPLIT_WANT_DIRECT);
        unlink(SPLIT_META);
    } else {
        split_write_meta("", old_gw);
    }
    fprintf(stderr, "loft-priv: split clear via -%d direct -%d keep_direct=%d\n",
        del_v, keep_direct ? 0 : del_d, keep_direct);
}

/* rebind_excl: move stamped pins onto a live physical hop (Wi-Fi after iPhone). */
static void rebind_excl(const char *gw) {
    char ips[64][64];
    char found[64];
    int n = load_excl_list(ips, 64);
    int i;
    if (!gw || !gw[0] || !gw_on_link(gw)) {
        discover_phys_gw(found, sizeof found);
        gw = found[0] ? found : NULL;
    }
    g_gw = gw;
    for (i = 0; i < n; i++) add_excl(ips[i]);
}

/* main: --sock hold, --down/--restore/--hold, --set-default/--clear-default, --kill/--reap. */
int main(int argc, char **argv) {
    const char *sock = NULL, *ip = NULL, *peer = NULL, *mtu = "1280", *gw = NULL, *exclude = NULL, *ifname = NULL, *dns = NULL;
    int i, tun, drop_bh = 0, down = 0, hold = 0, want_default = 0, set_default = 0, clear_default = 0;
    int do_kill = 0, force = 0, reap = 0, restore = 0, overlay_only = 0, rebind = 0, pin = 0, discover = 0;
    int apply_dns = 0, restore_dns_only = 0, drop_split = 0, split_ifdown = 0;
    int apply_split = 0, clear_split = 0, keep_direct = 0;
    pid_t kill_pid = 0;
    char name[16];
    for (i = 1; i < argc; i++) {
        if (!strcmp(argv[i], "--sock") && i + 1 < argc) sock = argv[++i];
        else if (!strcmp(argv[i], "--ip") && i + 1 < argc) ip = argv[++i];
        else if (!strcmp(argv[i], "--peer") && i + 1 < argc) peer = argv[++i];
        else if (!strcmp(argv[i], "--mtu") && i + 1 < argc) mtu = argv[++i];
        else if (!strcmp(argv[i], "--gw") && i + 1 < argc) gw = argv[++i];
        else if (!strcmp(argv[i], "--exclude") && i + 1 < argc) exclude = argv[++i];
        else if (!strcmp(argv[i], "--dns") && i + 1 < argc) dns = argv[++i];
        else if (!strcmp(argv[i], "--name") && i + 1 < argc) ifname = argv[++i];
        else if (!strcmp(argv[i], "--clear-blackhole")) drop_bh = 1;
        else if (!strcmp(argv[i], "--down")) down = 1;
        else if (!strcmp(argv[i], "--overlay")) overlay_only = 1;
        else if (!strcmp(argv[i], "--restore")) restore = 1;
        else if (!strcmp(argv[i], "--reap")) reap = 1;
        else if (!strcmp(argv[i], "--kill") && i + 1 < argc) {
            do_kill = 1;
            kill_pid = (pid_t)atoi(argv[++i]);
        }
        else if (!strcmp(argv[i], "--force")) force = 1;
        else if (!strcmp(argv[i], "--hold")) hold = 1;
        else if (!strcmp(argv[i], "--default")) want_default = 1;
        else if (!strcmp(argv[i], "--set-default")) set_default = 1;
        else if (!strcmp(argv[i], "--clear-default")) clear_default = 1;
        else if (!strcmp(argv[i], "--rebind-exclude")) rebind = 1;
        else if (!strcmp(argv[i], "--pin")) pin = 1;
        else if (!strcmp(argv[i], "--discover-gw")) discover = 1;
        else if (!strcmp(argv[i], "--apply-dns")) apply_dns = 1;
        else if (!strcmp(argv[i], "--restore-dns")) restore_dns_only = 1;
        else if (!strcmp(argv[i], "--drop-split")) drop_split = 1;
        else if (!strcmp(argv[i], "--ifdown")) split_ifdown = 1;
        else if (!strcmp(argv[i], "--apply-split")) apply_split = 1;
        else if (!strcmp(argv[i], "--clear-split")) clear_split = 1;
        else if (!strcmp(argv[i], "--keep-direct")) keep_direct = 1;
    }
    if (geteuid() != 0) {
        fprintf(stderr, "loft-priv: need root\n");
        return 1;
    }
    if (discover) {
        char buf[64];
        discover_phys_gw(buf, sizeof buf);
        if (!buf[0]) return 1;
        printf("%s\n", buf);
        return 0;
    }
    if (drop_split && !sock && !down && !restore) {
        if (split_ifdown) drop_split_force();
        else drop_split_default();
        route_unhold();
        route_restore_default(gw);
        unstamp_default();
        fprintf(stderr, "loft-priv: drop-split gw=%s ifdown=%d\n", gw ? gw : "", split_ifdown);
        return 0;
    }
    if (restore_dns_only && !sock && !down && !restore) {
        dns_restore();
        fprintf(stderr, "loft-priv: dns restore\n");
        return 0;
    }
    if (apply_dns && !sock && !down && !restore) {
        dns_apply(dns);
        return 0;
    }
    if (apply_split && !sock && !down && !restore) {
        g_gw = gw;
        split_apply(ifname, gw);
        return 0;
    }
    if (clear_split && !sock && !down && !restore) {
        split_clear(keep_direct);
        return 0;
    }
    if (pin) {
        char buf[64];
        if (!gw || !gw[0] || !gw_on_link(gw)) {
            discover_phys_gw(buf, sizeof buf);
            gw = buf[0] ? buf : NULL;
        }
        g_gw = gw;
        pin_replace(exclude);
        fprintf(stderr, "loft-priv: pin gw=%s\n", gw ? gw : "");
        return 0;
    }
    if (rebind) {
        rebind_excl(gw);
        fprintf(stderr, "loft-priv: rebind exclude gw=%s\n", gw ? gw : "");
        return 0;
    }
    if (do_kill) {
        if (kill_pid > 1) {
            kill(kill_pid, force ? SIGKILL : SIGTERM);
            fprintf(stderr, "loft-priv: kill %d %s\n", (int)kill_pid, force ? "KILL" : "TERM");
        }
        return 0;
    }
    if (reap && !down && !restore && !clear_default && !drop_bh) {
        reap_others(SIGTERM);
        return 0;
    }
    if (down || restore) {
        int touch_default = !overlay_only && (restore || hold || stamped_default());
        /* Restore always drops 0/1 first, even if the stamp is gone. */
        if (restore) touch_default = 1;
        if (touch_default) {
            drop_split_default();
            route_unhold();
        }
        if (ip) {
            char net[32];
            cidr24(ip, net, sizeof net);
            route_del_net(net);
            route_del_host(ip);
            lo_alias(0, ip);
        }
        if (peer) route_del_host(peer);
        route_del_host(TUN_LOCAL);
        each_ip(exclude, del_excl);
        wipe_excl();
        split_clear(hold || overlay_only);
        dns_restore();
        if (hold) route_blackhole();
        else if (touch_default) {
            route_restore_default(gw);
            unstamp_default();
        }
        fprintf(stderr, "loft-priv: %s gw=%s overlay=%d\n",
                hold ? "hold" : (touch_default ? "restore" : "overlay"),
                gw ? gw : "", overlay_only);
        fflush(stderr);
        /* Reap in a child so this --down can exit even if a holder is slow. */
        {
            pid_t k = fork();
            if (k == 0) {
                usleep(150000);
                reap_others(SIGTERM);
                _exit(0);
            }
        }
        return 0;
    }
    if (drop_bh && !sock) {
        route_unhold();
        route_restore_default(gw);
        drop_split_default();
        fprintf(stderr, "loft-priv: unhold gw=%s\n", gw ? gw : "");
        return 0;
    }
    if (clear_default) {
        drop_split_default();
        route_restore_default(gw);
        each_ip(exclude, del_excl);
        wipe_excl();
        unstamp_default();
        fprintf(stderr, "loft-priv: default off\n");
        return 0;
    }
    if (set_default) {
        if (!ifname) {
            fprintf(stderr, "loft-priv: --set-default needs --name\n");
            return 2;
        }
        if (!exclude || !exclude[0]) {
            fprintf(stderr, "loft-priv: refuse 0/1 without exclude pins\n");
            return 1;
        }
        g_gw = gw;
        each_ip(exclude, add_excl);
        drop_split_default();
        if (route_add_split("0.0.0.0/1", ifname, ifname) != 0) return 1;
        if (route_add_split("128.0.0.0/1", ifname, ifname) != 0) return 1;
        stamp_default();
        fprintf(stderr, "loft-priv: default via %s\n", ifname);
        return 0;
    }
    if (!sock || !ip || !peer) {
        fprintf(stderr, "usage: loft-priv --sock PATH --ip A --peer B [--mtu N] [--gw G] [--exclude ip,ip] [--default]\n");
        fprintf(stderr, "       loft-priv --apply-split [--name utunN] [--gw G]\n");
        fprintf(stderr, "       loft-priv --clear-split [--keep-direct]\n");
        return 2;
    }
    tun = loft_utun_open(name);
    if (tun < 0) {
        perror("utun");
        return 1;
    }
    fprintf(stderr, "loft-priv: open %s overlay=%s peer=%s local=%s\n", name, ip, peer, ip);
    if (drop_bh) route_unhold();
    /* A --> A like SSTP Connect's utun. Kernel sources ICMP from the overlay
       IP, so the PPP/loft gateway can reply. Drop a leftover lo0 alias from
       the old dummy-local layout; do not add a new one (duplicate of utun). */
    (void)lo_alias(0, ip);
    if (ifconfig_tun(name, ip, ip, mtu) != 0) {
        loft_utun_close(tun);
        return 1;
    }
    (void)ifconfig_no_inet6(name);
    {
        char net[32];
        cidr24(ip, net, sizeof net);
        route_del_net(net);
        if (route_add_split(net, name, peer) == 0)
            fprintf(stderr, "loft-priv: overlay %s via %s\n", net, name);
        else
            fprintf(stderr, "loft-priv: overlay %s failed\n", net);
    }
    if (want_default) {
        /* IPv4 0/1 split only. Overlay IPv6 later may add inet6 default.
           Pins first: 0/1 without a /32 to the front loops into utun. */
        if (!exclude || !exclude[0]) {
            fprintf(stderr, "loft-priv: refuse 0/1 without exclude pins\n");
            loft_utun_close(tun);
            return 1;
        }
        g_gw = gw;
        each_ip(exclude, add_excl);
        drop_split_default();
        if (route_add_split("0.0.0.0/1", name, peer) != 0) {
            loft_utun_close(tun);
            return 1;
        }
        if (route_add_split("128.0.0.0/1", name, peer) != 0) {
            loft_utun_close(tun);
            return 1;
        }
        stamp_default();
        fprintf(stderr, "loft-priv: default via %s (%s)\n", name, peer);
    } else {
        fprintf(stderr, "loft-priv: overlay only\n");
    }
    if (dns && dns[0]) dns_apply(dns);
    if (send_fd(sock, tun, name) != 0) {
        if (want_default) drop_split_default();
        loft_utun_close(tun);
        return 1;
    }
    /* Keep the original utun fd until SIGTERM. Darwin utun stays up
       while any fd remains (loft-front's SCM_RIGHTS copy). On signal,
       drop lo0 alias and overlay routes — SIGTERM otherwise never
       reaches the lines after pause(). */
    snprintf(g_ip, sizeof g_ip, "%s", ip);
    snprintf(g_peer, sizeof g_peer, "%s", peer);
    if (gw && gw[0]) snprintf(g_gwbuf, sizeof g_gwbuf, "%s", gw);
    g_did_default = want_default;
    g_tun = tun;
    {
        struct sigaction sa;
        memset(&sa, 0, sizeof sa);
        sa.sa_handler = on_hold_sig;
        sigemptyset(&sa.sa_mask);
        sigaction(SIGTERM, &sa, NULL);
        sigaction(SIGINT, &sa, NULL);
        sigaction(SIGHUP, &sa, NULL);
    }
    fprintf(stderr, "loft-priv: hold %s fd=%d\n", name, tun);
    fflush(stderr);
    stamp_hold();
    while (!stop_hold) pause();
    hold_cleanup();
    fprintf(stderr, "loft-priv: down %s\n", name);
    return 0;
}
