# loft · iOS 客户端

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

手机应用加一条 Packet Tunnel。标志、色板、Avenir Next：[`brand/`](../../../docs/brand/)。交互模型：[`index.html`](index.html)。协议：[docs/protocol_zh.md](../../../docs/protocol_zh.md)。导入 URI：[docs/share_zh.md](../../../docs/share_zh.md)。

SwiftUI 容器以后住在 [`../App/`](../App/)。活会话是 Network Extension 里的 Go，不是 `loft-front`。

这个模型跟着 **正在出货的 macOS GUI**（`clients/macOS`、`L10n.swift`，状态 / 列表 / 编辑 / 设置 / 引导 / 日志 / 关于）落到手机上。文案、字段和信息架构跟那个客户端对齐。iOS 留一条 Packet Tunnel、相机扫描、列表滑动/长按、系统 VPN 那一行。不长 SOCKS 入站、Dock、登录项、也不开几条并行隧道。

---

## 1. 产品

入站是系统 VPN（Packet Tunnel）。一次一条配置：loft（`lt_…`，没有密码）、SSTP（用户 + 密码）或 VLESS（`vless://`）。

v1：磁盘上的命名连接，导入/导出 `.loft`，打开 Loft / 断开，不要恢复直连，按协议列表分流。loft YAML 里 `select: auto`。一次一条隧道。

`.loft` 文件里是 **一条或多条连接**。类型可以混：`loft`、`sstp`、`vless`。界面里 loft 地址是字符串 `loft://`。规格：[docs/share_zh.md](../../../docs/share_zh.md)。

分享：扫二维码，或载入带 `loft://`、`sstp://`、`vless://` 或 `.loft` 的图片/文件。

iOS 设置里显示的就是应用拉起的那条 VPN。应用不替代设置。

Apple TV（tvOS 17+）是 iPhone GUI 之后同一条 Packet Tunnel。[计划](../PLAN_zh.md) 第 6 阶段。这个模型是手机。

---

## 2. 屏幕

跟 macOS 窗口同一套房间，收成一列，拇指够得到。

| 屏幕 | 内容 |
|--------|------|
| 主页 | 状态窗。标志、`loft`、状态胶囊（点一下 → 列表）、大按钮打开 Loft / 断开。简化界面关掉时有卡片 **OVERLAY / 前置 / 隧道** 和火花。下面是标签栏。正在打开也用这个屏：胶囊「未连接」，动作「正在连接…」，火花「握手」。 |
| 连接 | 标题、`+`，副标题「每条有自己的密钥和地址。同时只激活一条。」行：紧凑打开/断开、名称、**主机**副标题，旁边 **当前** 或类型（`get` / `sstp` / `vless`），淡色编辑。**向右滑** 编辑，**向左滑** 删除。**长按** 菜单：编辑 / 导出 `.loft`（仅选中） / 删除。**导入** 在顶栏 `+` 旁边；导出在选中的那一行和它的长按菜单里。下面是标签栏。没有「多个」开关。 |
| 编辑 | 模态：顶上 **取消** 回到打开它的地方。标题「连接」，顶栏 **QR**。名称。胶囊 **VPN 类型** loft / SSTP / VLESS / Trojan。页签 **连接 / 高级**。loft 和 VLESS：胶囊 **链接 / 手动**。loft 链接是带显示/隐藏的秘密 `loft://`；手动是访问密钥、主机、端口、GET·WS·H2·QUIC·H3、路径。两边写同一份配置。VLESS 链接是 `vless://` 字符串；手动是 UUID、主机、端口、TLS·REALITY、TCP·XHTTP、TLS 主机名、指纹，以及该模式要的字段。证书校验。两边都写成 `vless://`。没有 SOCKS 字段。隧道接口 = Packet Tunnel（强制开：没有别的入站）。SSTP：用户、密码、服务器、TLS 主机名、证书校验、PAP/CHAP。Trojan：密码、`trojan://` 服务器、TLS 主机名、证书校验。高级：mux（loft）、DNS、DNS 写入系统；SSTP 还有「只用这些地址」。保存，最后是删除。 |
| 引导 | 两步，跟 macOS 一样。**1 访问密钥**（`lt_…`）→ 继续，或导入文件/二维码。**2 地址**：URL、怎么连 GET/WS/H2/QUIC/H3、路径 → 打开 Loft。然后系统那张 iOS VPN 权限页，一次。 |
| 设置 | 三个页签 **通用 / 路由 / 客户端**。顶栏重置路由。链接 **默认 \| 日志 \| 关于**。iOS 子集：语言、简化界面、速率单位、扩展日志；默认路由、不要恢复直连、分流（没有故障切换）；自动连接、重连、外部 IP。没有 Dock、登录项、多条出站、SOCKS。 |
| 日志 | 从设置进入：顶上 **‹ 设置**，标签栏留着。前置和会话行。密钥会缩短。复制 / 清除。 |
| 关于 | 从设置进入：顶上 **‹ 设置**。标志、loft、`1.0.22`，协议 loft/1.0 · SSTP · VLESS · Trojan，简介，「Made with love by A³」。 |
| 扫描 | 模态：顶上 **取消**。相机二维码。从文件 / 从照片。粘贴 `loft://` / `sstp://` / `vless://`。 |
| 分享二维码 | 模态：顶上 **关闭**。一条前置的 `loft://`。多条前置或多条连接 → `.loft`。提醒文件里有密钥。 |
| iOS 设置 → VPN | 系统列表。loft Connected，类型 loft。开关是系统隧道。 |

**简化界面**（设置 → 通用）：主页是标志、胶囊、打开 Loft / 断开、标签栏。卡片和火花藏起来。列表仍有打开/断开和名字。设置还是全的。

只有 macOS 才有、这个模型不画的外壳：菜单栏弹出层、SOCKS5 卡片和端口、Dock 图标、登录项、同时多条出站、拖放托盘。

---

## 3. iOS 外壳

不要改 设置 → VPN 的样子。模型只说明 **loft** 在 *他们* 的 VPN 列表里长什么样：状态 Connected，类型 loft，开关是系统隧道，不是我们应用里第二个开关。

打开 Loft 调 `NETunnelProviderManager`。用户在设置里关了 loft，应用显示已关闭。

VPN 权限是系统那张页，一次。之后打开 Loft 不再问。

相机权限是第一次打开扫描时的系统页（`NSCameraUsageDescription`）。照片走 `PHPicker`（不要完整相册权限）。文件选择器认图片、文本、YAML、`.loft`。

URL scheme `loft`，点 `loft://…` 会提议导入。

### 列表手势

手机上用邮件 / 文件那套词。macOS 列表行上有淡色编辑；iOS 加上滑动和长按，因为没有指针。

| 手势 | 动作 |
|---------|--------|
| 点一下 | 选中。如果另一条已经开着，先断开再打开这条。 |
| 紧凑打开 / 断开 | 跟 macOS 列表按钮一样（`打开` / `断开`）。 |
| 向左滑 | 删除，带确认页。 |
| 向右滑 | 编辑。 |
| 长按 | 菜单：编辑；这条选中时导出 `.loft`；删除。 |
| 选中行上的导出 | 页：这条连接存成 `Name.loft`；活前置的二维码 `loft://`；所有连接存成 `Family.loft`。页上再写一遍导出提醒。 |

---

## 4. 视觉

跟 macOS 同一套 loft 记号：Ink `#1A1714`，Lamp `#E8B84A`，Paper `#E8DFD2`，Avenir Next。模型里的手机外壳是 loft（深色纸），不是 iOS 分组灰。

字体：标题 Demi，正文 Regular，密钥 SF Mono。已连接 = 灯窗亮。已关闭 = 灯灭，石头色 L。

安全区：状态栏 + Home 指示条。一列。拇指够得到：打开 Loft / 断开是主页上的大控件。标签栏 **主页 · 连接 · 设置** 坐在 Home 指示条上，出现在主页、连接、设置以及从设置进入的屏（日志、关于）。编辑、扫描和分享二维码是模态，没有标签栏；引导是全屏。

扫描：深色取景，灯色角括号，取景下面两个动作（从文件 / 从照片）。

VPN 类型是 **胶囊**（选中的类型用灯色填），连接/高级和设置页签跟 macOS 同一套分段。

---

## 5. 文案

规范字符串是 macOS GUI 的 `L10n.swift`。模型默认俄语（`?lang=en` 或 RU/EN 控件）。主页动作是 **Open loft**，不是 Connect；列表紧凑按钮是 **Connect**。密钥是 **Access key**，不是 Member key。

| id | EN | RU |
|----|----|----|
| connect | Open loft | Открыть лофт |
| pop_connect | Connect | Подключить |
| disconnect | Disconnect | Отключить |
| connecting | Connecting… | Подключить… |
| pill_on | Connected | Подключено |
| pill_off | Not connected | Не подключено |
| journal | Journal | Журнал |
| reset | Reset routes | Сбросить маршруты |
| settings | Settings | Настройки |
| conns | Connections | Подключения |
| conns_sub | Each has its own key and address. One is active. | Каждое — свой ключ и адрес. Активное одно. |
| active | active | активно |
| edit | Edit | Править |
| delete | Delete | Удалить |
| import / export | Import / Export | Импорт / Экспорт |
| warn_export | The file contains the key. Do not give it away. | В файле есть ключ доступа. Не передавайте посторонним. |
| vpn_type | VPN type | Тип VPN |
| loft_uri | loft:// address | Адрес loft:// |
| edit_main / edit_adv | Main / Advanced | Основные / Расширенные |
| key_title | Access key | Ключ доступа |
| url_title | Address | Адрес |
| url_go | Open loft | Открыть лофт |
| simple | Simplified interface | Упрощенный интерфейс |
| hold | Don’t restore direct internet | Не открывать прямой интернет |
| split | Split routing | Раздельная маршрутизация |
| default_route | Allow default route | Маршрут по умолчанию |
| blurb | From the street — ordinary HTTPS. Inside, a network of its own. | С улицы — обычный HTTPS. Внутри горит своя сеть. |

状态卡片：**OVERLAY** 保持英文。FRONT / TUNNEL 跟界面语言走（`前置` / `隧道`）。TUNNEL 顶替 macOS 的 SOCKS5 卡片：开着时是 MTU，关着是 `—`。

---

## 6. 对照 macOS GUI

| macOS | iOS 模型 |
|-------|----------|
| 状态窗 | 主页 |
| 点胶囊 → 连接 | 一样 |
| OVERLAY / FRONT / SOCKS5 | OVERLAY / 前置 / 隧道（MTU） |
| 连接 + 「多个」开关 | 连接，只能互斥 |
| 拖入 `client.yaml` / `.loft` / 二维码 | 导入 / 扫描 / 文件 / 照片 |
| 编辑 SOCKS5 + 隧道 | 只有隧道（Packet Tunnel） |
| 设置里的登录项、Dock、SOCKS、多个 | 没有 |
| 设置里的故障切换 | 没有（隧道就一条） |
| 关于 / 日志 | 同一套文案和布局，手机一列 |
