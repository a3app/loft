/* utun open/read/write and unix-socket SCM_RIGHTS receive from loft-priv.

   A utun is the macOS virtual network interface ("utunN") that a VPN uses:
   packets the kernel routes into it can be read from a file descriptor, and
   packets written to that descriptor appear as if they came from the network.
   Only root may create one, so the setuid helper loft-priv (loft-priv.c) opens
   it and hands the descriptor to the unprivileged session over a unix socket
   (SCM_RIGHTS is the socket feature that passes an open fd to another process).
   Implementation: utun.c. */

#pragma once
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Open a utun. name_out is "utunN". Returns fd >= 0, or -1. */
int loft_utun_open(char name_out[16]);
/* Packet I/O on an open utun fd. read/write deal in bare IPv4/IPv6 packets;
   the 4-byte address-family header utun adds is stripped or added here.
   Both return the packet length, or -1 / a short count on error. */
int loft_utun_read(int fd, uint8_t *ip, size_t cap);
int loft_utun_write(int fd, const uint8_t *ip, size_t n);
void loft_utun_close(int fd);

/* Receiving side of the fd hand-off: listen on a unix socket, wait for
   loft-priv to connect and send the utun fd plus its "utunN" name.
   recv_fd returns the fd (set non-blocking), or -1 on timeout or error. */
int loft_utun_listen(const char *sockpath);
int loft_utun_recv_fd(int lfd, char name_out[16], int timeout_ms);

#ifdef __cplusplus
}
#endif
