/* Userspace IPv4 stack: TCP TCB, UDP NAT, ICMP echo; callbacks into TUNEngine. */
#include "stack.h"
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <arpa/inet.h>
#include <sys/time.h>

#define MAX_TCB 256
#define MAX_UDP 128
#define MSS 1200
#define UNACK 16384

/* TCP TCB states. */
enum { ST_FREE = 0, ST_SYN, ST_EST, ST_FINW, ST_LAST };

/* tcb: one TCP connection between LAN and overlay. */
typedef struct {
    int st;
    uint32_t src, dst;
    uint16_t sport, dport;
    uint32_t iss, irs, snd_nxt, snd_una, rcv_nxt;
    void *cookie;
    uint8_t unack[UNACK];
    int unack_n;
    int retries;
    uint64_t last_ms;
} tcb;

/* unat: UDP 5-tuple slot for return traffic. */
typedef struct {
    int used;
    uint32_t src, dst;
    uint16_t sport, dport;
    uint64_t last_ms;
} unat;

/* loft_stack: overlay IP, callbacks, TCB and UDP tables. */
struct loft_stack {
    uint32_t our;
    uint16_t mtu;
    loft_stack_cbs cbs;
    pthread_mutex_t mu;
    tcb t[MAX_TCB];
    unat u[MAX_UDP];
    uint16_t ipid;
};

/* now_ms: wall milliseconds for timeouts. */
static uint64_t now_ms(void) {
    struct timeval tv;
    gettimeofday(&tv, NULL);
    return (uint64_t)tv.tv_sec * 1000 + (uint64_t)tv.tv_usec / 1000;
}

/* cksum: Internet checksum with optional pseudo-header start. */
static uint16_t cksum(const uint8_t *p, size_t n, uint32_t start) {
    uint32_t s = start;
    size_t i;
    for (i = 0; i + 1 < n; i += 2) s += ((uint32_t)p[i] << 8) | p[i + 1];
    if (n & 1) s += (uint32_t)p[n - 1] << 8;
    while (s >> 16) s = (s & 0xffff) + (s >> 16);
    return (uint16_t)~s;
}

/* rd32: big-endian u32 load. */
static uint32_t rd32(const uint8_t *p) {
    return ((uint32_t)p[0] << 24) | ((uint32_t)p[1] << 16) | ((uint32_t)p[2] << 8) | p[3];
}
/* rd16: big-endian u16 load. */
static uint16_t rd16(const uint8_t *p) { return (uint16_t)((p[0] << 8) | p[1]); }
/* wr32: big-endian u32 store. */
static void wr32(uint8_t *p, uint32_t v) {
    p[0] = (uint8_t)(v >> 24); p[1] = (uint8_t)(v >> 16); p[2] = (uint8_t)(v >> 8); p[3] = (uint8_t)v;
}
/* wr16: big-endian u16 store. */
static void wr16(uint8_t *p, uint16_t v) { p[0] = (uint8_t)(v >> 8); p[1] = (uint8_t)v; }

/* ip4str: host-order IPv4 to dotted decimal. */
static void ip4str(char *o, uint32_t host) {
    uint32_t n = htonl(host);
    const uint8_t *p = (const uint8_t *)&n;
    sprintf(o, "%u.%u.%u.%u", p[0], p[1], p[2], p[3]);
}

/* parse_ip: dotted decimal to host-order IPv4. */
static uint32_t parse_ip(const char *s) {
    unsigned a, b, c, d;
    if (sscanf(s, "%u.%u.%u.%u", &a, &b, &c, &d) != 4) return 0;
    return (a << 24) | (b << 16) | (c << 8) | d;
}

/* emit_ip: build an IPv4 header and output via cbs.output. */
static void emit_ip(loft_stack *s, uint32_t src, uint32_t dst, uint8_t proto,
                    const uint8_t *pl, size_t pn) {
    uint8_t pkt[1500];
    size_t tot = 20 + pn;
    uint16_t sum;
    if (tot > sizeof pkt) return;
    memset(pkt, 0, 20);
    pkt[0] = 0x45;
    wr16(pkt + 2, (uint16_t)tot);
    wr16(pkt + 4, ++s->ipid);
    pkt[8] = 64;
    pkt[9] = proto;
    wr32(pkt + 12, src);
    wr32(pkt + 16, dst);
    sum = cksum(pkt, 20, 0);
    wr16(pkt + 10, sum);
    memcpy(pkt + 20, pl, pn);
    if (s->cbs.output) s->cbs.output(s->cbs.ctx, pkt, tot);
}

/* tcp_pseudo: IPv4 pseudo-header sum for TCP checksum. */
static uint32_t tcp_pseudo(uint32_t src, uint32_t dst, uint16_t tcp_len) {
    uint32_t s = 0;
    s += (src >> 16) & 0xffff;
    s += src & 0xffff;
    s += (dst >> 16) & 0xffff;
    s += dst & 0xffff;
    s += 6;
    s += tcp_len;
    return s;
}

/* emit_tcp: send a TCP segment toward the LAN (src/dst swapped). */
static void emit_tcp(loft_stack *s, tcb *t, uint8_t flags, const uint8_t *data, size_t dn) {
    uint8_t th[20 + MSS];
    uint16_t sum;
    size_t ln;
    if (dn > MSS) dn = MSS;
    ln = 20 + dn;
    memset(th, 0, 20);
    wr16(th + 0, t->dport);
    wr16(th + 2, t->sport);
    wr32(th + 4, t->snd_nxt);
    wr32(th + 8, t->rcv_nxt);
    th[12] = 5 << 4;
    th[13] = flags;
    wr16(th + 14, 65535);
    if (dn) memcpy(th + 20, data, dn);
    sum = cksum(th, ln, tcp_pseudo(t->dst, t->src, (uint16_t)ln));
    wr16(th + 16, sum);
    emit_ip(s, t->dst, t->src, 6, th, ln);
}

/* emit_udp: send a UDP datagram toward the LAN. */
static void emit_udp(loft_stack *s, uint32_t src, uint16_t sport, uint32_t dst, uint16_t dport,
                     const uint8_t *data, size_t dn) {
    uint8_t uh[8 + 2048];
    uint16_t sum;
    size_t ln = 8 + dn;
    uint32_t pseudo;
    if (ln > sizeof uh) return;
    wr16(uh + 0, sport);
    wr16(uh + 2, dport);
    wr16(uh + 4, (uint16_t)ln);
    wr16(uh + 6, 0);
    memcpy(uh + 8, data, dn);
    pseudo = 0;
    pseudo += (src >> 16) & 0xffff;
    pseudo += src & 0xffff;
    pseudo += (dst >> 16) & 0xffff;
    pseudo += dst & 0xffff;
    pseudo += 17;
    pseudo += (uint16_t)ln;
    sum = cksum(uh, ln, pseudo);
    if (sum == 0) sum = 0xffff;
    wr16(uh + 6, sum);
    emit_ip(s, src, dst, 17, uh, ln);
}

/* emit_icmp_echo_reply: swap src/dst of an echo request. */
static void emit_icmp_echo_reply(loft_stack *s, const uint8_t *ip, size_t n) {
    uint8_t buf[1500];
    uint16_t sum;
    size_t ihl, ln;
    if (n < 28 || n > sizeof buf) return;
    ihl = (ip[0] & 0xf) * 4;
    ln = n - ihl;
    memcpy(buf, ip + ihl, ln);
    buf[0] = 0; /* echo reply */
    buf[1] = 0;
    wr16(buf + 2, 0);
    sum = cksum(buf, ln, 0);
    wr16(buf + 2, sum);
    emit_ip(s, rd32(ip + 16), rd32(ip + 12), 1, buf, ln);
}

/* find_tcb: match a 4-tuple. */
static tcb *find_tcb(loft_stack *s, uint32_t src, uint16_t sport, uint32_t dst, uint16_t dport) {
    int i;
    for (i = 0; i < MAX_TCB; i++) {
        if (s->t[i].st && s->t[i].src == src && s->t[i].sport == sport &&
            s->t[i].dst == dst && s->t[i].dport == dport)
            return &s->t[i];
    }
    return NULL;
}

/* find_cookie: TCB whose mux cookie is `cookie`. */
static tcb *find_cookie(loft_stack *s, void *cookie) {
    int i;
    if (!cookie) return NULL;
    for (i = 0; i < MAX_TCB; i++) {
        if (s->t[i].st && s->t[i].cookie == cookie) return &s->t[i];
    }
    return NULL;
}

/* alloc_tcb: first free TCB, or NULL if the table is full. */
static tcb *alloc_tcb(loft_stack *s) {
    int i;
    for (i = 0; i < MAX_TCB; i++) {
        if (s->t[i].st == ST_FREE) {
            memset(&s->t[i], 0, sizeof s->t[i]);
            return &s->t[i];
        }
    }
    return NULL;
}

/* tcb_close: optional RST, then tcp_close callback. */
static void tcb_close(loft_stack *s, tcb *t, int rst) {
    void *cookie;
    if (!t->st) return;
    cookie = t->cookie;
    if (rst) emit_tcp(s, t, 0x04, NULL, 0); /* RST */
    t->st = ST_FREE;
    t->cookie = NULL;
    if (cookie && s->cbs.tcp_close) {
        pthread_mutex_unlock(&s->mu);
        s->cbs.tcp_close(s->cbs.ctx, cookie);
        pthread_mutex_lock(&s->mu);
    }
}

/* handle_tcp: SYN opens a mux stream; data goes to tcp_send. */
static void handle_tcp(loft_stack *s, const uint8_t *ip, size_t n) {
    size_t ihl, doff, plen;
    const uint8_t *th, *data;
    uint32_t src, dst, seq, ack;
    uint16_t sport, dport;
    uint8_t flags;
    tcb *t;
    void *cookie;
    char ipstr[32];

    ihl = (ip[0] & 0xf) * 4;
    if (n < ihl + 20) return;
    th = ip + ihl;
    doff = (th[12] >> 4) * 4;
    if (doff < 20 || ihl + doff > n) return;
    src = rd32(ip + 12);
    dst = rd32(ip + 16);
    sport = rd16(th + 0);
    dport = rd16(th + 2);
    seq = rd32(th + 4);
    ack = rd32(th + 8);
    flags = th[13];
    data = th + doff;
    plen = n - ihl - doff;

    t = find_tcb(s, src, sport, dst, dport);

    if (flags & 0x04) { /* RST */
        if (t) tcb_close(s, t, 0);
        return;
    }

    if (!t) {
        if (!(flags & 0x02)) return; /* not SYN */
        t = alloc_tcb(s);
        if (!t) return;
        t->src = src; t->dst = dst; t->sport = sport; t->dport = dport;
        t->irs = seq;
        t->rcv_nxt = seq + 1;
        t->iss = (uint32_t)now_ms() ^ (src << 8) ^ sport;
        t->snd_nxt = t->iss;
        t->snd_una = t->iss;
        t->st = ST_SYN;
        t->last_ms = now_ms();
        emit_tcp(s, t, 0x12, NULL, 0); /* SYN-ACK */
        t->snd_nxt = t->iss + 1;
        ip4str(ipstr, dst);
        pthread_mutex_unlock(&s->mu);
        cookie = s->cbs.tcp_open ? s->cbs.tcp_open(s->cbs.ctx, ipstr, dport) : NULL;
        pthread_mutex_lock(&s->mu);
        if (t->st == ST_FREE) {
            if (cookie && s->cbs.tcp_close) {
                pthread_mutex_unlock(&s->mu);
                s->cbs.tcp_close(s->cbs.ctx, cookie);
                pthread_mutex_lock(&s->mu);
            }
            return;
        }
        t->cookie = cookie;
        if (!cookie) {
            tcb_close(s, t, 1);
            return;
        }
        if (s->cbs.tcp_ready) {
            pthread_mutex_unlock(&s->mu);
            s->cbs.tcp_ready(s->cbs.ctx, cookie);
            pthread_mutex_lock(&s->mu);
        }
        return;
    }

    t->last_ms = now_ms();

    if (flags & 0x10) { /* ACK */
        if ((int32_t)(ack - t->snd_una) > 0 && (int32_t)(ack - t->snd_nxt) <= 0) {
            uint32_t adv = ack - t->snd_una;
            if (t->st == ST_SYN && adv >= 1) {
                t->st = ST_EST;
                t->snd_una = ack;
                if (t->unack_n > 0) {
                    /* SYN consumed */
                }
            } else {
                if (adv > (uint32_t)t->unack_n) adv = (uint32_t)t->unack_n;
                if (adv) {
                    memmove(t->unack, t->unack + adv, t->unack_n - (int)adv);
                    t->unack_n -= (int)adv;
                    t->snd_una = ack;
                    t->retries = 0;
                }
            }
        }
    }

    if (t->st == ST_SYN && (flags & 0x10)) t->st = ST_EST;

    if (plen && seq == t->rcv_nxt) {
        t->rcv_nxt += (uint32_t)plen;
        emit_tcp(s, t, 0x10, NULL, 0);
        if (t->cookie && s->cbs.tcp_send) {
            void *c = t->cookie;
            pthread_mutex_unlock(&s->mu);
            s->cbs.tcp_send(s->cbs.ctx, c, data, plen);
            pthread_mutex_lock(&s->mu);
        }
    } else if (plen) {
        emit_tcp(s, t, 0x10, NULL, 0); /* ACK current */
    }

    if (flags & 0x01) { /* FIN */
        if (seq == t->rcv_nxt || seq + plen == t->rcv_nxt) {
            t->rcv_nxt = seq + plen + 1;
            emit_tcp(s, t, 0x11, NULL, 0); /* FIN+ACK */
            t->snd_nxt++;
            t->st = ST_LAST;
            tcb_close(s, t, 0);
        }
    }
}

/* handle_udp: remember the 5-tuple and forward payload via udp_send. */
static void handle_udp(loft_stack *s, const uint8_t *ip, size_t n) {
    size_t ihl;
    const uint8_t *uh;
    uint32_t src, dst;
    uint16_t sport, dport, ulen;
    int i, slot = -1;
    char ipstr[32];
    ihl = (ip[0] & 0xf) * 4;
    if (n < ihl + 8) return;
    uh = ip + ihl;
    src = rd32(ip + 12);
    dst = rd32(ip + 16);
    sport = rd16(uh + 0);
    dport = rd16(uh + 2);
    ulen = rd16(uh + 4);
    if (ulen < 8 || ihl + ulen > n) ulen = (uint16_t)(n - ihl);
    for (i = 0; i < MAX_UDP; i++) {
        if (s->u[i].used && s->u[i].src == src && s->u[i].sport == sport &&
            s->u[i].dst == dst && s->u[i].dport == dport) {
            slot = i; break;
        }
        if (slot < 0 && !s->u[i].used) slot = i;
    }
    if (slot < 0) slot = 0;
    s->u[slot].used = 1;
    s->u[slot].src = src; s->u[slot].dst = dst;
    s->u[slot].sport = sport; s->u[slot].dport = dport;
    s->u[slot].last_ms = now_ms();
    ip4str(ipstr, dst);
    if (s->cbs.udp_send) {
        pthread_mutex_unlock(&s->mu);
        s->cbs.udp_send(s->cbs.ctx, ipstr, dport, uh + 8, ulen - 8);
        pthread_mutex_lock(&s->mu);
    }
}

/* loft_stack_new: allocate a stack for overlay `our_ip_host` (host order). */
loft_stack *loft_stack_new(uint32_t our_ip_host, uint16_t mtu, loft_stack_cbs cbs) {
    loft_stack *s = (loft_stack *)calloc(1, sizeof *s);
    if (!s) return NULL;
    s->our = our_ip_host;
    s->mtu = mtu ? mtu : 1280;
    s->cbs = cbs;
    pthread_mutex_init(&s->mu, NULL);
    return s;
}

/* loft_stack_free: RST every TCB and free. */
void loft_stack_free(loft_stack *s) {
    int i;
    if (!s) return;
    pthread_mutex_lock(&s->mu);
    for (i = 0; i < MAX_TCB; i++) {
        if (s->t[i].st) tcb_close(s, &s->t[i], 1);
    }
    pthread_mutex_unlock(&s->mu);
    pthread_mutex_destroy(&s->mu);
    free(s);
}

/* loft_stack_input: IPv4 packet from utun (TCP/UDP/ICMP echo). */
void loft_stack_input(loft_stack *s, const uint8_t *pkt, size_t n) {
    uint8_t proto;
    uint16_t frag;
    if (!s || n < 20) return;
    if ((pkt[0] >> 4) != 4) return;
    frag = rd16(pkt + 6);
    if (frag & 0x3fff) return; /* fragments */
    proto = pkt[9];
    pthread_mutex_lock(&s->mu);
    if (proto == 6) handle_tcp(s, pkt, n);
    else if (proto == 17) handle_udp(s, pkt, n);
    else if (proto == 1 && n >= 28 && pkt[20] == 8 && rd32(pkt + 16) == s->our)
        emit_icmp_echo_reply(s, pkt, n);
    pthread_mutex_unlock(&s->mu);
}

/* loft_stack_tcp_rx: mux bytes toward the LAN TCP peer (PSH ACK). */
void loft_stack_tcp_rx(loft_stack *s, void *cookie, const uint8_t *p, size_t n) {
    tcb *t;
    if (!s || !cookie || !p) return;
    pthread_mutex_lock(&s->mu);
    t = find_cookie(s, cookie);
    if (!t || t->st == ST_FREE) { pthread_mutex_unlock(&s->mu); return; }
    while (n) {
        size_t take = n > MSS ? MSS : n;
        if (t->unack_n + (int)take > UNACK) break;
        memcpy(t->unack + t->unack_n, p, take);
        t->unack_n += (int)take;
        emit_tcp(s, t, 0x18, p, take); /* PSH ACK */
        t->snd_nxt += (uint32_t)take;
        p += take; n -= take;
    }
    pthread_mutex_unlock(&s->mu);
}

/* loft_stack_tcp_eof: mux closed; send FIN ACK. */
void loft_stack_tcp_eof(loft_stack *s, void *cookie) {
    tcb *t;
    if (!s || !cookie) return;
    pthread_mutex_lock(&s->mu);
    t = find_cookie(s, cookie);
    if (t && t->st == ST_EST) {
        emit_tcp(s, t, 0x11, NULL, 0); /* FIN ACK */
        t->snd_nxt++;
        t->st = ST_FINW;
    }
    pthread_mutex_unlock(&s->mu);
}

/* loft_stack_udp_rx: mux datagram toward the matching NAT slot. */
void loft_stack_udp_rx(loft_stack *s, const char *remote_ip, uint16_t remote_port,
                       const uint8_t *p, size_t n) {
    uint32_t dip;
    int i, best = -1;
    uint64_t newest = 0;
    if (!s || !remote_ip) return;
    dip = parse_ip(remote_ip);
    pthread_mutex_lock(&s->mu);
    for (i = 0; i < MAX_UDP; i++) {
        if (s->u[i].used && s->u[i].dst == dip && s->u[i].dport == remote_port) {
            if (s->u[i].last_ms >= newest) { newest = s->u[i].last_ms; best = i; }
        }
    }
    if (best >= 0) {
        emit_udp(s, s->u[best].dst, s->u[best].dport, s->u[best].src, s->u[best].sport, p, n);
    }
    pthread_mutex_unlock(&s->mu);
}

/* loft_stack_tick: retransmit unacked TCP; expire idle UDP. */
void loft_stack_tick(loft_stack *s) {
    int i;
    uint64_t t;
    if (!s) return;
    t = now_ms();
    pthread_mutex_lock(&s->mu);
    for (i = 0; i < MAX_TCB; i++) {
        tcb *c = &s->t[i];
        if (!c->st) continue;
        if (t - c->last_ms > 120000) { tcb_close(s, c, 1); continue; }
        if (c->unack_n > 0 && t - c->last_ms > 250) {
            emit_tcp(s, c, 0x18, c->unack, (size_t)(c->unack_n > MSS ? MSS : c->unack_n));
            c->retries++;
            if (c->retries > 12) tcb_close(s, c, 1);
        }
    }
    for (i = 0; i < MAX_UDP; i++) {
        if (s->u[i].used && t - s->u[i].last_ms > 60000) s->u[i].used = 0;
    }
    pthread_mutex_unlock(&s->mu);
}
