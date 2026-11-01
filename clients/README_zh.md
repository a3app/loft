# 客户端

[English](README.md) · [Русский](README_ru.md) · **中文**

| | |
|--|--|
| [`go/cmd/loft`](go/cmd/loft/) | CLI：SOCKS5、TUN。协议 1.0 参考实现 |
| [`go/cmd/loft-front`](go/cmd/loft-front/) | 会话进程。macOS 和 Sailfish 会拉起它 |
| [`go/cmd/loft-probe`](go/cmd/loft-probe/) | 经会话打 ICMP/TCP/UDP/DNS/HTTP |
| [`go/cmd/loft-bench`](go/cmd/loft-bench/) | 自动压 GET/WS 吞吐，对照 macOS 打开那条路径 |
| [`macOS/`](macOS/) | 菜单栏应用。UX：[`macOS/design/DESIGN_zh.md`](macOS/design/DESIGN_zh.md) |
| [`NDMS/`](NDMS/) | Entware / OpkgTun。`make -C clients/NDMS ipk`。页面 `http://<lan>:8090/`。 |
| [`MikroTik/`](MikroTik/) | RouterOS 7 容器。模型：[`MikroTik/design/index.html`](MikroTik/design/index.html) |
| [`iOS/`](iOS/) | Packet Tunnel。薄宿主 + appex。模型：[`iOS/design/index.html`](iOS/design/index.html)。Apple TV：[`iOS/PLAN.md`](iOS/PLAN.md) 第 6 阶段 |
| [`Android/`](Android/) | VpnService。薄 Java 宿主。手机和电视 flavor。模型：[`Android/design/index.html`](Android/design/index.html) |
| [`Windows/`](Windows/) | 托盘客户端。x86/x64 为 Win7 SP1+；arm64 为 Win10/11 ARM。Win32 外壳 + loft-svc。UX：[`Windows/design/DESIGN_zh.md`](Windows/design/DESIGN_zh.md) |
| [`Sailfish/`](Sailfish/) | ConnMan 覆盖网。薄 QML 宿主 + loft-front。Aurora OS 是 OMP RPM。模型：[`Sailfish/design/index.html`](Sailfish/design/index.html) |
| [`mobile/`](mobile/) | 共用 Go 引擎（`gomobile bind`）。没有 GUI。 |

共用代码：`internal/`。产物：`dist/clients/`。

```bash
make client        # dist/clients/go/loft
make front         # dist/clients/go/loft-front
make probe         # dist/clients/go/loft-probe
make bench         # dist/clients/go/loft-bench
make macos         # dist/clients/macOS/loft.app
make macos-dmg     # dist/clients/macOS/loft-*-macos-<arch>.dmg
make macos-dmgs    # arm64 和 x86_64 两份 DMG（CI）
make android       # dist/clients/Android/loft.app + loft-tv.app + loft-gtv.app + APKs
make ios           # dist/clients/iOS/loft.app（macOS）+ iphonesimulator/loft.app
make windows       # dist/clients/Windows/x64 和 x86
make dist-clients  # dist/clients/go/loft-<ver>-<os>-<arch>.tar.gz
```

协议：[docs/protocol_zh.md](../docs/protocol_zh.md)。跑起来：[docs/guide_zh.md](../docs/guide_zh.md)。
