# Code scanning exceptions

**English** · [Русский](codeql_ru.md) · [中文](codeql_zh.md)

GitHub CodeQL runs the default Go suite on `main`. Some hits are protocols loft speaks as specified, or a trust path that is not the TLS CA. Those sites stay. Each has a `codeql[<rule>]` comment on the line above the sink, and the GitHub alert is dismissed with the same reason.

Do not add a suppression without a protocol or trust reason. Do not turn a query off in `.github/codeql/codeql-config.yml`. A new hit of the same rule in overlay code stays open.

## SSTP MS-CHAPv2 (MD4, DES)

`internal/sstp/mschapv2.go`. Windows SSTP authenticates with MS-CHAPv2 ([RFC 2759](https://www.rfc-editor.org/rfc/rfc2759)). The NT hash is MD4 of the UTF-16LE password. The challenge response is DES. A different hash or cipher would not talk to Windows. loft overlay data is ChaCha20-Poly1305 or AES-256-GCM after Status.

## SSTP CHAP-MD5

`internal/sstp/ppp.go`. PPP CHAP ([RFC 1994](https://www.rfc-editor.org/rfc/rfc1994)) is MD5(id || password || challenge). Same Windows/SSTP path, not overlay auth.

## SSTP TLS 1.0 minimum

`internal/sstp/client.go`. Some Windows SSTP endpoints only finish TLS 1.0 or 1.1. `MinVersion` is TLS 1.0 on that dial only. Overlay fronts are TLS 1.2 or newer.

## Trojan password hash (SHA-224)

`internal/trojan/proto.go`. The Trojan wire identifier is `hex(SHA224(password))`. That is the Trojan protocol, not a KDF for loft members.

## Roster GET skips the CA

`internal/transport/roster_fetch.go`. The roster JSON is signed with the controller key. The TLS certificate of the fetch URL is not what we trust. Skip-verify here does not weaken overlay auth (`lt_` / `pbk`).

## Dest-shaped TLS (mask) skips the CA

`internal/transport/mask.go`. The client copies TLS record sizes from a decoy origin so padding can follow that origin. Trust is `lt_` / `pbk`, not that origin’s public CA.

## loft-probe skip-verify

`clients/go/cmd/loft-probe/probes.go`. A probe asks whether a destination completes a TLS handshake. That is reachability, not a trust decision. HTTPS probes honor `-insecure` the same way.

## loft-probe does not log `authKey`

CodeQL taints `authKey` in `internal/xtls/reality.go` (HKDF session key for REALITY AEAD) into `printResults`. loft-probe never reads that field. `Detail` is RTT, TLS, or HTTP status of the probe.
