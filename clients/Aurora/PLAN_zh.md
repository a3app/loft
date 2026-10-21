# 计划 · loft 在 Aurora OS

[English](PLAN.md) · [Русский](PLAN_ru.md) · **中文**

软件 1.0.0。协议 1.0。本计划不抬任一版本。

## 目标

Aurora OS 手机上的 loft / SSTP / VLESS / Trojan 客户端，作为系统覆盖网会话。用户导入访问密钥（`loft://`、`sstp://`、`vless://`、`trojan://`、`.loft`），点打开，设备流量进会话。走 loft 时，Wi-Fi / LTE 切换保住 Overlay IP（新的 `0x02`，同一个 `instance_id`）。

系统管 TUN（`connman-vpnd`）。Loft 管线上（前置）。GUI 是 Qt/QML Silica。

## 不做

- gomobile / `clients/mobile`。Aurora OS 是 GNU/Linux；会话是 `loft-front`，跟 macOS 一样。
- Qt Widgets。Aurora OS 对外是 Qt Quick + Silica；Widgets 不在公开 API 里。
- 第一版 GUI 用 Flutter。先原生 QML；Flutter 以后可以再来。
- SOCKS5 当别的应用用的入站。第 1 阶段可以起 SOCKS 自己先用；产品入站是 ConnMan TUN。
- 同时多条覆盖网会话（ConnMan 只持一条 VPN）。
- 本应用里的 IKEv2 / OpenVPN / WireGuard。那些还是 Aurora 设置里的系统类型。SSTP 接上以后是同一个 loft-front 里的进程内 Go。
- 第一版就上 RuStore / Aurora 商店（侧载 RPM 够）。
- 第一个 `v*` GitHub Release 就带 Dist / RPM（要等后面的标签）。

## 阶段

### 0. 目录 — 就是这里

文档、计划、HTML 模型（[design/](design/)）。薄 QML 宿主粘贴 YAML。

### 1. 薄宿主 + loft-front

交叉编译 `loft-front` `linux/arm64`（设备要的话再 `arm`）。应用写 `client.yaml`，拉起助手。SOCKS 在 loopback 上给浏览器检查。还没有 TUN。

冒烟：Hello，应用里看到 Overlay IP，curl 走 SOCKS。

### 2. ConnMan VPN 插件 — 已经写了

Aurora OS 官方 VPN 扩展：ConnMan `.so`（`loft-plugin.c`）+ 助手 `loft-connman`（exec `loft-front -tun NAME -tun-noexec`）+ `loft-script` notify，带 Overlay IP / DNS / MTU。`connman-vpnd` 建 tun 和路由。发夹：`Host` → `trusted_ip`（前置不进 tun），跟 macOS 同一思路。

设置 → 网络加载 `settings/{listitem,edit,advanced,details}.qml`，类型是 **Loft**。QML 宿主优先 D-Bus `net.connman.vpn` Create/Connect；没有插件时退回 SOCKS `loft-front`。

冒烟（要真机）：ping 覆盖网网关；系统浏览器走 Overlay IP。`.so` 只在 Aurora SDK 里编。

### 3. 配置和整机策略

YAML 配置在 `~/.local/share/app.loft.aurora/`。导入/导出 `.loft`。一条当前配置（一条 ConnMan）。

- 不要恢复直连：会话躺着时 ConnMan 默认路由还占着（文案：*路由* / *直连网络*）。
- 分流：区域列表 → ConnMan `via` / `direct` 路由。方向活在区域上。
- 扫描：相机二维码 + 文件。识别：`loft://`、`sstp://`、`vless://`、`trojan://`、YAML / `.loft`。URL scheme `loft`。先确认再保存。列表副标题 类型 · 传输 · 主机。
- 封面：灯 + 打开/断开。

### 4. 打包

签过名的 aarch64 RPM（设备要的话再 armv7hl）。Aurora SDK（`mb2` / Aurora IDE）。开发机侧载够自己先用。GitHub Actions 以后再说；不进第一个标签。

## Go 目录里的顺序

1. 会话还是 `loft-front` 进程。通用 `-tun NAME` + `-tun-noexec`（挂上，跳过地址/路由）。没有只给 Aurora 的参数。
2. 交叉：这个 Makefile 里 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`。设备 glibc 拒了二进制，就在 Aurora Build Engine 里重编。
3. 换路：ConnMan / Qt Network 通知 → loft-front 已经用新的 `0x02` 和同一个 `instance_id` 续上。不要加能力。
4. 不要给 Aurora bind `clients/mobile`。

## 风险

| 风险 | 怎么挡 |
|------|------------|
| Regular 沙箱打不开 TUN | 第 2 阶段走 ConnMan 插件；GUI 从不打开 `/dev/net/tun`。 |
| 插件要特权 RPM | 安全配置写进文档；先侧载。 |
| glibc / Go 静态二进制 | 先试 `CGO_ENABLED=0`；不行再 SDK gcc+cgo。 |
| GET/WS 发夹进 tun | 默认路由之前先排除前置 IP，跟 macOS 一样。 |
| 商店审核 | 下一个标签之前侧载。文案还是 loft，不套第三方 VPN 牌子。 |
| 没有真机 | 模拟器跑 QML；TUN 冒烟要硬件。 |

## 做到什么算完

Aurora OS 5.x 上的成员导入 `.loft` 或 `loft://` URI，点打开，看见灯，系统浏览器走 Overlay IP。Wi-Fi 切 LTE 这个 Overlay IP 还在。
