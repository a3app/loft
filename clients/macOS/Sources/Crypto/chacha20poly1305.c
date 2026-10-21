/* RFC 8439 ChaCha20-Poly1305 AEAD used by LoftCrypto.seal/open. */
#include "chacha20poly1305.h"
#include <stdlib.h>
#include <string.h>

/* ld32: little-endian u32 load. */
static uint32_t ld32(const uint8_t *p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}
/* st32: little-endian u32 store. */
static void st32(uint8_t *p, uint32_t v) {
    p[0] = (uint8_t)v; p[1] = (uint8_t)(v >> 8); p[2] = (uint8_t)(v >> 16); p[3] = (uint8_t)(v >> 24);
}
/* rotl: 32-bit rotate left. */
static uint32_t rotl(uint32_t x, int n) { return (x << n) | (x >> (32 - n)); }
/* qr: ChaCha quarter round. */
static void qr(uint32_t *a, uint32_t *b, uint32_t *c, uint32_t *d) {
    *a += *b; *d ^= *a; *d = rotl(*d, 16);
    *c += *d; *b ^= *c; *b = rotl(*b, 12);
    *a += *b; *d ^= *a; *d = rotl(*d, 8);
    *c += *d; *b ^= *c; *b = rotl(*b, 7);
}

/* chacha_block: 20-round ChaCha20 block into `out`. */
static void chacha_block(uint8_t out[64], const uint32_t s[16]) {
    uint32_t x[16];
    int i;
    memcpy(x, s, sizeof x);
    for (i = 0; i < 10; i++) {
        qr(x+0, x+4, x+8,  x+12); qr(x+1, x+5, x+9,  x+13);
        qr(x+2, x+6, x+10, x+14); qr(x+3, x+7, x+11, x+15);
        qr(x+0, x+5, x+10, x+15); qr(x+1, x+6, x+11, x+12);
        qr(x+2, x+7, x+8,  x+13); qr(x+3, x+4, x+9,  x+14);
    }
    for (i = 0; i < 16; i++) st32(out + 4 * i, x[i] + s[i]);
}

/* chacha_init: IETF ChaCha20 state (constants, key, counter, 96-bit nonce). */
static void chacha_init(uint32_t s[16], const uint8_t key[32], const uint8_t nonce[12], uint32_t counter) {
    s[0] = 0x61707865; s[1] = 0x3320646e; s[2] = 0x79622d32; s[3] = 0x6b206574;
    s[4] = ld32(key);      s[5] = ld32(key + 4);  s[6] = ld32(key + 8);   s[7] = ld32(key + 12);
    s[8] = ld32(key + 16);  s[9] = ld32(key + 20); s[10] = ld32(key + 24); s[11] = ld32(key + 28);
    s[12] = counter;
    s[13] = ld32(nonce); s[14] = ld32(nonce + 4); s[15] = ld32(nonce + 8);
}

/* chacha_xor: keystream XOR of `n` bytes starting at `counter`. */
static void chacha_xor(uint8_t *out, const uint8_t *in, size_t n,
                       const uint8_t key[32], const uint8_t nonce[12], uint32_t counter) {
    uint32_t s[16];
    uint8_t block[64];
    size_t off = 0, i, take;
    chacha_init(s, key, nonce, counter);
    while (off < n) {
        take = n - off;
        if (take > 64) take = 64;
        chacha_block(block, s);
        s[12]++;
        for (i = 0; i < take; i++) out[off + i] = in[off + i] ^ block[i];
        off += take;
    }
}

/* poly1305: one-time MAC (poly1305-donna 32-bit limbs, public domain, Andrew Moon). */
static void poly1305(uint8_t mac[16], const uint8_t *m, size_t bytes, const uint8_t key[32]) {
    uint32_t r0, r1, r2, r3, r4, s1, s2, s3, s4;
    uint32_t h0 = 0, h1 = 0, h2 = 0, h3 = 0, h4 = 0;
    uint32_t t0, t1, t2, t3;
    uint64_t d0, d1, d2, d3, d4;
    uint32_t c;

    t0 = ld32(key + 0); t1 = ld32(key + 4); t2 = ld32(key + 8); t3 = ld32(key + 12);
    r0 = t0 & 0x3ffffff; t0 >>= 26; t0 |= t1 << 6;
    r1 = t0 & 0x3ffff03; t1 >>= 20; t1 |= t2 << 12;
    r2 = t1 & 0x3ffc0ff; t2 >>= 14; t2 |= t3 << 18;
    r3 = t2 & 0x3f03fff; t3 >>= 8;
    r4 = t3 & 0x00fffff;
    s1 = r1 * 5; s2 = r2 * 5; s3 = r3 * 5; s4 = r4 * 5;

    while (bytes > 0) {
        uint8_t block[17];
        size_t take = bytes < 16 ? bytes : 16;
        memset(block, 0, 17);
        memcpy(block, m, take);
        block[take] = 1;
        t0 = ld32(block + 0); t1 = ld32(block + 3); t2 = ld32(block + 6); t3 = ld32(block + 9);
        h0 += t0 & 0x3ffffff;
        h1 += (ld32(block + 3) >> 2) & 0x3ffffff;
        h2 += (ld32(block + 6) >> 4) & 0x3ffffff;
        h3 += (ld32(block + 9) >> 6) & 0x3ffffff;
        h4 += (ld32(block + 12) >> 8) | ((uint32_t)block[16] << 24);

        d0 = (uint64_t)h0*r0 + (uint64_t)h1*s4 + (uint64_t)h2*s3 + (uint64_t)h3*s2 + (uint64_t)h4*s1;
        d1 = (uint64_t)h0*r1 + (uint64_t)h1*r0 + (uint64_t)h2*s4 + (uint64_t)h3*s3 + (uint64_t)h4*s2;
        d2 = (uint64_t)h0*r2 + (uint64_t)h1*r1 + (uint64_t)h2*r0 + (uint64_t)h3*s4 + (uint64_t)h4*s3;
        d3 = (uint64_t)h0*r3 + (uint64_t)h1*r2 + (uint64_t)h2*r1 + (uint64_t)h3*r0 + (uint64_t)h4*s4;
        d4 = (uint64_t)h0*r4 + (uint64_t)h1*r3 + (uint64_t)h2*r2 + (uint64_t)h3*r1 + (uint64_t)h4*r0;

        c = (uint32_t)(d0 >> 26); h0 = (uint32_t)d0 & 0x3ffffff; d1 += c;
        c = (uint32_t)(d1 >> 26); h1 = (uint32_t)d1 & 0x3ffffff; d2 += c;
        c = (uint32_t)(d2 >> 26); h2 = (uint32_t)d2 & 0x3ffffff; d3 += c;
        c = (uint32_t)(d3 >> 26); h3 = (uint32_t)d3 & 0x3ffffff; d4 += c;
        c = (uint32_t)(d4 >> 26); h4 = (uint32_t)d4 & 0x3ffffff;
        h0 += c * 5; c = h0 >> 26; h0 &= 0x3ffffff; h1 += c;

        m += take;
        bytes -= take;
    }

    c = h1 >> 26; h1 &= 0x3ffffff; h2 += c;
    c = h2 >> 26; h2 &= 0x3ffffff; h3 += c;
    c = h3 >> 26; h3 &= 0x3ffffff; h4 += c;
    c = h4 >> 26; h4 &= 0x3ffffff; h0 += c * 5;
    c = h0 >> 26; h0 &= 0x3ffffff; h1 += c;

    {
        uint32_t g0, g1, g2, g3, g4, mask;
        g0 = h0 + 5; c = g0 >> 26; g0 &= 0x3ffffff;
        g1 = h1 + c; c = g1 >> 26; g1 &= 0x3ffffff;
        g2 = h2 + c; c = g2 >> 26; g2 &= 0x3ffffff;
        g3 = h3 + c; c = g3 >> 26; g3 &= 0x3ffffff;
        g4 = h4 + c - (1u << 26);
        mask = (g4 >> 31) - 1;
        g0 &= mask; g1 &= mask; g2 &= mask; g3 &= mask; g4 &= mask;
        mask = ~mask;
        h0 = (h0 & mask) | g0;
        h1 = (h1 & mask) | g1;
        h2 = (h2 & mask) | g2;
        h3 = (h3 & mask) | g3;
        h4 = (h4 & mask) | g4;
    }

    {
        uint64_t f0, f1, pad0, pad1;
        pad0 = (uint64_t)ld32(key + 16) | ((uint64_t)ld32(key + 20) << 32);
        pad1 = (uint64_t)ld32(key + 24) | ((uint64_t)ld32(key + 28) << 32);
        f0 = ((uint64_t)h0) | ((uint64_t)h1 << 26) | ((uint64_t)h2 << 52);
        f1 = ((uint64_t)h2 >> 12) | ((uint64_t)h3 << 14) | ((uint64_t)h4 << 40);
        f0 += pad0;
        f1 += pad1 + (f0 < pad0);
        st32(mac + 0, (uint32_t)f0);
        st32(mac + 4, (uint32_t)(f0 >> 32));
        st32(mac + 8, (uint32_t)f1);
        st32(mac + 12, (uint32_t)(f1 >> 32));
    }
}

/* poly_aead: Poly1305 over AAD || pad || CT || pad || lenAAD || lenCT. */
static void poly_aead(uint8_t mac[16], const uint8_t key[32],
                      const uint8_t *aad, size_t aad_len,
                      const uint8_t *ct, size_t ct_len) {
    size_t aad_pad = (16 - (aad_len % 16)) % 16;
    size_t ct_pad = (16 - (ct_len % 16)) % 16;
    size_t total = aad_len + aad_pad + ct_len + ct_pad + 16;
    uint8_t *buf = (uint8_t *)calloc(1, total ? total : 1);
    size_t off = 0;
    if (aad_len && aad) memcpy(buf + off, aad, aad_len);
    off += aad_len + aad_pad;
    if (ct_len && ct) memcpy(buf + off, ct, ct_len);
    off += ct_len + ct_pad;
    {
        uint64_t al = (uint64_t)aad_len, cl = (uint64_t)ct_len;
        int i;
        for (i = 0; i < 8; i++) buf[off + i] = (uint8_t)(al >> (8 * i));
        for (i = 0; i < 8; i++) buf[off + 8 + i] = (uint8_t)(cl >> (8 * i));
    }
    poly1305(mac, buf, total, key);
    free(buf);
}

/* loft_aead_encrypt: counter 0 is the Poly1305 key; payload XOR starts at 1. */
int loft_aead_encrypt(const uint8_t key[32], const uint8_t nonce[12],
                      const uint8_t *aad, size_t aad_len,
                      const uint8_t *pt, size_t pt_len,
                      uint8_t *out, size_t *out_len) {
    uint8_t otk[64], polykey[32], tag[16];
    uint32_t s[16];
    chacha_init(s, key, nonce, 0);
    chacha_block(otk, s);
    memcpy(polykey, otk, 32);
    chacha_xor(out, pt, pt_len, key, nonce, 1);
    poly_aead(tag, polykey, aad, aad_len, out, pt_len);
    memcpy(out + pt_len, tag, 16);
    *out_len = pt_len + 16;
    return 0;
}

/* loft_aead_decrypt: constant-time tag compare, then XOR. -1 on mismatch. */
int loft_aead_decrypt(const uint8_t key[32], const uint8_t nonce[12],
                      const uint8_t *aad, size_t aad_len,
                      const uint8_t *ct, size_t ct_len,
                      uint8_t *out, size_t *out_len) {
    uint8_t otk[64], polykey[32], expect[16];
    uint32_t s[16];
    size_t pt_len;
    int i, diff = 0;
    if (ct_len < 16) return -1;
    pt_len = ct_len - 16;
    chacha_init(s, key, nonce, 0);
    chacha_block(otk, s);
    memcpy(polykey, otk, 32);
    poly_aead(expect, polykey, aad, aad_len, ct, pt_len);
    for (i = 0; i < 16; i++) diff |= expect[i] ^ ct[pt_len + i];
    if (diff) return -1;
    chacha_xor(out, ct, pt_len, key, nonce, 1);
    *out_len = pt_len;
    return 0;
}
