# loft · Android TV / Google TV

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

和手机同一条会话：`LoftVpnService` + `clients/mobile` AAR。这份模型是十英尺外壳。标志、色板、Avenir Next：[`brand/`](../../../../docs/brand/)。交互模型：[`index.html`](index.html)；每个状态有自己的链接，例如 `#home-on`、`#pair`、`#settings-route`。手机模型：[`../index.html`](../index.html)。协议：[docs/protocol_zh.md](../../../../docs/protocol_zh.md)。导入 URI：[docs/share_zh.md](../../../../docs/share_zh.md)。目录：[README](../../README_zh.md)。

后面的 GUI 是 Leanback（Java 17 Views，遥控器）。不用 Compose。`connect` 只在节点上。自动 = GET。

---

## 1. 产品

盒子是客厅里的主机。YouTube、Play 商店和电视上的其它应用走 overlay，因为 Android 只有一条系统 VPN。常见末跳是以太网，Wi-Fi 是备用。路径变化（`NetworkCallback` → `NotifyPathChange`）保住 overlay 地址。

一次一条配置：loft / SSTP / VLESS / Trojan。磁盘上的命名连接，导入/导出 `.loft`，连接/断开，靠系统「始终开启的 VPN」开关保持，分流是 **默认路由 + 绕过局域网**。CIDR 列表编辑留在手机上。

这个树里的 flavor 是 `tv`。`LEANBACK_LAUNCHER`，横幅 320×180，不要求触摸屏，要求 Leanback。独立 `applicationId`：`app.loft.android.tv`，手机和盒子可以挂在同一个 Google 账号下。

v1 电视上没有摄像头。导入：把二维码 **画在电视上**（手机扫屏幕），U 盘或「下载」里的 `.loft`，或用遥控器把链接打进去。

VIDAA / Tizen / webOS / Roku 不做。

---

## 2. 屏幕

没有底栏、FAB、滑动。左边一条轨（主页 / 连接 / 设置），右边内容。默认焦点是主操作。系统返回一律退一层。

| 屏幕 | 内容 |
|------|------|
| 主页 | 标志、`loft`、状态（灯 + 配置名 + 时长）。巨大的 **连接** / **断开**。关闭时：右边配置卡片（名称、`类型 · 前置`、主机）。在线时：overlay IP、公网 IP、以太网/Wi-Fi 芯片，↓ / ↑ 两个大数字 — 没有速度火花图，沙发上看不清。连接中：**取消**，灯在等待。空盒子：三块添加磁贴代替连接。 |
| 连接 | 一条 Leanback 横滑。卡片：名称、`loft · get`、主机。当前这条有灯边。最后一张是 **+**。在卡片上按 OK 把它设为当前并回到主页。从横滑往下：编辑 · 分享二维码 · 删除。 |
| 添加 | 三块一样大的磁贴：**在这台电视上显示二维码** · **U 盘或下载** · **输入链接**。 |
| 配对 | 电视上大二维码和六位码（`K7M2P4`）。文案：手机打开 loft → 发到电视 → 扫这个（或输入码）。手机把 YAML POST 到局域网里一个短命监听。返回就关掉监听。 |
| 分享二维码 | 和配对同一套外壳，内容是所选配置的 `loft://`。手机扫电视拿走密钥。 |
| 文件 | Leanback 选择器，覆盖 USB OTG 和「下载」。`.loft`、`loft://`、YAML。确认：名称、类型 · 传输、主机；密钥隐藏。 |
| 输入 | Leanback 输入法。字段是 `loft://` 或 YAML。**添加**。 |
| 编辑 | 故意写得很短。名称，方式芯片 **自动 · GET · WS · H2 · QUIC · H3 · L3**，保存。完整的 loft / SSTP / VLESS / Trojan 表单留在手机。遥控器不是 YAML 编辑器。 |
| 设置 | 分组 **常规 / 路由 / 连接**。常规：语言、详细日志、崩溃报告（GitHub 渠道）。路由：默认路由、绕过局域网，**无 VPN 则无网络** 是链接 **打开 VPN 设置**（「始终开启的 VPN」和「阻止不使用 VPN 的连接」是系统开关）。连接：启动时连接、断开后重连。页脚：日志 · 关于。没有 CIDR 列表编辑。 |
| 日志 | 最近几行，密钥缩短。清除要确认。 |
| 关于 | 标志、版本、loft/1.0 · SSTP · VLESS · Trojan、说明、Made with love by A³。 |
| 授权 | Android TV 系统的 **连接请求**。一次。之后连接不再问。 |
| 系统 VPN | 设置 → 网络和互联网 → VPN。模型展示 loft 在 *他们的* 列表里。 |
| 横幅 | 给 Google TV / Play 那一行用的 320×180：深色纸、L 标、单词 `loft`，会话活着时灯亮。 |

---

## 3. 遥控器

| 键 | 动作 |
|----|------|
| ← → | 轨 ↔ 内容；沿卡片横滑。 |
| ↑ ↓ | 轨上的图标；设置行；连接下方的操作行。 |
| OK | 当前焦点。在主页就是连接 / 断开。 |
| 返回 | 退一层。配对/分享/输入/文件/编辑关闭。主页再按是 Leanback 退出。 |

焦点：3 px 灯环，外扩 4 px，卡片 1.06。最小命中 48 dp。过扫描内缩 48 dp。1080p 下标题约 34 sp，正文约 20 sp，提示约 16 sp。

笔记本上的模型用方向键、Enter、Escape 对应这张表。

---

## 4. 没有摄像头怎么导入

| 路径 | 何时 |
|------|------|
| 电视上的二维码 | 手机和盒子同一局域网。首选。 |
| 六位码 | 同一套配对，手机也没有摄像头时。 |
| U 盘 / 下载 | 已经插上的盘，或已经落到盒子上的文件。 |
| 输入 `loft://` | 最后手段。Leanback 输入法慢；字段接受蓝牙键盘粘贴。 |

手机上的扫描屏已经有了。电视是反过来的：**屏幕**就是二维码。

Hairpin、双栈、`protect(fd)`、FrontIP `excludeRoute` 是手机引擎。这份模型不重画它们。

---

## 5. 视觉

和手机同一套：Ink `#1A1714`、Lamp `#E8B84A`、Paper `#E8DFD2`、Avenir Next。电视外壳是 loft 深色纸铺满。系统对话框（连接请求、设置里的 VPN）保留 OEM 外壳，好看出产品边界。

在线 = 灯窗亮。关闭 = 灯灭，石头色的 L。不用 Material You 的紫。没有手机底栏、没有 FAB、没有快捷设置磁贴（Android TV 没有）。

---

## 6. 文案

共用按钮来自 `clients/macOS/lang/*.lang`。电视独有：三块添加磁贴、配对、U 盘、遥控器提示。各语言的 VPN 用语（始终开启的 VPN）。

| id | EN | RU |
|----|----|----|
| connect | Connect | Подключить |
| disconnect | Disconnect | Отключить |
| add_qr | Show QR on this TV | QR на телевизоре |
| add_usb | From USB or Downloads | С флешки или из Загрузок |
| add_type | Type the link | Набрать ссылку |
| pair_hint | Open loft on the phone → Send to TV | На телефоне: loft → На телевизор |
| remote_ok | OK — connect | OK — подключить |
| underlying_eth | Ethernet | Ethernet |

---

## 7. 这份模型不做

Play TV 卡片。2 GiB 盒子上的 RSS 由运营者实测（About 显示 VmRSS）。未要求时不改 Dist / VERSION。`tv` flavor、Leanback Java、横幅和十英尺 GUI 在 `app/src/tv/`。
