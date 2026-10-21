# 计划 · loft 在 Android

[English](PLAN.md) · [Русский](PLAN_ru.md) · **中文**

软件 1.0.0。协议 1.0。本计划不抬任一版本。

## 目标

手机上的 loft / SSTP / VLESS / Trojan 客户端，然后上 **Android TV / Google TV**，作为系统 VPN。用户导入访问密钥（`loft://`、`sstp://`、`vless://`、`trojan://`、`.loft`），点打开，设备流量进会话。走 loft 时，Wi-Fi / LTE / Ethernet 切换保住 Overlay IP（新的 `0x02`，同一个 `instance_id`）。

Google TV 是 Android TV 上的启动器。一个 `tv` flavor，一份 AAR，一个 `VpnService`。

## 不做

- Kotlin / Compose。适配器是 Java 17 Views（手机）和 Leanback（电视）。gomobile 已经吐 Java。
- 拉起 `loft-front`。`LoftVpnService` *就是* 会话。
- SOCKS5 当别的应用用的入站。流媒体应用不理本机代理；产品入站是 `VpnService`。
- 同时多个 VpnService（Android 只允一个）。
- 本应用里的 IKEv2 / WireGuard。SSTP 是同一条服务里的进程内 Go。
- **VIDAA、Tizen、webOS、Roku。** 没有 TUN。那些电视走 Keenetic（或插一根跑这个客户端的 HDMI 棒）。
- 第一版电视上架不单独列 Fire TV（同一个 APK 可以侧载）。
- 第一版就上 Play Store（侧载够）。
- 第一个 `v*` GitHub Release 就带 Dist / APK（要等后面的标签）。

## 阶段

### 0. 目录 — 就是这里

文档、HTML 模型（[design/](design/)）。薄 Java 宿主已经能粘贴 YAML。

### 1. 薄 VpnService

`gomobile bind` [`clients/mobile`](../mobile/)（`make -C ../mobile android` → `engine.aar`）。`LoftVpnService` 泵 tun fd，`protect(fd)`，从 Snapshot 装路由/DNS。已经写了。本机 bind 还需要 NDK。

### 2. 手机 GUI

用模型换掉粘贴 YAML 的屏：主页（灯，打开/断开）、连接、扫描、编辑、设置、引导。Java Views。默认简化界面。

### 3. 配置和整机策略

磁盘上的配置、当前指针、`lt_…` 进 Android Keystore。导入/导出 `.loft`。

- 一条当前配置（一条 VPN）。
- 不要恢复直连：深链到 **Always-on VPN** 和 **Block connections without VPN**。文案用 *路由* / *直连网络*。
- 分流：协议列表 → 路由（`via` / `direct` / `drop`）。前置 IP 走 `excludeRoute`（API 33+）；任何系统都 `protect(fd)`。
- 扫描：相机二维码 + SAF + Photo Picker。识别：`loft://`、`sstp://`、`vless://`、`trojan://`、YAML / `.loft`（一条或多条连接）。Intent `loft`。先确认再保存。列表：滑动编辑/删除、长按、选中时导出。副标题 类型 · 传输 · 主机。
- 开着时前台通知。快捷设置磁贴。

### 4. Android TV / Google TV

本目录里的产品 flavor `tv`（Leanback、方向键、横幅、`LEANBACK_LAUNCHER`）。同一份 `engine.aar` 和 `LoftVpnService`。

- 十英尺主页 / 列表 / 设置。手机布局留在 phone flavor。
- 电视没有相机：反向二维码（手机扫电视）、存储里的 `.loft`、遥控器键盘粘贴。
- 电视上的 Play 只列 leanback；phone APK 在这个 flavor 打标之前不会出现在那里。
- 先在 2 GiB 盒子上量 RSS，再承诺便宜棒。先 Shield / Google TV Streamer。

### 5. 打包

签过名的 APK / AAB。GitHub Actions 以后再说；不进第一个标签。Pixel 和 Shield 上侧载够自己先用。

## Go 目录里的顺序

1. [`clients/mobile`](../mobile/) — 已经是手机/电视引擎。没有 CLI 参数。
2. 换路：`ConnectivityManager.NetworkCallback` → `NotifyPathChange`。新的 `0x02`，同一个 `instance_id`。
3. 不要加能力。`loft-front` 留给 macOS。

## 风险

| 风险 | 怎么挡 |
|------|------------|
| 1–2 GiB 电视盒子上的 Go RSS | 先在 Shield 上量。不够就从 bind 里丢掉不用的传输。 |
| 发夹 | `protect(fd)` 加上 `excludeRoute`（API 33+）。 |
| 没有 VPN 同意页的 OEM 盒子 | 写进文档；整条隧道可能起不来。这不是 loft 的 bug。 |
| Play TV 搜索 | leanback 分类只打在 `tv` flavor。 |
| 带访问密钥的二维码 | 确认屏；URI 不进日志。电视没有相机。 |

## 做到什么算完

**手机。** API 26+ 上的成员导入 `.loft` 或 `loft://` URI，点打开，看见灯，Chrome 走 Overlay IP。Wi-Fi → LTE 这个 IP 还在。

**电视。** 同一条会话在 Android TV / Google TV 上用遥控器：没有电视相机也能导入，打开，YouTube 走 Overlay IP。Ethernet → Wi-Fi 这个 IP 还在。
