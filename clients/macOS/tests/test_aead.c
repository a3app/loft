/* RFC 8439 §2.8.2 AEAD vector for loft_aead_encrypt/decrypt.
 * Built by `make test-crypto`; binary lands in dist/clients/macOS/obj/. */
#include "chacha20poly1305.h"
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

/* unhex: parse hex, skipping whitespace. Returns byte count or -1. */
static int unhex(const char *s, uint8_t *out, size_t cap) {
    size_t n = 0;
    while (*s) {
        unsigned v;
        if (*s == ' ' || *s == '\n' || *s == '\t') { s++; continue; }
        if (sscanf(s, "%2x", &v) != 1) break;
        if (n >= cap) return -1;
        out[n++] = (uint8_t)v;
        s += 2;
    }
    return (int)n;
}

/* main: RFC vector encrypt/decrypt plus empty AAD+PT round-trip. */
int main(void) {
    uint8_t key[32], nonce[12], aad[12], pt[114], expect[130], out[160], back[160];
    size_t out_len = 0, back_len = 0;
    int nk, nn, na, np, ne;

    nk = unhex("808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f", key, 32);
    nn = unhex("070000004041424344454647", nonce, 12);
    na = unhex("50515253c0c1c2c3c4c5c6c7", aad, 12);
    np = unhex(
        "4c616469657320616e642047656e746c656d656e206f662074686520636c617373"
        "206f66202739393a204966204920636f756c64206f6666657220796f75206f6e6c"
        "79206f6e652074697020666f7220746865206675747572652c2073756e73637265"
        "656e20776f756c642062652069742e", pt, 114);
    ne = unhex(
        "d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63d"
        "bea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b3692dd"
        "bd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc3ff4de"
        "f08e4b7a9de576d26586cec64b61161ae10b594f09e26a7e902ecbd0600691", expect, 130);
    if (nk != 32 || nn != 12 || na != 12 || np != 114 || ne != 130) {
        fprintf(stderr, "hex parse %d %d %d %d %d\n", nk, nn, na, np, ne);
        return 1;
    }
    if (loft_aead_encrypt(key, nonce, aad, 12, pt, 114, out, &out_len) != 0) {
        fprintf(stderr, "encrypt failed\n");
        return 1;
    }
    if (out_len != 130 || memcmp(out, expect, 130) != 0) {
        fprintf(stderr, "encrypt mismatch (len %zu)\n", out_len);
        return 1;
    }
    if (loft_aead_decrypt(key, nonce, aad, 12, expect, 130, back, &back_len) != 0) {
        fprintf(stderr, "decrypt failed\n");
        return 1;
    }
    if (back_len != 114 || memcmp(back, pt, 114) != 0) {
        fprintf(stderr, "decrypt mismatch\n");
        return 1;
    }
    /* empty AAD + empty PT round-trip */
    if (loft_aead_encrypt(key, nonce, NULL, 0, NULL, 0, out, &out_len) != 0 || out_len != 16) {
        fprintf(stderr, "empty encrypt failed\n");
        return 1;
    }
    if (loft_aead_decrypt(key, nonce, NULL, 0, out, out_len, back, &back_len) != 0 || back_len != 0) {
        fprintf(stderr, "empty decrypt failed\n");
        return 1;
    }
    printf("aead rfc8439 ok\n");
    return 0;
}
