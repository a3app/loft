# Исключения Code scanning

[English](codeql.md) · **Русский** · [中文](codeql_zh.md)

GitHub CodeQL гоняет стандартный набор Go на `main`. Часть срабатываний — чужие протоколы, которые loft говорит как написано в RFC, или доверие не через CA сертификата. Эти места остаются. Над каждым — комментарий `codeql[<правило>]`, алерт на GitHub закрыт с той же формулировкой.

Подавление без причины протокола или доверия не ставить. Правило в `.github/codeql/codeql-config.yml` не выключать. Новый удар того же правила в коде оверлея остаётся открытым.

## SSTP MS-CHAPv2 (MD4, DES)

`internal/sstp/mschapv2.go`. Windows SSTP логинится через MS-CHAPv2 ([RFC 2759](https://www.rfc-editor.org/rfc/rfc2759)). NT-хеш — MD4 от пароля в UTF-16LE. Ответ на challenge — DES. Другой хеш или шифр с Windows не сойдётся. Данные оверлея loft — ChaCha20-Poly1305 или AES-256-GCM после Status.

## SSTP CHAP-MD5

`internal/sstp/ppp.go`. PPP CHAP ([RFC 1994](https://www.rfc-editor.org/rfc/rfc1994)) — MD5(id || пароль || challenge). Тот же путь Windows/SSTP, не аутентификация оверлея.

## SSTP, минимум TLS 1.0

`internal/sstp/client.go`. Часть Windows SSTP умеет только TLS 1.0 или 1.1. `MinVersion` TLS 1.0 только на этом наборе. Фронты оверлея — TLS 1.2 и новее.

## Хеш пароля Trojan (SHA-224)

`internal/trojan/proto.go`. Идентификатор в Trojan — `hex(SHA224(password))`. Так устроен Trojan, это не KDF ключей loft.

## GET ростера без проверки CA

`internal/transport/roster_fetch.go`. JSON ростера подписан ключом контроллера. Сертификат TLS по URL загрузки — не то, чему мы верим. Skip-verify здесь не ослабляет аутентификацию оверлея (`lt_` / `pbk`).

## Mask: TLS «как у origin» без проверки CA

`internal/transport/mask.go`. Клиент снимает размеры TLS-записей с origin, чтобы паддинг повторял этот origin. Доверие — `lt_` / `pbk`, не публичный CA origin.

## loft-probe skip-verify

`clients/go/cmd/loft-probe/probes.go`. Проба: дотягивается ли TLS handshake до назначения. Это доступность, не решение о доверии. Для HTTPS то же делает флаг `-insecure`.

## loft-probe не печатает `authKey`

CodeQL тащит `authKey` из `internal/xtls/reality.go` (ключ HKDF для AEAD REALITY) в `printResults`. loft-probe это поле не читает. `Detail` — RTT, TLS или HTTP-статус пробы.
