# Code scanning 例外

[English](codeql.md) · [Русский](codeql_ru.md) · **中文**

GitHub CodeQL 在 `main` 上跑默认 Go 套件。一部分命中是 loft 按规范实现的外部协议，或信任路径不是 TLS CA。这些位置保留。每个 sink 上一行有 `codeql[<规则>]` 注释，GitHub 上的告警按同一理由关闭。

没有协议或信任理由不要加抑制。不要在 `.github/codeql/codeql-config.yml` 里关掉整条规则。覆盖网代码里同一规则的新命中保持打开。

## SSTP MS-CHAPv2（MD4、DES）

`internal/sstp/mschapv2.go`。Windows SSTP 用 MS-CHAPv2 认证（[RFC 2759](https://www.rfc-editor.org/rfc/rfc2759)）。NT 哈希是密码 UTF-16LE 的 MD4。挑战响应是 DES。换哈希或算法就连不上 Windows。loft 覆盖网数据在 Status 之后是 ChaCha20-Poly1305 或 AES-256-GCM。

## SSTP CHAP-MD5

`internal/sstp/ppp.go`。PPP CHAP（[RFC 1994](https://www.rfc-editor.org/rfc/rfc1994)）是 MD5(id || 密码 || challenge)。同样是 Windows/SSTP，不是覆盖网认证。

## SSTP 最低 TLS 1.0

`internal/sstp/client.go`。部分 Windows SSTP 只能完成 TLS 1.0/1.1。仅该拨号 `MinVersion` 为 TLS 1.0。覆盖网前端是 TLS 1.2 及以上。

## Trojan 密码哈希（SHA-224）

`internal/trojan/proto.go`。Trojan 线上标识是 `hex(SHA224(password))`。这是 Trojan 协议，不是 loft 成员的 KDF。

## 拉取 roster 跳过 CA

`internal/transport/roster_fetch.go`。roster JSON 由控制器密钥签名。拉取 URL 的 TLS 证书不是信任根。这里 skip-verify 不削弱覆盖网认证（`lt_` / `pbk`）。

## mask：按 origin 形状的 TLS，跳过 CA

`internal/transport/mask.go`。客户端从伪装 origin 抄 TLS 记录长度，好让填充跟着 origin。信任是 `lt_` / `pbk`，不是该 origin 的公开 CA。

## loft-probe skip-verify

`clients/go/cmd/loft-probe/probes.go`。探测的是目的地能否完成 TLS 握手。这是可达性，不是信任决定。HTTPS 探测同样认 `-insecure`。

## loft-probe 不打印 `authKey`

CodeQL 把 `internal/xtls/reality.go` 里的 `authKey`（REALITY AEAD 的 HKDF 会话键）污点传到 `printResults`。loft-probe 不读该字段。`Detail` 是探测的 RTT、TLS 或 HTTP 状态。
