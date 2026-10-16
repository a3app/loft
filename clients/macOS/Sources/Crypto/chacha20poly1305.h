/* ChaCha20-Poly1305 AEAD (RFC 8439). Bridged into Swift as loft_aead_encrypt/decrypt. */

#pragma once
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* RFC 8439 AEAD. out must hold pt_len + 16. Returns 0 on success. */
int loft_aead_encrypt(const uint8_t key[32], const uint8_t nonce[12],
                      const uint8_t *aad, size_t aad_len,
                      const uint8_t *pt, size_t pt_len,
                      uint8_t *out, size_t *out_len);

/* ct_len includes the 16-byte tag. out must hold ct_len - 16. */
int loft_aead_decrypt(const uint8_t key[32], const uint8_t nonce[12],
                      const uint8_t *aad, size_t aad_len,
                      const uint8_t *ct, size_t ct_len,
                      uint8_t *out, size_t *out_len);

#ifdef __cplusplus
}
#endif
