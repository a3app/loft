// ChaCha20-Poly1305 seal/open via the C AEAD.

import Foundation
import Security

/// ChaCha20-Poly1305 seal/open via the C AEAD.
enum LoftCrypto {
    static let nonceSize = 12

    /// `n` cryptographically random bytes from SecRandom.
    static func randomBytes(_ n: Int) -> Data {
        var d = Data(count: n)
        _ = d.withUnsafeMutableBytes { SecRandomCopyBytes(kSecRandomDefault, n, $0.baseAddress!) }
        return d
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
