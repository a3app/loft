/* Userspace IPv4 stack: TCP/UDP/ICMP echo over utun, callbacks into Swift. */

#pragma once
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct loft_stack loft_stack;

/* loft_stack_cbs: Swift/C callbacks for mux TCP, UDP, and utun output. */
typedef struct {
    void *ctx;
    void *(*tcp_open)(void *ctx, const char *ip, uint16_t port);
    void (*tcp_ready)(void *ctx, void *cookie);
    int (*tcp_send)(void *ctx, void *cookie, const uint8_t *p, size_t n);
    void (*tcp_close)(void *ctx, void *cookie);
    int (*udp_send)(void *ctx, const char *ip, uint16_t port, const uint8_t *p, size_t n);
    int (*output)(void *ctx, const uint8_t *pkt, size_t n);
} loft_stack_cbs;

/* loft_stack_new: allocate a stack for overlay `our_ip_host` (host order). */
loft_stack *loft_stack_new(uint32_t our_ip_host, uint16_t mtu, loft_stack_cbs cbs);
/* loft_stack_free: RST every TCB and free. */
void loft_stack_free(loft_stack *s);
/* loft_stack_input: IPv4 packet from utun (TCP/UDP/ICMP echo). */
void loft_stack_input(loft_stack *s, const uint8_t *pkt, size_t n);
/* loft_stack_tcp_rx: mux bytes toward the LAN TCP peer. */
void loft_stack_tcp_rx(loft_stack *s, void *cookie, const uint8_t *p, size_t n);
/* loft_stack_tcp_eof: mux closed; send FIN. */
void loft_stack_tcp_eof(loft_stack *s, void *cookie);
/* loft_stack_udp_rx: mux datagram toward the matching NAT slot. */
void loft_stack_udp_rx(loft_stack *s, const char *remote_ip, uint16_t remote_port, const uint8_t *p, size_t n);
/* loft_stack_tick: retransmit unacked TCP; expire idle UDP. */
void loft_stack_tick(loft_stack *s);

#ifdef __cplusplus
}
#endif
