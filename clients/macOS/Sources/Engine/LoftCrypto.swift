// Token auth, HKDF key derive, and ChaCha20-Poly1305 seal/open via the C AEAD.

import Foundation
import Security
import CommonCrypto

/// Token auth, HKDF key derive, and ChaCha20-Poly1305 seal/open via the C AEAD.
enum LoftCrypto {
    static let nonceSize = 12
    static let keySize = 32
    static let authSize = 16
    static let proofLen = 1 + 16 + authSize
    static let tagAuth: UInt8 = 0x02

    /// `n` cryptographically random bytes from SecRandom.
    static func randomBytes(_ n: Int) -> Data {
        var d = Data(count: n)
        _ = d.withUnsafeMutableBytes { SecRandomCopyBytes(kSecRandomDefault, n, $0.baseAddress!) }
        return d
    }

    /// HMAC-SHA256 of `data` under `key`.
    static func hmacSHA256(_ key: Data, _ data: Data) -> Data {
        var out = Data(count: Int(CC_SHA256_DIGEST_LENGTH))
        out.withUnsafeMutableBytes { o in
            key.withUnsafeBytes { k in
                data.withUnsafeBytes { d in
                    CCHmac(CCHmacAlgorithm(kCCHmacAlgSHA256), k.baseAddress, key.count, d.baseAddress, data.count, o.baseAddress)
                }
            }
        }
        return out
    }

    /// First 16 bytes of HMAC-SHA256, used as the auth proof MAC.
    static func mac16(_ key: Data, _ data: Data) -> Data {
        return hmacSHA256(key, data).prefix(authSize)
    }

    /// HKDF-Extract(token, salt=nonce) then Expand to a 32-byte session key.
    static func deriveKey(token: Data, nonce: Data) -> Data {
        let prk = hmacSHA256(nonce, token) // HKDF-Extract(salt=nonce, ikm=token)
        return hkdfExpand(prk: prk, info: Data("loft-v1".utf8), length: keySize)
    }

    /// RFC 5869 HKDF-Expand of `prk` to `length` bytes.
    static func hkdfExpand(prk: Data, info: Data, length: Int) -> Data {
        var out = Data()
        var prev = Data()
        var block: UInt8 = 1
        while out.count < length {
            var input = prev
            input.append(info)
            input.append(block)
            prev = hmacSHA256(prk, input)
            out.append(prev)
            block &+= 1
        }
        return out.prefix(length)
    }

    /// Auth proof: tag 0x02, 16-byte nonce, MAC16(token, nonce).
    static func makeAuth(token: Data) -> (proof: Data, nonce: Data) {
        let nonce = randomBytes(16)
        var proof = Data([tagAuth])
        proof.append(nonce)
        proof.append(mac16(token, nonce))
        return (proof, nonce)
    }

    /// AEAD encrypt: 12-byte nonce || ciphertext || 16-byte tag.
    static func seal(key: Data, plaintext: Data, aad: Data) -> Data? {
        let nonce = randomBytes(nonceSize)
        var ct = Data(count: plaintext.count + 16)
        var outLen: Int = 0
        let rc = withPtr(key) { k in
            withPtr(nonce) { n in
                withPtr(aad) { a in
                    withPtr(plaintext) { p in
                        ct.withUnsafeMutableBytes { o -> Int32 in
                            loft_aead_encrypt(k, n, a, aad.count, p, plaintext.count,
                                              o.bindMemory(to: UInt8.self).baseAddress, &outLen)
                        }
                    }
                }
            }
        }
        if rc != 0 { return nil }
        var blob = nonce
        blob.append(ct.prefix(outLen))
        return blob
    }

    /// AEAD decrypt of nonce||ct||tag. Nil on short blob or tag mismatch.
    static func open(key: Data, blob: Data, aad: Data) -> Data? {
        if blob.count < nonceSize + 16 { return nil }
        let nonce = blob.prefix(nonceSize)
        let ct = Data(blob.dropFirst(nonceSize))
        var pt = Data(count: max(0, ct.count - 16))
        var outLen: Int = 0
        let rc = withPtr(key) { k in
            withPtr(nonce) { n in
                withPtr(aad) { a in
                    withPtr(ct) { c in
                        pt.withUnsafeMutableBytes { o -> Int32 in
                            loft_aead_decrypt(k, n, a, aad.count, c, ct.count,
                                              o.bindMemory(to: UInt8.self).baseAddress, &outLen)
                        }
                    }
                }
            }
        }
        if rc != 0 { return nil }
        return pt.prefix(outLen)
    }

    /// Calls `body` with a pointer to `d`, or nil if empty.
    private static func withPtr(_ d: Data, _ body: (UnsafePointer<UInt8>?) -> Int32) -> Int32 {
        if d.isEmpty { return body(nil) }
        return d.withUnsafeBytes { body($0.bindMemory(to: UInt8.self).baseAddress) }
    }
}
