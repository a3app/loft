# Loft · Sailfish OS 客户端

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

手机应用加一条 ConnMan 覆盖网。标志、色板、Avenir Next：[`brand/`](../../../docs/brand/)。交互模型：[`index.html`](index.html)。协议：[docs/protocol_zh.md](../../../docs/protocol_zh.md)。导入 URI：[docs/share_zh.md](../../../docs/share_zh.md)。

QML 容器在 [`../qml/`](../qml/)。活会话是 `loft-front`，不是 gomobile。薄宿主已经能粘贴 YAML；这个模型是要换掉粘贴屏的 GUI。

应用是 **Loft**。协议是 **loft** / **LOFT**。

---

## 1. 产品

入站是系统覆盖网那一行（ConnMan）。一次一条配置：loft（`lt_…`，没有密码）、SSTP（用户 + 密码）、VLESS（`vless://`）或 Trojan（`trojan://`）。

v1：磁盘上的命名连接，导入/导出 `.loft`，打开/断开，不要恢复直连，按区域分流。loft YAML 里 `select: auto`。一次一条 tun。

`.loft` 文件里是 **一条或多条连接**。类型可以混：`loft`、`sstp`、`vless`、`trojan`。列表副标题是 **类型 · 传输 · 主机**（`loft · get · cdn.example.com`）。loft 地址是字符串 `loft://`。规格：[docs/share_zh.md](../../../docs/share_zh.md)。

分享：扫二维码，或载入带 `loft://`、`sstp://`、`vless://`、`trojan://` 或 `.loft` 的文件/图片。

设置里显示的就是插件拉起的那条 VPN。应用不替代设置。Aurora OS 用同一套 GUI。

适配器语言是 **Qt 5 / QML Silica**。模型是 HTML；后面的 GUI 是 Silica 页（不用 Widgets，不用 Compose）。

## 2. 屏幕

| 屏幕 | 内容 |
|--------|------|
| 引导 | 第一次打开：粘贴 `lt_…` + URL，扫二维码，或选 `.loft`。然后如果系统要问，走 ConnMan 那张同意页。 |
| 主页 | 灯、连接名、打开/断开。Overlay IP、前置、字节。 |
| 正在打开 | 灯暗，文案「正在连接…」。取消回到主页，已关闭。 |
| 连接 | 配置列表。副标题：类型 · 传输 · 主机。点一下选中（并会重连）。推进编辑，删除带确认。长按：编辑 / 导出 `.loft` / 删除。 |
| 扫描 | 相机二维码。从文件。粘贴 `loft://` / `sstp://` / `vless://` / `trojan://`。确认：名称、类型、传输、主机；密钥藏着。 |
| 编辑 | 名称、类型（`loft` / `sstp` / `vless` / `trojan`）。字段跟类型走。loft 和 VLESS：胶囊 **链接 / 手动**。loft 链接是 `loft://` 字符串；手动是访问密钥、主机、端口、传输 get / ws / h2 / quic / h3、路径。两边写同一份配置。VLESS 链接是 `vless://` 字符串；手动是 UUID、主机、端口、TLS·REALITY、TCP·XHTTP、TLS 主机名、指纹，以及该模式要的字段。证书校验。两边都写成 `vless://`。SSTP：用户、密码、服务器、TLS 主机名、证书校验、PAP/CHAP。Trojan：密码、`trojan://` 服务器、TLS 主机名、证书校验。把选中出站分享成二维码。导出这条连接的 `.loft`。 |
| 设置 | 不要恢复直连、分流（区域）、关于（软件 + 协议 loft/1.0）。导入 `.loft`。全部导出成 `Family.loft`。 |
| 封面 | 灯 + 打开/断开。从主屏 Peek。 |
| 日志 | loft-front 最近几行。没有密钥材料。藏在「高级」后面。 |

## 3. Sailfish 外壳

不要改 设置 → VPN 的样子。模型只说明 **Loft** 在 *他们* 的列表里长什么样：状态 Connected，类型 Loft，开关是系统那一行，不是我们应用里第二个开关。

我们的打开写 YAML，请 ConnMan 激活 loft 连接。没有插件时，应用拉起带 loopback SOCKS 的 `loft-front`。

相机权限是第一次打开扫描时的系统页。文件：Documents 权限。

URL scheme `loft`，点 `loft://…` 会提议导入。

不要恢复直连，落到 ConnMan 默认路由还占着。文案用 *路由* / *直连网络*。

区域：经 Loft / 绕过 Loft。方向活在区域上。

## 4. 视觉

跟 iOS、Android、macOS 同一套 loft 记号：Ink `#1A1714`，Lamp `#E8B84A`，Paper `#E8DFD2`，Avenir Next。模型里的手机外壳是 loft（深色纸）。Silica pulley 和封面是 Sailfish；不抄 Material 或 iOS 导航。

字体：标题 Demi，正文 Regular，密钥 SF Mono / Sail Sans。已连接 = 灯亮。已关闭 = 灯灭，石头色 L。

安全区：状态栏 + pulley。一列。拇指够得到：打开是主页上的大控件。

## 5. 文案

规范语言是英文。界面默认俄语，跟 macOS 一样。

| id | English |
|----|---------|
| connect | Open Loft |
| disconnect | Disconnect |
| connecting | Connecting… |
| menu_about | About Loft |
| split.via | Through Loft |
| split.bypass | Around Loft |
| about.loft | loft/1.0 |
