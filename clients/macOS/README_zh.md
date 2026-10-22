# loft · macOS

[English](README.md) · [Русский](README_ru.md) · **中文**

菜单栏客户端。最低系统：**macOS 10.13**。UX：[`design/DESIGN_zh.md`](design/DESIGN_zh.md)。模型：[`design/index.html`](design/index.html)。

```
make app          # ../../dist/clients/macOS/loft.app  （版本 patch +1）
make dmg          # ../../dist/clients/macOS/loft-*-macos-<arch>.dmg
make dmg BUMP=0   # 同一份 DMG，VERSION 不动（CI）
make dmg ARCH=x86_64 BUMP=0   # Intel 构建（从 Apple Silicon 交叉）
make dmg ARCH=arm64 BUMP=0    # Apple Silicon 构建（从 Intel 交叉）
make dmgs         # 两份 DMG；第一份可能把 VERSION +1，第二份不动
make run
make test-crypto  # RFC 8439 向量 → dist/clients/macOS/obj/<arch>/test_aead
make shot         # 窗口 PNG → /tmp/loft-shots
make front-probe  # 一次 loft-front 握手，没有 TUN
```

仓库根目录：`make macos` / `make macos-dmg`。

DMG 里是 `loft.app`、**Applications** 符号链接（拖进去安装）和 [`pack/README.txt`](pack/README.txt)。

签名是 ad-hoc，直到钥匙串里有 Developer ID：

```bash
# 本地
make dmg

# 钥匙串里第一个 Developer ID Application
CODESIGN_IDENTITY=auto make dmg

# 显式
CODESIGN_IDENTITY="Developer ID Application: Name (TEAMID)" make dmg

# 按需公证（钥匙串配置来自 `xcrun notarytool store-credentials`）
CODESIGN_IDENTITY=auto NOTARY_PROFILE=loft make dmg
```

`pack/sign.sh` 签 `loft-priv`、`loft-front`、Swift dylib，然后签 bundle。Entitlements：[`pack/entitlements.plist`](pack/entitlements.plist)（网络 + `loft-front` 的 unsigned executable memory）。没有 App Sandbox — TUN 和 setuid 助手需要关掉它。

`.o` 和 `test_aead` 写到 `dist/clients/macOS/obj/<arch>/`，和应用放一起。AEAD 测试源码：[`tests/test_aead.c`](tests/test_aead.c)。`--probe` 和 `--shot` 留在应用二进制上，因为需要 AppKit。

每次 `make app` 把 [`VERSION`](VERSION) 的 patch +1（`1.0.0` → `0.9.1` → `0.9.2`）并盖进 `Info.plist`。关于读这个字符串。协议软件版本仍是仓库根的 `VERSION`。`BUMP=0` 盖当前文件，不改它。DMG 名带 `ARCH`（`arm64` / `x86_64`），默认 `uname -m`。CI（`make macos-dmgs BUMP=0`）在 Apple Silicon runner 上打两份：本机 arm64 和 `ARCH=x86_64` 交叉。

界面是 Swift 5 + AppKit（Go 1.20 之后无法把 Go 编进进程并打 10.13）。Swift stdlib 拷进 `Contents/Frameworks`。覆盖网会话是 **`loft-front`**（Go），在 `Contents/MacOS/`，和 `loft` 挨着。助手以 `-macos=10.13` 链接；[`gooverlay/`](gooverlay/) 替换标准库 `crypto/x509` 的 Darwin 校验，这样 dyld 不需要 macOS 12 的 Security 符号。`loft-front` 说 GET、WS、H2、QUIC 和 H3，mux，clockless auth，mask，CmdLeave，覆盖网 ICMP 当作 Dgram `:1`。Hello caps 来自 Go 客户端，不是 `GETFront.swift`。

| 本机 | 信道上 |
|------|--------|
| TUN（utun，默认路由）经 loft-front | GET、WS、H2、QUIC 或 H3 前置，协议 1.0 |
| SSTP | 到 Windows/accel-ppp SSTP 服务器的 PPP-over-HTTPS |

第一次打开会要管理员密码（`ifconfig` / `route`）。Hold 在 Loft 挂了之后关掉直连默认路由。**重置路由** 把它还回去。

一条配置是 `~/Library/Application Support/loft/profiles/` 下的一份 `client.yaml` / `.loft`。导入、导出、手工改都在 **打开 Loft…**。

图标：`../../docs/brand/`（`app/loft.icns`，`menubar/loftTemplate.png`）。标志、色板、字体：[`brand/README_zh.md`](../../docs/brand/README_zh.md)。
