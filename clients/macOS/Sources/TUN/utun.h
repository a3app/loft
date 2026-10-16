/* utun open/read/write and unix-socket SCM_RIGHTS receive from loft-priv. */

#pragma once
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Open a utun. name_out is "utunN". Returns fd >= 0, or -1. */
int loft_utun_open(char name_out[16]);
int loft_utun_read(int fd, uint8_t *ip, size_t cap);
int loft_utun_write(int fd, const uint8_t *ip, size_t n);
void loft_utun_close(int fd);

/* Listen on a unix socket, wait for SCM_RIGHTS from loft-priv. */
int loft_utun_listen(const char *sockpath);
int loft_utun_recv_fd(int lfd, char name_out[16], int timeout_ms);

#ifdef __cplusplus
}
#endif
