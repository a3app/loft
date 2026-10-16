---
title: loft
description: HTTPS overlay for operators. Secure admin session that holds through short disconnects and packet loss. Protocol loft/1.0.
image: https://raw.githubusercontent.com/a3app/loft/main/brand/png/github-social-preview.png
---

# loft

**English** · [Русский](README_ru.md)

From the street, ordinary HTTPS. Inside, a holdable admin channel for lossy and intermittent paths.

Protocol **loft/1.0**. Share URI **`loft://`**. Apache License 2.0.

**loft** is loft overlay for TLS. A secure admin session that holds through short disconnects and packet loss. Traffic on the public path is ordinary HTTPS, so it works through CDNs, reverse proxies, and TLS middleboxes those sites already use. This site is the open specification. The reference node and clients are in the [repository](https://github.com/a3app/loft).

## Spec

| | |
|--|--|
| [protocol.md](protocol.md) | Wire. Enough to write a third-party client or node. |
| [architecture.md](architecture.md) | This tree: controller, relay, client, overlay. |
| [guide.md](guide.md) | Build, config, decoy, roster, Debian. |
| [share.md](share.md) | Client share URI: `loft://`, `.loft` (one or several connections), QR. |
| [keenetic.md](keenetic.md) | KeeneticOS Entware client. |
| [ios.md](ios.md) | iOS Packet Tunnel. |
| [android.md](android.md) | Android VpnService. |
| [yandex-cdn_ru.md](yandex-cdn_ru.md) | Yandex Cloud CDN operator guide (RU). |

[Download](https://github.com/a3app/loft/releases/latest) · [Source](https://github.com/a3app/loft)
