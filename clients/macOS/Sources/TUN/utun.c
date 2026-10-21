/* Darwin utun: PF_SYSTEM socket plus SCM_RIGHTS fd pass from loft-priv. */
#include "utun.h"
#include <sys/socket.h>
#include <sys/ioctl.h>
#include <sys/kern_control.h>
#include <sys/sys_domain.h>
#include <sys/types.h>
#include <net/if_utun.h>
#include <unistd.h>
#include <string.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <sys/stat.h>

/* loft_utun_open: kernel-picked utun; name_out is "utunN". Returns fd or -1. */
int loft_utun_open(char name_out[16]) {
    struct ctl_info ci;
    struct sockaddr_ctl sc;
    char ifname[16];
    socklen_t ifnlen = sizeof(ifname);
    int fd;

    fd = socket(PF_SYSTEM, SOCK_DGRAM, SYSPROTO_CONTROL);
    if (fd < 0) return -1;
    memset(&ci, 0, sizeof ci);
    strncpy(ci.ctl_name, UTUN_CONTROL_NAME, sizeof(ci.ctl_name) - 1);
    if (ioctl(fd, CTLIOCGINFO, &ci) < 0) { close(fd); return -1; }
    memset(&sc, 0, sizeof sc);
    sc.sc_len = sizeof sc;
    sc.sc_family = AF_SYSTEM;
    sc.ss_sysaddr = AF_SYS_CONTROL;
    sc.sc_id = ci.ctl_id;
    sc.sc_unit = 0; /* kernel picks */
    if (connect(fd, (struct sockaddr *)&sc, sizeof sc) < 0) { close(fd); return -1; }
    if (getsockopt(fd, SYSPROTO_CONTROL, UTUN_OPT_IFNAME, ifname, &ifnlen) < 0) {
        close(fd); return -1;
    }
    if (name_out) {
        memset(name_out, 0, 16);
        strncpy(name_out, ifname, 15);
    }
    return fd;
}

/* loft_utun_read: strips the 4-byte AF header; copies IPv4/IPv6 into `ip`. */
int loft_utun_read(int fd, uint8_t *ip, size_t cap) {
    uint8_t buf[2048];
    ssize_t n;
    if (cap == 0) return 0;
    n = read(fd, buf, cap + 4 > sizeof buf ? sizeof buf : cap + 4);
    if (n < 5) return (int)n;
    n -= 4;
    if ((size_t)n > cap) n = (ssize_t)cap;
    memcpy(ip, buf + 4, (size_t)n);
    return (int)n;
}

/* loft_utun_write: prepends AF_INET/AF_INET6 from the IP version nibble. */
int loft_utun_write(int fd, const uint8_t *ip, size_t n) {
    uint8_t buf[2048];
    if (n == 0 || n + 4 > sizeof buf) return -1;
    memset(buf, 0, 4);
    if ((ip[0] >> 4) == 4) buf[3] = AF_INET;
    else if ((ip[0] >> 4) == 6) buf[3] = AF_INET6;
    else return -1;
    memcpy(buf + 4, ip, n);
    if (write(fd, buf, n + 4) < (ssize_t)(n + 4)) return -1;
    return (int)n;
}

/* loft_utun_close: close(2) if fd >= 0. */
void loft_utun_close(int fd) {
    if (fd >= 0) close(fd);
}

#include <sys/un.h>
#include <sys/select.h>

/* loft_utun_listen: AF_UNIX listen socket at sockpath (mode 0600). */
int loft_utun_listen(const char *sockpath) {
    int fd;
    struct sockaddr_un un;
    unlink(sockpath);
    fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return -1;
    memset(&un, 0, sizeof un);
    un.sun_family = AF_UNIX;
    strncpy(un.sun_path, sockpath, sizeof(un.sun_path) - 1);
    if (bind(fd, (struct sockaddr *)&un, sizeof un) < 0) { close(fd); return -1; }
    if (listen(fd, 1) < 0) { close(fd); return -1; }
    chmod(sockpath, 0600);
    return fd;
}

/* loft_utun_recv_fd: accept + recvmsg SCM_RIGHTS; sets O_NONBLOCK on the tun fd. */
int loft_utun_recv_fd(int lfd, char name_out[16], int timeout_ms) {
    fd_set rs;
    struct timeval tv;
    int cfd, tun = -1;
    struct msghdr msg;
    struct iovec iov;
    char name[16];
    char cbuf[CMSG_SPACE(sizeof(int))];
    struct cmsghdr *c;
    FD_ZERO(&rs);
    FD_SET(lfd, &rs);
    tv.tv_sec = timeout_ms / 1000;
    tv.tv_usec = (timeout_ms % 1000) * 1000;
    if (select(lfd + 1, &rs, NULL, NULL, &tv) <= 0) return -1;
    cfd = accept(lfd, NULL, NULL);
    if (cfd < 0) return -1;
    memset(&msg, 0, sizeof msg);
    memset(name, 0, sizeof name);
    iov.iov_base = name;
    iov.iov_len = sizeof name;
    msg.msg_iov = &iov;
    msg.msg_iovlen = 1;
    msg.msg_control = cbuf;
    msg.msg_controllen = sizeof cbuf;
    if (recvmsg(cfd, &msg, 0) <= 0) { close(cfd); return -1; }
    close(cfd);
    for (c = CMSG_FIRSTHDR(&msg); c; c = CMSG_NXTHDR(&msg, c)) {
        if (c->cmsg_level == SOL_SOCKET && c->cmsg_type == SCM_RIGHTS) {
            memcpy(&tun, CMSG_DATA(c), sizeof tun);
            break;
        }
    }
    if (tun < 0) return -1;
    if (name_out) {
        memset(name_out, 0, 16);
        memcpy(name_out, name, 15);
    }
    {
        int fl = fcntl(tun, F_GETFL, 0);
        if (fl >= 0) fcntl(tun, F_SETFL, fl | O_NONBLOCK);
    }
    return tun;
}
