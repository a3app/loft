<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/png/loft-lockup-readme-on-dark.png">
  <img src="docs/brand/png/loft-lockup-readme.png" alt="loft — Loft Overlay For TLS" width="480">
</picture>

[English](README.md) · [Русский](README_ru.md) · **中文**

loft 是跑在 TLS 里面的覆盖网。流在 GET、WebSocket、HTTP/2、QUIC 或 HTTP/3 上做多路复用，再用 ChaCha20-Poly1305 封住。在公网路径上，会话看起来就是普通 HTTPS，所以能过 CDN、反向代理和 TLS 终结。短断和丢包撑得住。

协议 **loft/1.0**。分享链接 **`loft://`**。YAML 里 `kind: loft`。

本仓库是开放规范，加上参考节点、网页面板和客户端（macOS 应用，Linux / macOS CLI）。软件版本 `1.0.25`。第三方按 [docs/protocol_zh.md](docs/protocol_zh.md) 就能自己实现 loft。

## 目录

- [给运营方](#给运营方)
- [下载](#下载)
- [应用](#应用)
- [面板](#面板)
- [怎么跑起来的](#怎么跑起来的)
- [仓库](#仓库)
- [编译](#编译)
- [支持](#支持)
- [许可](#许可)

## 给运营方

典型用途：从外面进实验室局域网、管一机群、站点互联。成员出示的是浏览器模样的 ClientHello。中间盒要是改写了握手，会话就起不来。

## 下载

文件在 [最新 GitHub Release](https://github.com/a3app/loft/releases/latest)。每次 `v*` 标签 Dist 都按这些名字上传，所以链接永远指向当前版本。

| | Apple Silicon / arm64 | Intel / amd64 |
|--|--|--|
| macOS 应用 | [DMG](https://github.com/a3app/loft/releases/latest/download/loft-macos-arm64.dmg) | [DMG](https://github.com/a3app/loft/releases/latest/download/loft-macos-x86_64.dmg) |
| macOS CLI | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-darwin-arm64.tar.gz) | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-darwin-amd64.tar.gz) |
| Linux CLI | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-linux-arm64.tar.gz) | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-linux-amd64.tar.gz) |
| 节点 | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-node-linux-arm64.tar.gz) · [deb](https://github.com/a3app/loft/releases/latest/download/loft-node_arm64.deb) | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-node-linux-amd64.tar.gz) · [deb](https://github.com/a3app/loft/releases/latest/download/loft-node_amd64.deb) |
| 面板 | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-panel-linux-arm64.tar.gz) · [deb](https://github.com/a3app/loft/releases/latest/download/loft-panel_arm64.deb) | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-panel-linux-amd64.tar.gz) · [deb](https://github.com/a3app/loft/releases/latest/download/loft-panel_amd64.deb) |

校验和：[客户端](https://github.com/a3app/loft/releases/latest/download/loft-clients-SHA256SUMS.txt) · [节点](https://github.com/a3app/loft/releases/latest/download/loft-node-SHA256SUMS.txt) · [面板](https://github.com/a3app/loft/releases/latest/download/loft-panel-SHA256SUMS.txt)。macOS DMG 是菜单栏应用（ad-hoc 签名）。CLI 归档里是 `loft`、`loft-front`、`loft-probe`、`loft-bench`。

## 应用

macOS 客户端住在菜单栏。从那里打开覆盖网，挂着几条有名字的连接，导入 `loft://` 链接或 `.loft` 文件。SOCKS5 和 TUN 都在本机。

| 状态 | 连接 |
|:--:|:--:|
| <img src="docs/img/macos/zh-status.png" alt="loft 状态窗口" width="400"> | <img src="docs/img/macos/zh-connections.png" alt="命名连接" width="400"> |

## 面板

单台 loft 服务器的网页管理：节点、其网络与成员、IP 地址与上游通道、域名（DNS 记录与证书）、邮件、软件包、面板用户。目标：不手改文件即可安装并配置 loft 服务器。规范：[server/panel](server/panel/)。

<img src="docs/img/panel/zh-overview.png" alt="loft-panel 概览" width="800">

## 怎么跑起来的

```mermaid
flowchart LR
    subgraph Client["loft 客户端"]
        App["SSH · 浏览器 · curl"] --> In["SOCKS5 / TUN"]
        In --> Mux["mux Open / Data / Close / Dgram"]
        Mux --> Seal["ChaCha20-Poly1305"]
        Seal --> Front["get · ws · h2 · quic · h3"]
    end
    Front -->|"TLS 1.3 HTTPS"| Edge["CDN / 反向代理"]
    Edge --> Node["loft-node"]
    Node --> Dest["实验室 · 机群 · 站点"]
```

1. 应用走本机 SOCKS5 或 TUN。`Holder` 盯着这个监听口，前置怎么轮换都不撤。
2. 流共用一条 mux，用 ChaCha20-Poly1305 封住。
3. 公网路径上这就是普通 HTTPS，所以节点前面可以挂 CDN 或反向代理。
4. 短断会把 mux 停在 `gap_hold_ms`（默认 8 秒）。续上用新的 `0x02` 和同一个 `instance_id`；里面的 TCP 不断。

| | |
|--|--|
| [docs/protocol_zh.md](docs/protocol_zh.md) | 线协议。照着就能写第三方客户端或节点。 |
| [docs/architecture_zh.md](docs/architecture_zh.md) | 本仓库怎么摆。 |
| [docs/guide_zh.md](docs/guide_zh.md) | 编译、配置、跑起来、Debian。 |
| [docs/yandex-cdn_zh.md](docs/yandex-cdn_zh.md) | Yandex Cloud CDN 运营指南。 |
| [docs/keenetic_zh.md](docs/keenetic_zh.md) | KeeneticOS Entware 客户端（OpkgTun）。目录：[clients/Keenetic/](clients/Keenetic/)。 |
| [docs/mikrotik_zh.md](docs/mikrotik_zh.md) | RouterOS 7 容器客户端。目录：[clients/MikroTik/](clients/MikroTik/)。 |
| [docs/ios_zh.md](docs/ios_zh.md) | iOS Packet Tunnel 客户端。目录：[clients/iOS/](clients/iOS/)。 |
| [docs/android_zh.md](docs/android_zh.md) | Android VpnService 客户端。目录：[clients/Android/](clients/Android/)。 |
| [docs/sailfish_zh.md](docs/sailfish_zh.md) | Sailfish OS ConnMan 客户端。目录：[clients/Sailfish/](clients/Sailfish/)。Aurora OS 是 OMP 打包。 |
| [docs/share_zh.md](docs/share_zh.md) | 导入链接 `loft://` 和 `.loft` 文件（一条或多条连接）。 |

## 仓库

```
docs/             协议、架构、指南
docs/brand        标志和图标
internal/         wire, mux, crypto, transport, overlay
server/           loft-node（控制器或中继）
server/panel      loft-panel 网页面板
clients/go        loft, loft-front, loft-probe
clients/macOS     菜单栏应用（拉起 loft-front）
clients/Keenetic  Entware / OpkgTun（方案和模型）
clients/MikroTik  RouterOS 7 容器（镜像 + .rsc）
clients/iOS       Packet Tunnel（薄宿主 + appex）
clients/Android   VpnService（薄 Java 宿主）
clients/Sailfish  ConnMan 覆盖网（Silica；Aurora 是 OMP RPM）
clients/mobile    共用 Go 引擎（gomobile bind）
```

## 编译

Go 1.24+。在仓库根目录：

```bash
make test
make node       # ./loft-node
make client     # dist/clients/go/loft
make macos      # dist/clients/macOS/loft.app
make macos-dmg  # dist/clients/macOS/loft-*-macos-<arch>.dmg
make macos-dmgs # arm64 和 x86_64 两份 DMG（CI）
make dist       # 节点 tar/.deb + CLI 归档
```

打标签 `vX.Y.Z`（必须和 `VERSION` 一致）会跑 GitHub Actions：测试、`make dist`、macOS DMG arm64 和 x86_64（ad-hoc）、发 Release、ping 一下 pkg.go.dev。手动：Actions → Dist。

```bash
export LOFT_TOKEN='change-me-long-random'
./loft-node -c server/configs/server.yaml -dir /var/lib/loft
./dist/clients/go/loft -c clients/go/configs/client.yaml
ALL_PROXY=socks5://127.0.0.1:1080 curl https://example.com
```

Debian 包和安装脚本：[server/debian/README_zh.md](server/debian/README_zh.md)。

## 支持

开放的互联网需要投入。你的赞助很重要：它让协议、节点和客户端保持独立。

| | 网络 | 地址 |
|--|--|--|
| Bitcoin | Bitcoin | `bc1q3q8vzxta0yegvwkgth44a0n2ee8d9kjtjs738t` |
| Ethereum | Ethereum | `0xEfBF2e1C8B76B6033ab00199Bdf36671ed0Dba3A` |
| Tether | Ethereum（USDT ERC-20） | `0xEfBF2e1C8B76B6033ab00199Bdf36671ed0Dba3A` |
| TRON | TRON | `TQq2CGEZ7rTE7RqA6rhh2H8j24qdUqd5Jx` |
| Tether | TRON（USDT TRC20） | `TQq2CGEZ7rTE7RqA6rhh2H8j24qdUqd5Jx` |
| Litecoin | Litecoin | `ltc1qq93zr9pjst9fy732qjuy0pltze4x4llcv30w79` |
| Zcash | Zcash（透明地址） | `t1RCtApkwLxjuG2dmR7SdnHSVR1Xrx2c6Bk` |

ETH 和 USDT ERC-20 共用 Ethereum 地址。TRX 和 USDT TRC20 共用 TRON 地址。USDT 按该行的网络发送。说明：[SUPPORT_zh.md](SUPPORT_zh.md)。

## 许可

[Apache License 2.0](LICENSE)。按 [docs/protocol_zh.md](docs/protocol_zh.md) 实现 loft/1.0 不收费。
