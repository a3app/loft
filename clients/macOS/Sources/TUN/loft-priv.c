/* Setuid helper: open utun, ifconfig, split default, pass fd via SCM_RIGHTS.
   fork/exec /sbin/ifconfig and /sbin/route. system() from setuid drops euid.

   loft-priv is the only part of the macOS client that runs as root. The menu
   bar app (Engine/Priv.swift) installs it once as setuid root and then runs
   it with command-line flags for each privileged job:
   - "holder" mode (--sock): create a utun (virtual tunnel interface, see
     utun.h), give it the overlay IP (the address inside the loft network),
     add routes, send the open utun fd to the unprivileged session helper
     loft-front over a unix socket, then stay alive holding the interface
     until SIGTERM, and clean up on exit;
   - one-shot modes: install or remove the "split default" route, pin hosts,
     apply split-tunnel prefix lists, set or restore system DNS, tidy up after
     a crash, or find the real (non-tunnel) gateway.
   Key terms used below:
   - split default: routes 0.0.0.0/1 and 128.0.0.0/1 into the utun. Together
     they cover all of IPv4 and are more specific than the normal 0.0.0.0/0
     default, so they win without deleting it; removing them restores the
     original path at once.
   - front: the loft server endpoint the session connects to. Its address is
     "pinned" (a /32 host route via the real gateway) so the tunnel's own
     traffic does not loop back into the tunnel.
   - stamp: a small file in /tmp or /var/run that records what this helper
     changed, so a later run (even after a crash) can undo exactly that.
   Linked with utun.c. */
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

/* Defined further down, next to the other gateway helpers. */
static int gw_on_link(const char *gw);
static void discover_phys_gw(char *out, size_t n);
static int first_phys_if(char *out, size_t n);

/* runv: fork/execv `path`; keep euid 0. ignore non-zero if `ignore`.
   Runs a system tool (ifconfig, route, networksetup) directly, without a
   shell: system() goes through /bin/sh, which drops root when the real user
   is not root, and a shell would also parse arguments. In the child, setuid(0)
   makes the real uid root too so the tool sees a full root process.
   Returns 0 on success (or always when `ignore`), -1 on failure, logging
   the failed command to stderr unless ignored. */
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

/* Link-local address that an older layout used as the utun's local end.
   Still removed during cleanup in case a leftover route points at it, and
   never accepted as a real gateway. */
#define TUN_LOCAL "169.254.8.1"

/* ifconfig_tun: p2p IPv4 on utun. NEPacketTunnel / SSTP Connect style is
   overlay IP as both ends (A --> A) so the kernel sources from that address.
   A utun is a point-to-point link with a local and a peer address; using the
   overlay IP for both makes outgoing packets carry the overlay IP as source,
   which is what the loft network expects. Also sets the MTU and brings it up. */
static int ifconfig_tun(const char *name, const char *local, const char *peer, const char *mtu) {
    char *argv[] = {
        "ifconfig", (char *)name, "inet", (char *)local, (char *)peer,
        "netmask", "255.255.255.255", "mtu", (char *)mtu, "up", NULL
    };
    return runv(0, "/sbin/ifconfig", argv);
}

/* ifconfig_no_inet6: IPv4-only until overlay IPv6 is assigned. Do not install ::/0.
   Turns IPv6 off on the utun so macOS does not give it an IPv6 address and
   try to send IPv6 into a tunnel that cannot carry it yet. */
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

/* is_split_dest: Darwin prints 0/1 or 0.0.0.0/1 (and the 128.0 half).
   True if a destination column from `netstat -rn` is one half of the split
   default, in either of the spellings macOS uses. */
static int is_split_dest(const char *d) {
    return d && (!strcmp(d, "0/1") || !strcmp(d, "0.0.0.0/1")
        || !strcmp(d, "128.0/1") || !strcmp(d, "128.0.0.0/1"));
}

/* drop_split_default: remove 0/1 and 128/1. Kernel then stops injecting into utun.
   Must run BEFORE killing the utun reader and BEFORE wiping /32 pins.
   Deletes both spellings by name, then scans the routing table for any copy
   left behind (there can be several when sibling sessions each added one). */
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
   Reset Routes uses this so a dead reader cannot keep filling mbufs.
   (mbufs are kernel packet buffers: if nobody reads the utun, packets pile up
   there.) Remembers up to 8 utun names, downs each once, then drops again. */
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
   A leftover pin via a dead hotspot hop makes connect() return EADDRNOTAVAIL.
   A "pin" is a /32 host route that keeps traffic to one address (usually the
   front) on the real network instead of the tunnel. "On-link" means the
   gateway is inside a subnet of a physical interface, i.e. reachable now.
   Returns -1 when there is neither a usable gateway nor a physical NIC. */
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



/* cidr24: `a.b.c.0/24` of `ip`. The overlay network is routed as the /24
   around the overlay IP. A string that is not a dotted IPv4 is copied as is. */
static void cidr24(const char *ip, char *out, size_t n) {
    unsigned a, b, c, d;
    if (sscanf(ip, "%u.%u.%u.%u", &a, &b, &c, &d) == 4)
        snprintf(out, n, "%u.%u.%u.0/24", a, b, c);
    else
        snprintf(out, n, "%s", ip);
}

/* route_add_split: add `net` via utun `-interface`, else via `peer`.
   Routing by interface name is preferred; the peer address is the fallback
   for systems where `-interface` fails. Returns 0 if either worked. */
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

/* route_blackhole: hold default via 127.0.0.1 -blackhole.
   Kill-switch used by `--down --hold`: while the tunnel is down, traffic
   is dropped instead of leaking out over the normal network. A
   real default route (if any) stays in place; route_unhold removes this one. */
static int route_blackhole(void) {
    char *argv[] = {
        "route", "-n", "add", "-inet", "0.0.0.0/0", "127.0.0.1", "-blackhole", NULL
    };
    return runv(1, "/sbin/route", argv);
}

/* Stamp file: exists while loft's IPv6 reject route (below) is installed. */
#define IPV6_DROP_STAMP "/tmp/loft-ipv6-drop"

/* ipv6_drop_global: reject 2000::/3 so Happy Eyeballs gets ENETUNREACH
   and uses IPv4 through loft. Overlay is IPv4-only until Status.IP6;
   a native inet6 default would send Global destinations around loft.
   Link-local and ULA stay. reject, not blackhole: silent drop delays HE. */
static void ipv6_drop_global(void) {
    char *argv[] = {
        "route", "-n", "add", "-inet6", "2000::/3", "::1", "-reject", NULL
    };
    if (runv(1, "/sbin/route", argv) == 0) {
        FILE *fp = fopen(IPV6_DROP_STAMP, "w");
        if (fp) {
            fputs("2000::/3\n", fp);
            fclose(fp);
        }
        fprintf(stderr, "loft-priv: inet6 2000::/3 reject\n");
    }
}

/* ipv6_undrop_global: remove the 2000::/3 reject route and its stamp, so
   native IPv6 works again. Safe to call when it is not installed. */
static void ipv6_undrop_global(void) {
    char *argv[] = {
        "route", "-n", "delete", "-inet6", "2000::/3", "::1", NULL
    };
    runv(1, "/sbin/route", argv);
    unlink(IPV6_DROP_STAMP);
}

/* route_unhold: delete only the 127.0.0.1 blackhole, not a real 0/0. */
static int route_unhold(void) {
    /* Only the blackhole we installed. A bare delete of 0/0 can drop the real default. */
    char *argv[] = {
        "route", "-n", "delete", "-inet", "0.0.0.0/0", "127.0.0.1", NULL
    };
    return runv(1, "/sbin/route", argv);
}

/* Path of this holder's stamp file, empty when there is none. */
static char hold_stamp[64];

/* stamp_hold: /tmp/loft-hold.<pid> so --reap can find this holder.
   The file is empty; the process id in its name is the information. */
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

/* comm_is_priv: true if pid's executable basename is loft-priv.
   Guards every kill: a stale stamp may carry a pid that the system has
   since given to an unrelated process. */
static int comm_is_priv(pid_t pid) {
    char path[PROC_PIDPATHINFO_MAXSIZE];
    const char *base;
    if (proc_pidpath(pid, path, sizeof path) <= 0) return 0;
    base = strrchr(path, '/');
    base = base ? base + 1 : path;
    return strcmp(base, "loft-priv") == 0;
}

/* Holders are started with --sock. --down/--reap/--kill are not. */
/* argv_has_sock: true if pid was started with --sock (a holder).
   Reads the process's arguments with sysctl KERN_PROCARGS2. That buffer
   starts with argc (an int), then the executable path, padding NULs, and
   the NUL-separated arguments, which are scanned for "--sock". */
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
/* reap_others: SIGTERM leftover holders (hold stamps and --sock argv).
   Finds holders two ways, because a crash can leave either one missing:
   1. every /tmp/loft-hold.<pid> stamp (the stamp is deleted as well);
   2. every running process whose arguments include --sock.
   kill_holder makes sure only real loft-priv processes are signalled. */
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

/* DNS_SAVE: the DNS servers each macOS "Network Service" (Wi-Fi, Ethernet,
   ...) had before loft changed them, one "service|ip ip ..." line each
   ("Empty" = automatic/DHCP). g_did_dns: this process changed DNS. */
#define DNS_SAVE "/var/run/loft-dns.prev"
static int g_did_dns;

/* dns_capture: run `path` and copy stdout into `out`.
   Like runv, but keeps the tool's output (NUL-terminated, cut at n-1
   bytes). Despite the name it is also used for netstat and ipconfig.
   Returns 0 only if the tool exited with status 0. */
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

/* dns_set_svc: networksetup -setdnsservers for one service.
   `ips` is a NULL-terminated list; NULL or empty passes "Empty", which tells
   macOS to go back to the servers it gets automatically (DHCP). */
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

/* dns_restore: replay DNS_SAVE and unlink it. Unused on the live overlay-DNS-off path.
   Puts every service back to the servers recorded before loft's first
   change. Does nothing if there is no save file. */
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

/* dns_apply: push csv nameservers onto every Network Service; save previous.
   Used when a connection asks for system DNS through the tunnel. Takes up
   to 8 servers. The previous settings are saved only if no save file exists
   yet, so applying twice never overwrites the true pre-loft values.
   Returns 0 (also for an empty list), -1 if services cannot be listed. */
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
    /* 1. Parse the comma-separated server list (trimming leading spaces). */
    if (nips == 0) {
        free(dup);
        return 0;
    }
    /* 2. List all network services; first line is an explanatory header,
       and lines starting with '*' are disabled services, which are skipped. */
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
        /* 3. Per service: save its current servers (first time only), then
           set the new ones. */
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

/* Stamp file: exists while a loft split default (0/1 + 128/1) is installed. */
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
   router, else `-interface` of the first physical NIC.
   Needed because after the split default is dropped, macOS may be left with
   a default route via the utun, or none at all. `gw` is the gateway the app
   remembered from before loft; it is replaced when it is missing, is the old
   TUN_LOCAL, is an interface placeholder ("link#N"), or is no longer on-link.
   Always returns 0: failures are only logged. */
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

/* send_fd: connect to unix sock and pass the utun fd via SCM_RIGHTS.
   `path` is the socket the session helper (loft-front) listens on. The
   message carries the 16-byte "utunN" name as normal data and the fd as
   ancillary data; the receiver gets its own copy of the open descriptor.
   Mirrors loft_utun_recv_fd in utun.c. Returns 0 or -1. */
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

/* Gateway that add_excl pins through (set by the mode that calls it). */
static const char *g_gw;

/* Holder state, kept in globals so hold_cleanup can undo the setup after a
   signal: overlay IP, peer, pre-loft gateway, the utun fd, whether this
   holder installed the split default, and the "stop now" flag. */
static char g_ip[64], g_peer[64], g_gwbuf[64];
static int g_tun = -1, g_did_default;
static volatile sig_atomic_t stop_hold;
static void wipe_excl(void);

/* on_hold_sig: SIGTERM/INT/HUP while holding utun.
   Only sets a flag: almost nothing is safe inside a signal handler, so the
   main loop wakes from pause() and does the cleanup itself. */
static void on_hold_sig(int sig) {
    (void)sig;
    stop_hold = 1;
}

/* hold_cleanup: drop overlay routes, restore 0/0 if we installed it, close utun.
   Runs once when a holder is told to stop. Also removes the IPv6 reject
   route, restores DNS if this holder changed it, wipes front pins and
   deletes the hold stamp. */
static void hold_cleanup(void) {
    char net[32];
    ipv6_undrop_global();
    if (g_ip[0]) {
        cidr24(g_ip, net, sizeof net);
        route_del_net(net);
        route_del_host(g_ip);
        lo_alias(0, g_ip);
    }
    if (g_peer[0]) route_del_host(g_peer);
    route_del_host(TUN_LOCAL);
    /* --set-default is a different process and only leaves the stamp.
       This holder drops 0/1 only if it installed the split itself.
       A sibling utun's exit must not steal another session's default. */
    if (g_did_default) {
        drop_split_default();
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

/* Stamp file: one pinned host IP per line ("exclude" = excluded from the
   tunnel). Shared by all sessions, so each removes only its own entries. */
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

/* load_excl_list: host IPs from EXCL_STAMP. Skips blank and '#' lines;
   returns how many were read (at most `max`). */
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

/* first_phys_if: name of an UP IPv4 NIC (prefer en*), skipping utun/lo/feth.
   en* are Wi-Fi and Ethernet; utun are tunnels (loft's or other VPNs'), lo
   is loopback and feth are virtual "fake ethernet" pairs. Returns 0 and
   fills `out`, or -1 when no such interface is up. */
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

/* ipconfig_router: DHCP/static router for `iff`, or 0.
   Asks macOS for the router configured on one interface. Unlike the routing
   table this answer is not affected by loft's own routes. Returns 1 and
   fills `out` with a valid IPv4, else 0. */
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

/* gw_on_link: true if `gw` is on a physical IPv4 subnet (not utun/lo/feth).
   In other words: the address is directly reachable on a real network right
   now. A gateway remembered from a network the Mac has since left (e.g. a
   phone hotspot) fails this test. */
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

/* unstamp_excl: drop listed pins from the stamp, keep sibling sessions' pins. */
static void unstamp_excl(const char *exclude) {
    char old[64][64], gone[64][64], keep[64][64];
    int nold, ngone, nkeep = 0, i;
    nold = load_excl_list(old, 64);
    ngone = parse_ip_csv(exclude, gone, 64);
    for (i = 0; i < nold; i++) {
        if (ip_in_list(gone, ngone, old[i])) continue;
        snprintf(keep[nkeep], 64, "%s", old[i]);
        nkeep++;
    }
    rewrite_excl_stamp(keep, nkeep);
}

/* Split tunneling: per-prefix lists of networks that go "via" loft (into
   the utun) or "direct" (around loft, via the physical gateway). The app
   (Engine/Priv.swift) writes the wanted lists to the *want* files; after
   applying them this helper saves what it installed in the *have* files and
   which utun and gateway it used in SPLIT_META, so the next run only adds
   and deletes the difference. SPLIT_MAX caps the entries per list.
   RT_ROUNDUP pads a sockaddr to the 4-byte boundary that routing-socket
   messages require. */
#define SPLIT_MAX 65536
#define SPLIT_WANT_VIA "/tmp/loft-split-want-via"
#define SPLIT_WANT_DIRECT "/tmp/loft-split-want-direct"
#define SPLIT_HAVE_VIA "/tmp/loft-split-via"
#define SPLIT_HAVE_DIRECT "/tmp/loft-split-direct"
#define SPLIT_META "/tmp/loft-split-meta"
#define RT_ROUNDUP(a) ((a) > 0 ? (1 + (((a) - 1) | (sizeof(uint32_t) - 1))) : sizeof(uint32_t))

/* cidr4: one IPv4 prefix. `net` is the network address in host byte order
   with the host bits cleared; `bits` is the prefix length (0..32). */
struct cidr4 {
    uint32_t net;
    uint8_t bits;
};

/* Routing socket (opened on first use by rt_fd) and its message counter. */
static int g_rtfd = -1;
static int g_rtseq;

/* cidr_cmp: qsort order for cidr4, by network then prefix length. The split
   lists are kept sorted this way so split_diff can merge them in one pass. */
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

/* cidr_skip: default halves, loopback, link-local, multicast.
   Prefixes a split list must never touch: 0/0 and the 0/1 + 128/1 split
   default are managed separately, and 127/8, 169.254/16 and 224/4 would
   break the Mac's own local networking. Returns 1 to skip. */
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

/* cidr_fmt: cidr4 back to "a.b.c.d/n" text. */
static void cidr_fmt(const struct cidr4 *c, char *out, size_t n) {
    snprintf(out, n, "%u.%u.%u.%u/%u",
        (c->net >> 24) & 255, (c->net >> 16) & 255,
        (c->net >> 8) & 255, c->net & 255, c->bits);
}

/* load_cidrs: unique IPv4 prefixes from `path`.
   One prefix per line; blank lines, '#' comments, bad lines and cidr_skip
   prefixes are ignored. Result is sorted and de-duplicated. A missing file
   counts as an empty list. Returns the count (at most `cap`). */
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

/* save_cidrs: write prefixes to `path`, one per line (the *have* files). */
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

/* rt_fd: the PF_ROUTE socket, opened once and reused. Writing messages to
   it changes the kernel routing table directly, which is much faster than
   running /sbin/route once per prefix for lists of thousands. */
static int rt_fd(void) {
    if (g_rtfd >= 0) return g_rtfd;
    g_rtfd = socket(PF_ROUTE, SOCK_RAW, AF_UNSPEC);
    return g_rtfd;
}

/* rt_mod: RTM_ADD / RTM_DELETE one IPv4 prefix via gw or `-interface` iff.
   Builds a routing-socket message by hand: a header followed by the
   destination, the gateway (a link-level address naming the interface, or
   an IPv4 address) and, unless it is a /32 host route, the netmask.
   "Already exists" and "not found" count as success so callers can be
   idempotent. Returns 0, or -1 on bad input or a real error. */
static int rt_mod(int cmd, const struct cidr4 *c, const char *gw, const char *iff) {
    uint8_t buf[256];
    struct rt_msghdr *m;
    uint8_t *p;
    struct sockaddr_in sin;
    int addrs = RTA_DST;
    int flags = RTF_UP | RTF_STATIC;
    unsigned idx = 0;
    int fd, n, tries = 0, replaced = 0;
    /* 1. Destination address right after the header. */
    memset(buf, 0, sizeof buf);
    m = (struct rt_msghdr *)buf;
    p = buf + sizeof(*m);
    memset(&sin, 0, sizeof sin);
    sin.sin_len = sizeof sin;
    sin.sin_family = AF_INET;
    sin.sin_addr.s_addr = htonl(c->net);
    memcpy(p, &sin, sizeof sin);
    p += RT_ROUNDUP(sizeof sin);
    /* 2. Gateway: the interface itself (via loft) or a router IP (direct). */
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
    /* 3. Netmask, or the host flag for a single address. */
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
    /* 4. Fill in the header and send; retry briefly if the kernel is out of
       buffers (ENOBUFS), which happens when adding many routes quickly. */
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
        /* Via ADD onto a leftover native hop: delete the dest, then retry.
           Treating EEXIST as success left through-loft prefixes around loft. */
        if (cmd == RTM_ADD && errno == EEXIST && iff && iff[0] && replaced == 0) {
            char net[64];
            cidr_fmt(c, net, sizeof net);
            (void)route_del_net(net);
            replaced = 1;
            m->rtm_seq = ++g_rtseq;
            continue;
        }
        if (errno == EEXIST || errno == ESRCH || errno == EADDRNOTAVAIL) return 0;
        if (errno == ENOBUFS && tries++ < 8) {
            usleep(2000);
            continue;
        }
        return -1;
    }
}

/* split_diff: delete have-not-want and/or add want-not-have.
   Both lists are sorted (cidr_cmp), so one merge-style walk finds the
   difference. Counters in *added / *deleted only grow on success.
   Callers run deletes on both bags first, then adds, so a prefix
   moving via↔direct is gone from the old hop before the new ADD. */
static void split_diff(const struct cidr4 *have, int nh,
    const struct cidr4 *want, int nw,
    const char *gw, const char *iff, int *added, int *deleted,
    int do_del, int do_add) {
    int i = 0, j = 0;
    while (i < nh || j < nw) {
        int cmp;
        if (i == nh) cmp = 1;
        else if (j == nw) cmp = -1;
        else cmp = cidr_cmp(&have[i], &want[j]);
        if (cmp < 0) {
            if (do_del && rt_mod(RTM_DELETE, &have[i], gw, iff) == 0) (*deleted)++;
            i++;
        } else if (cmp > 0) {
            if (do_add && rt_mod(RTM_ADD, &want[j], gw, iff) == 0) (*added)++;
            j++;
        } else {
            i++;
            j++;
        }
    }
}

/* split_wipe: delete every prefix in `have` from the given hop. */
static void split_wipe(const struct cidr4 *have, int n, const char *gw, const char *iff, int *deleted) {
    int i;
    for (i = 0; i < n; i++) {
        if (rt_mod(RTM_DELETE, &have[i], gw, iff) == 0) (*deleted)++;
    }
}

/* split_read_meta: utun name and gateway the installed split lists use
   ("if=" and "gw=" lines in SPLIT_META); empty strings if unknown. */
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

/* split_write_meta: record the utun and gateway now in use. */
static void split_write_meta(const char *iff, const char *gw) {
    FILE *fp = fopen(SPLIT_META, "w");
    if (!fp) return;
    fprintf(fp, "if=%s\ngw=%s\n", iff ? iff : "", gw ? gw : "");
    fclose(fp);
}

/* cidr_eq: same IPv4 prefix. */
static int cidr_eq(const struct cidr4 *a, const struct cidr4 *b) {
    return a->net == b->net && a->bits == b->bits;
}

/* drop_via_from_direct: equal prefix, through-loft wins.
   Removes from the direct list every prefix that is also in the via list,
   compacting in place; returns the new direct count. */
static int drop_via_from_direct(struct cidr4 *dir, int nd, const struct cidr4 *via, int nv) {
    int w = 0, i, j;
    for (i = 0; i < nd; i++) {
        int hit = 0;
        for (j = 0; j < nv; j++) {
            if (cidr_eq(&dir[i], &via[j])) { hit = 1; break; }
        }
        if (!hit) dir[w++] = dir[i];
    }
    return w;
}

/* split_apply: install want-via via utun and want-direct via native gw.
   Direct is applied first so a prefix moving to via is deleted off the
   native hop before RTM_ADD to utun (EEXIST would otherwise keep it around loft).
   If the utun is gone, or the gateway is unusable and none can be found,
   that list is emptied (its routes removed). If the utun or gateway changed
   since last time, the old routes are wiped and re-added on the new hop. */
static void split_apply(const char *iff, const char *gw) {
    static struct cidr4 have_via[SPLIT_MAX], want_via[SPLIT_MAX];
    static struct cidr4 have_dir[SPLIT_MAX], want_dir[SPLIT_MAX];
    char old_if[IFNAMSIZ], old_gw[64];
    int nv_h, nv_w, nd_h, nd_w;
    int add_v = 0, del_v = 0, add_d = 0, del_d = 0;
    int via_ok, dir_ok, rebind_via, rebind_dir;
    /* 1. Load what is installed, what is wanted, and the old hops. */
    split_read_meta(old_if, sizeof old_if, old_gw, sizeof old_gw);
    nv_h = load_cidrs(SPLIT_HAVE_VIA, have_via, SPLIT_MAX);
    nv_w = load_cidrs(SPLIT_WANT_VIA, want_via, SPLIT_MAX);
    nd_h = load_cidrs(SPLIT_HAVE_DIRECT, have_dir, SPLIT_MAX);
    nd_w = load_cidrs(SPLIT_WANT_DIRECT, want_dir, SPLIT_MAX);
    nd_w = drop_via_from_direct(want_dir, nd_w, want_via, nv_w);
    /* 2. Check that the utun exists and the gateway is reachable. */
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
    /* 3. Hop changed or unusable: remove everything on the old hop. */
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
    /* 4. All deletes first, then all adds (see the ordering note above). */
    if (dir_ok) split_diff(have_dir, nd_h, want_dir, nd_w, gw, NULL, &add_d, &del_d, 1, 0);
    if (via_ok) split_diff(have_via, nv_h, want_via, nv_w, NULL, iff, &add_v, &del_v, 1, 0);
    if (dir_ok) split_diff(have_dir, nd_h, want_dir, nd_w, gw, NULL, &add_d, &del_d, 0, 1);
    if (via_ok) split_diff(have_via, nv_h, want_via, nv_w, NULL, iff, &add_v, &del_v, 0, 1);
    /* 5. Remember the new state for the next run. */
    save_cidrs(SPLIT_HAVE_VIA, want_via, nv_w);
    save_cidrs(SPLIT_HAVE_DIRECT, want_dir, nd_w);
    split_write_meta(via_ok ? iff : "", dir_ok ? gw : "");
    fprintf(stderr, "loft-priv: split via +%d -%d n=%d if=%s direct +%d -%d n=%d gw=%s\n",
        add_v, del_v, nv_w, via_ok ? iff : "-",
        add_d, del_d, nd_w, dir_ok ? gw : "-");
}

/* split_clear: drop stamped extra CIDRs. keep_direct leaves around-loft pins.
   Via routes always go (they point into a utun that is going away). Direct
   routes are kept when another session or a hold may still need them. */
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

/* rebind_excl: move stamped pins onto a live physical hop (Wi-Fi after iPhone).
   Called after the Mac changes network: re-adds every pinned front address
   via the current gateway (add_excl deletes the old pin first). */
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

/* main: --sock hold, --down/--restore/--hold, --set-default/--clear-default, --kill/--reap.
   Parses all flags, refuses to run without root, then runs exactly one mode;
   the order of the checks below decides which flag wins. One-shot modes
   return at once. With none of them, it becomes a holder: --sock, --ip and
   --peer are then required. Exit code 0 = done, 1 = failed, 2 = bad usage. */
int main(int argc, char **argv) {
    const char *sock = NULL, *ip = NULL, *peer = NULL, *mtu = "1280", *gw = NULL, *exclude = NULL, *ifname = NULL, *dns = NULL;
    int i, tun, drop_bh = 0, down = 0, hold = 0, want_default = 0, set_default = 0, clear_default = 0;
    int do_kill = 0, force = 0, reap = 0, restore = 0, overlay_only = 0, rebind = 0, pin = 0, discover = 0;
    int apply_dns = 0, restore_dns_only = 0, drop_split = 0, split_ifdown = 0;
    int apply_split = 0, clear_split = 0, keep_direct = 0, cleanup_leftover = 0;
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
        else if (!strcmp(argv[i], "--cleanup-leftover")) cleanup_leftover = 1;
        else if (!strcmp(argv[i], "--apply-split")) apply_split = 1;
        else if (!strcmp(argv[i], "--clear-split")) clear_split = 1;
        else if (!strcmp(argv[i], "--keep-direct")) keep_direct = 1;
    }
    if (geteuid() != 0) {
        fprintf(stderr, "loft-priv: need root\n");
        return 1;
    }
    /* --discover-gw: print the real (non-tunnel) gateway for the app. */
    if (discover) {
        char buf[64];
        discover_phys_gw(buf, sizeof buf);
        if (!buf[0]) return 1;
        printf("%s\n", buf);
        return 0;
    }
    if (cleanup_leftover && !sock && !down && !restore) {
        /* Boot: undo loft leftovers. Never ifconfig-down a foreign utun.
           0/1 is dropped only when loft stamped that it installed the split. */
        int ours = stamped_default();
        if (ours) {
            drop_split_default();
            route_restore_default(gw);
            unstamp_default();
        }
        route_unhold();
        ipv6_undrop_global();
        dns_restore();
        wipe_excl();
        reap_others(SIGTERM);
        fprintf(stderr, "loft-priv: cleanup leftover gw=%s stamp=%d\n",
            gw ? gw : "", ours);
        return 0;
    }
    /* --drop-split: take the split default off (optionally downing a stuck
       utun) and put the normal default route back. */
    if (drop_split && !sock && !down && !restore) {
        if (split_ifdown) drop_split_force();
        else drop_split_default();
        route_unhold();
        route_restore_default(gw);
        unstamp_default();
        fprintf(stderr, "loft-priv: drop-split gw=%s ifdown=%d\n", gw ? gw : "", split_ifdown);
        return 0;
    }
    /* DNS, split-list and pin jobs, each a single step. */
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
    /* --kill PID [--force]: the app cannot signal root holders itself. */
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
    /* --down / --restore: tear a session's routes down. --overlay limits it
       to one session; --hold leaves the kill-switch blackhole in place. */
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
        if (overlay_only) unstamp_excl(exclude);
        else wipe_excl();
        split_clear(hold || overlay_only);
        dns_restore();
        if (hold) {
            route_blackhole();
            ipv6_drop_global();
        } else {
            ipv6_undrop_global();
            if (touch_default) {
                route_restore_default(gw);
                unstamp_default();
            }
        }
        fprintf(stderr, "loft-priv: %s gw=%s overlay=%d\n",
                hold ? "hold" : (touch_default ? "restore" : "overlay"),
                gw ? gw : "", overlay_only);
        fflush(stderr);
        /* Full down reaps stragglers. --overlay is one session; sibling holders stay. */
        if (!overlay_only) {
            pid_t k = fork();
            if (k == 0) {
                usleep(150000);
                reap_others(SIGTERM);
                _exit(0);
            }
        }
        return 0;
    }
    /* --clear-blackhole without --sock: lift the kill-switch. */
    if (drop_bh && !sock) {
        route_unhold();
        route_restore_default(gw);
        drop_split_default();
        ipv6_undrop_global();
        fprintf(stderr, "loft-priv: unhold gw=%s\n", gw ? gw : "");
        return 0;
    }
    /* --clear-default / --set-default: manage the split default for a utun
       held by another process (--name). Pins come first, see below. */
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
        fprintf(stderr, "       loft-priv --cleanup-leftover [--gw G]\n");
        return 2;
    }
    /* Holder mode. 1. Create the utun and configure its address. */
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
    /* 2. Route the overlay /24 into the utun. */
    (void)ifconfig_no_inet6(name);
    ipv6_drop_global();
    {
        char net[32];
        cidr24(ip, net, sizeof net);
        route_del_net(net);
        if (route_add_split(net, name, peer) == 0)
            fprintf(stderr, "loft-priv: overlay %s via %s\n", net, name);
        else
            fprintf(stderr, "loft-priv: overlay %s failed\n", net);
    }
    /* 3. Optional full tunnel: pin the fronts, then add the split default. */
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
    /* 4. Optional DNS, then hand the fd to the session helper. */
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
    /* 5. Remember what to undo, install signal handlers, wait, clean up. */
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
