# loft · Android 客户端

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

手机应用加一个 `VpnService`。标志、色板、Avenir Next：[`brand/`](../../../docs/brand/)。交互模型：[`index.html`](index.html)；每个状态都有自己的链接，比如 `index.html#edit-sstp`、`#settings-route`、`#home-off`（点到哪儿地址就跟到哪儿）。协议：[docs/protocol_zh.md](../../../docs/protocol_zh.md)。导入 URI：[docs/share_zh.md](../../../docs/share_zh.md)。

Java 容器在 [`../app/`](../app/)。活会话是 `LoftVpnService` 里的 Go，不是 `loft-front`。薄宿主已经能粘贴 YAML；这个模型是要换掉粘贴屏的 GUI。

---

## 1. 产品

入站是系统 VPN（`VpnService`）。一次一条配置：loft（`lt_…`，没有密码）、SSTP（用户 + 密码）、VLESS（`vless://`）或 Trojan（`trojan://`）。

v1：磁盘上的命名连接，导入/导出 `.loft`，连接/断开，没有 VPN 就不上网（用 Android 系统开关），按协议列表分流。loft YAML 里 `select: auto`。一次一条隧道。

`.loft` 文件里是 **一条或多条连接**。类型可以混：`loft`、`sstp`、`vless`、`trojan`。列表行显示主机和 **类型 · 传输**（`loft · get`）。loft 地址是字符串 `loft://`。规格：[docs/share_zh.md](../../../docs/share_zh.md)。

分享：扫二维码，或载入带 `loft://`、`sstp://`、`vless://`、`trojan://` 或 `.loft` 的文件/图片。

Android 设置里显示的就是应用拉起的那条 VPN。应用不替代设置。

适配器语言是 **Java 17**。gomobile 已经吐 Java；这个进程不装 Kotlin runtime。模型是 HTML；后面的 GUI 是 Views（不用 Compose）。

Android TV / Google TV 是同一个 `VpnService` 和 AAR，加 Leanback flavor。不是这个模型；[计划](../PLAN_zh.md) 第 4 阶段。VIDAA / Tizen / webOS 不做。

## 2. 屏幕

和 iOS、macOS 一样的几个屏，用 Material 外壳：底部导航栏，顶栏带「←」或「✕」，FAB，底部弹出页。系统返回在哪儿都等于「←」。

| 屏 | 内容 |
|----|------|
| 主页 | 标志、`loft`、状态胶囊（点一下 → 列表）、大按钮连接 / 断开。关着：胶囊「未连接 · Home ›」。连接中：胶囊「正在连接: Home…」，按钮 **取消**，火花「握手」。简化界面关掉时有卡片 **方式 / 下载 / 上传** 和火花「近一分钟速度」（↓ 灯色，↑ 石色）；点卡片弹出 **详情**（loft 网络地址、方式、MTU、服务器）。下面，连着时有 **外部 IP** 和 **Overlay IP** 两行，和 macOS 弹出层一样（简化界面没有）。下面是导航栏。 |
| 连接 | 标题，副标题「每条有自己的密钥和地址。同时只激活一条。」行：紧凑连接/断开、名称、主机，旁边类型（`loft · get` / `sstp` / `vless` / `trojan`），开着的那条另有 **当前**。点一下打开这条连接；只有「连接」按钮切换隧道。**向右滑** 编辑，**向左滑** 删除（有对话框），`ItemTouchHelper`。**长按** 菜单：编辑 / 导出 `.loft`（仅选中） / 删除。**FAB `+`** 打开一页：扫描二维码 · 粘贴链接 · 从文件 · 手动。屏上没有手势提示。 |
| 编辑 | 全屏对话框：顶上 **✕** 回到打开它的地方。新连接标题「新连接」，类型 loft / SSTP / VLESS / Trojan，顶栏 **扫描**。已有的连接只显示自己的类型（不能换），顶栏 **⋮** 菜单（分享二维码、导出 `.loft`），最下面红色 **删除**，带对话框。loft 和 VLESS：**链接 / 手动**（图标：链环和铅笔）；链接显示服务器，只藏密钥，显示 打开全部。loft 手动：访问密钥、主机、端口、方式（**自动**·GET·WS·H2·QUIC·H3）、路径。VLESS 手动：UUID、主机、端口、TLS·REALITY、TCP·XHTTP、TLS 主机名、指纹和该模式的字段。SSTP：用户、密码、服务器、证书校验、PAP/CHAP。Trojan：密码、`trojan://` 服务器、证书校验。没有接口开关：隧道总是 `VpnService`。折叠的 **更多选项**：mux（loft）、DNS、DNS 写入系统；SSTP 和 Trojan 的 TLS 主机名在这里，SSTP 还有「只用这些地址」。保存。 |
| 引导 | 开始：**扫描二维码** 和 **粘贴链接**（已复制的链接直接添加）；「手动输入密钥和地址」进入两步：**1 访问密钥**（`lt_…`）→ 继续，**2 地址**：URL；**更多选项** 里收着方式（先是 **自动**）和路径 → 连接。然后 Android 系统的 **连接请求** 一次；屏上提示点「确定」。 |
| 设置 | 一个列表，分组 **通用 / 路由 / 连接**。顶栏重置路由。链接 **默认 \| 日志 \| 关于**；「默认」先确认。通用：简化界面、速率单位、语言、扩展日志。路由：默认路由、**没有 VPN 就不上网**（不是开关：在 Android 上就是系统的「始终开启的 VPN」和「屏蔽未使用 VPN 的连接」，有 **打开 VPN 设置** 链接）、分流和 **列表** 按钮。连接：**启动时连接**、**断线自动重连**、外部 IP。 |
| 日志 | 从设置进入，顶上 **←**，导航栏留着。前置和会话行，密钥缩短。复制 / 分享 / 清除（带对话框）。 |
| 关于 | 从设置进入，顶上 **←**。标志、loft、版本、协议 loft/1.0 · SSTP · VLESS · Trojan、简介、「Made with love by A³」。 |
| 扫描 | 顶上 **✕**。相机二维码。从文件（SAF）/ 从相册（Photo Picker）。「或粘贴链接」输入框，按钮 **粘贴** 和 **添加**。确认页显示名称、类型、传输、主机；密钥藏起来。 |
| 分享二维码 | 顶上 **✕**。二维码和 `loft://` 那行、密钥提醒、**分享** 和 **复制链接**。 |

**简化界面**：主页是标志、胶囊、连接 / 断开、导航栏。卡片和火花藏起来。

### 分流

沿用 macOS 的模型（macOS DESIGN §5）：**区域** 走 Loft 或绕过 Loft，**服务**（名称 + CIDR）只是属于某个区域，**忽略** 是不写入路由、拉取列表后仍保留的 CIDR。内置区域 Local（绕过，局域网）和 Global（走 Loft，`global.bat`），自己的区域在编辑器里加。Android 上「走 Loft」是 `VpnService.Builder.addRoute`，「绕过」是 `excludeRoute`（Android 13+）；老版本从隧道路由里减掉。隧道关着时本来就全部直连。

入口：设置 → 路由 → 开关和金色的 **列表**。这些屏在设置标签里打开，顶上 **←**。屏和 iOS 一样：列表（开关、带服务数的区域标签、`Global · 走 Loft`、搜索、服务行、**+ 添加**、导入 · 导出 · 仓库）、网络（路由 / 忽略、网段、服务、区域）、区域、区域编辑（名称、方向、列表地址 + **列表**、自动更新；自己的区域能删，服务 → Global）、仓库、目录（全球、俄罗斯、中国、伊朗、哈萨克斯坦、白俄罗斯）、导入（通过 SAF 从文件）、导出（所有区域写进一个 loft-routes 文件，分享页；没有 Keenetic .bat 格式）。

## 3. Android 外壳

不重画 设置 → 网络和互联网 → VPN。模型展示 **loft** 在 *系统* VPN 列表里的样子：状态已连接，开关是系统隧道，不是我们应用里第二个开关。

连接时先调一次 `VpnService.prepare`，再 `startForegroundService(LoftVpnService)`。用户在设置里关了 loft，应用显示已关闭。

VPN 权限是系统的 **连接请求** 对话框，一次。之后连接不再问。

相机权限是第一次扫描时的系统页。相册用系统 Photo Picker（不要宽泛的媒体权限）。SAF：图片、文本、YAML、`.loft`。

intent 过滤 `loft`：点 `loft://…` 提示导入。`.loft` 文件走同一个识别器。

### 列表手势

| 手势 | 动作 |
|------|------|
| 点一下 | 打开这条连接（编辑）。连接靠紧凑的「连接」按钮；另一条开着的话先断开。 |
| 向左滑 | 删除，带确认对话框。 |
| 向右滑 | 编辑。 |
| 长按 | 菜单：编辑；选中的行有导出 `.loft`；删除。 |
| 导出 | 弹出页：这条连接 `Name.loft`；当前前置的二维码 `loft://`；全部连接 `Family.loft`。 |

活着的会话是 **前台服务**（类型 `connectedDevice`）。通知栏里：名称、类型 · 传输 · overlay IP、断开。API 33+ 还有一次通知权限页。

Android 上的 **没有 VPN 就不上网** 就是系统的 **始终开启的 VPN** 和 **屏蔽未使用 VPN 的连接**。应用打开那页设置；开关由用户自己拨。

快捷设置开关：点一下连接或断开当前配置。灯亮 = 在线。

## 4. 视觉

和 iOS、macOS 同一套 token：Ink `#1A1714`、Lamp `#E8B84A`、Paper `#E8DFD2`、Avenir Next。应用外壳是 loft（深色纸），不是紫色的 Material You；组件用 Material：底部导航栏（当前标签是图标下的灯色胶囊）、带「←」/「✕」和 ⋮ 菜单的顶栏、56 dp 圆角 16 dp 的 FAB、底部弹出页和对话框。

字体：标题 Demi，正文 Regular，密钥 Roboto Mono。在线 = 灯窗亮。关 = 灯灭，石色 L。

模型里的系统界面（连接请求、设置 → VPN、通知栏、快捷开关）保留 Android 原生外壳，让产品边界看得见。

## 5. 文案

共用的文案——按钮、设置、路由——来自 `clients/macOS/lang/*.lang`，macOS、iOS 和 Android 上说法一样。Android 只在没有对应的地方用自己的文案：连接请求、系统的保持开关、通知栏、快捷开关、「从相册」。模型有俄语、英语和中文。

| id | EN | RU |
|----|----|----|
| connect | Connect | Подключить |
| connecting | Connecting… | Подключение… |
| disconnect | Disconnect | Отключить |
| pill_off | Not connected · Home › | Не подключено · Home › |
| key_title | Access key | Ключ доступа |
| hold | No internet without the VPN | Без VPN — без интернета |
| auto | Connect at launch | Подключать при запуске |
| reconnect | Reconnect if it drops | Переподключаться при обрыве |
| split | Split routing | Раздельная маршрутизация |
| scan | Scan QR | Сканировать QR |
| from_gallery | From gallery | Из галереи |
| open_sys | Open VPN settings | Открыть Настройки VPN |
| always | Always-on VPN | VPN всегда включён |
| block | Block connections without VPN | Блокировать без VPN |
| consent | Connection request | Запрос подключения |
