# loft · Sailfish OS 客户端

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

手机应用加一条 ConnMan 覆盖网行。标志、色板、Avenir Next：[`brand/`](../../../docs/brand/)。交互模型：[`index.html`](index.html)（Sailfish OS，经典 Silica）和 [`aurora.html`](aurora.html)（ОС Аврора 5，见 §7）；模型侧栏里的切换在两者之间跳转，保持当前屏。每个状态都有自己的链接，比如 `index.html#edit-sstp`、`#settings-route`、`#home-off`、`#home-pulley`（点到哪儿地址就跟到哪儿）。协议：[docs/protocol_zh.md](../../../docs/protocol_zh.md)。导入 URI：[docs/share_zh.md](../../../docs/share_zh.md)。

QML 容器在 [`../qml/`](../qml/)。活会话是 `loft-front`，不是 gomobile。适配层是 **Qt 5 / QML Silica**；模型是 HTML，GUI 是 Silica 页面。ОС Аврора（Aurora OS）是同一棵树的 OMP 包：`aurora/`（`app.loft.aurora`）；Sailfish 的 RPM 是 `sailfish/`（`harbour-loft`）。

这个模型跟着 **正在出货的 macOS GUI**（`clients/macOS`、`L10n.swift`，状态 / 列表 / 编辑 / 设置 / 引导 / 日志 / 关于）落到手机上，经由它所来自的 iOS 模型。文案、字段和信息架构跟那个客户端对齐。Sailfish 留一条 ConnMan 隧道、相机扫描、列表长按、封面、系统 VPN 那一行。不长 SOCKS 入站、Dock、登录项、也不开几条并行隧道。

---

## 1. 产品

入站是系统覆盖网那一行（ConnMan）。一次一条配置：loft（`lt_…`，没有密码）、SSTP（用户 + 密码）、VLESS（`vless://`）或 Trojan（`trojan://`）。

v1：磁盘上的命名连接，导入/导出 `.loft`，连接 / 断开，不要恢复直连，按协议列表分流。loft YAML 里 `select: auto`。一次一条隧道。

`.loft` 文件里是 **一条或多条连接**。类型可以混：`loft`、`sstp`、`vless`、`trojan`。界面里 loft 地址是字符串 `loft://`。规格：[docs/share_zh.md](../../../docs/share_zh.md)。

分享：扫二维码，或载入带 `loft://`、`sstp://`、`vless://`、`trojan://` 或 `.loft` 的图片/文件。

设置 → VPN 里显示的就是插件拉起的那条 ConnMan **Loft** 行。应用不替代设置。

---

## 2. 屏幕

跟 macOS 窗口同一套房间，收成一列，拇指够得到，用 Silica 外壳：没有标签栏，页面栈、下拉菜单、对话框和 remorse（可撤销的延时操作）。

**导航。** 主页是根页面。它的 **下拉菜单**（pulley，往下拉）有 设置、连接、扫描二维码；页面顶上一条亮线表示这页有下拉菜单。其他页面都压进栈：左上角返回指示 **‹**（以及向回滑）退一步。连接和设置退回主页。页面标题是 Silica `PageHeader`：靠右，用高亮色（灯色）。

| 屏幕 | 内容 |
|--------|------|
| 主页 | 状态窗，也是根页面。标志、`loft`、状态胶囊（点一下 → 列表）、大按钮连接 / 断开。关着：胶囊「未连接 · Home ›」，看得出会打开哪条。连接中：胶囊「正在连接: Home…」，按钮 **取消**，火花「握手」。卡片随协议变化 —— loft：**协议** loft/1.0 · **前置** GET（没连上时「自动」）· **选项** mux · mask；SSTP：**协议** · **传输** PPP · **认证** CHAP；VLESS：**协议** · **传输** TCP · **安全** REALITY；Trojan：**协议** · **传输** TCP · **安全** TLS。左边是近一分钟的速度图，当前 ↓（灯色）和 ↑（石色）靠窗口右边对齐。设置 → **速度图**：**曲线**（默认；平滑的 ↓ 曲线带柔和灯色填充，细的 ↑ 线，最新值上一个点）或 **柱形**（两行细圆角柱，越旧越淡）。简化界面没有卡片和火花；点卡片弹出 **详细信息**（loft 网络地址、前置、MTU、服务器）。下面，连着时有 **外部 IP**、**Overlay IP** 和 **路由**（网关）三行，和 macOS 弹出层一样（简化界面没有）。下拉菜单：**设置 · 连接 · 扫描二维码**（`#home-pulley`）。 |
| 连接 | 从主页下拉菜单进入，**‹** 退回主页。标题，副标题「每条有自己的密钥和地址。同时只激活一条。」行：紧凑连接/断开、名称、**主机**副标题，旁边类型（`loft · get` / `sstp` / `vless`），开着的那条另有 **当前**。点一下打开这条连接；只有「连接」按钮切换隧道。没有滑动操作。**长按** 打开上下文菜单：编辑 / 导出 `.loft`（仅选中） / 删除。添加在下拉菜单里：**扫描二维码 · 粘贴链接 · 从文件 · 手动**（`#list-pulley`）；它取代 iOS 的「+」和那一页。屏上没有手势提示。没有「多个」开关。 |
| 编辑 | Silica 对话框：顶上左边 **取消**、右边 **保存**（接受）；两者都回到打开它的地方。底部没有保存按钮。新连接标题是「新连接」，有胶囊 **VPN 类型** loft / SSTP / VLESS / Trojan，顶栏 **扫描**。已有的连接只显示自己的类型（不能换：字段不同），顶栏 **分享**（二维码），最下面红色 **删除**（remorse）。loft 和 VLESS：胶囊 **链接 / 手动**（图标：链环和铅笔）。链接显示服务器，只藏密钥（`loft://lt_••••@cdn…`）；显示 打开全部。loft 手动是访问密钥、主机、端口、方式（**自动**·GET·WS·H2·QUIC·H3）、路径。两边写同一份配置。VLESS 手动是 UUID、主机、端口、TLS·REALITY、TCP·XHTTP、TLS 主机名、指纹，以及该模式要的字段。证书校验。没有 SOCKS 字段，也没有接口开关：隧道总是 ConnMan 那一行。SSTP：用户、密码、服务器、证书校验、PAP/CHAP。Trojan：密码、`trojan://` 服务器、证书校验。折叠的 **更多选项**：mux（loft，「所有连接共用一条到节点的会话」）、DNS、DNS 写入系统；SSTP 和 Trojan 的 TLS 主机名在这里，SSTP 还有「只用这些地址」。 |
| 引导 | 开始：**扫描二维码** 和 **粘贴链接**（已复制的链接直接添加）；「手动输入密钥和地址」进入 macOS 那两步。**1 访问密钥**（`lt_…`）→ 继续。**2 地址**：URL；**更多选项** 里收着怎么连（先是 **自动**，再 GET/WS/H2/QUIC/H3）和路径（占位提示，不是预填值）→ 连接。然后是 ConnMan 系统同意（如果系统要问）；屏上写：「如果系统请求允许 VPN，点「允许」。只问一次。」 |
| 设置 | 从主页下拉菜单进入，**‹** 退回主页。一个列表，分组 **通用 / 路由 / 连接**。顶栏重置路由。链接 **默认 \| 日志 \| 关于**；「默认」走 remorse。通用：简化界面、速率单位（比特或字节；数字会自己换到 Mbit/s）、速度图、语言、详细日志。路由：默认路由、**无 VPN 时禁止联网**、分流和 **列表** 按钮（没有故障切换）。连接：**启动时连接**、**断线重连**、公网 IP。没有按需连接，也没有受信任的 Wi‑Fi。没有 Dock、登录项、多条出站、SOCKS。 |
| 日志 | 从设置进入：顶上 **‹**。前置和会话行（`tun connman-loft up`）。密钥会缩短。复制 / 分享 / 清除（remorse）。 |
| 关于 | 从设置进入：顶上 **‹**。标志、loft、`1.0.22`，协议 loft/1.0 · SSTP · VLESS · Trojan，简介，「Made with love by A³」。 |
| 扫描 | 压栈：顶上 **‹**。相机二维码。从文件 / 从相册。「或粘贴链接」输入框（`loft://…、sstp://…、vless://…`），按钮 **粘贴** 和 **添加**。 |
| 分享二维码 | 压栈：顶上 **‹**。二维码和 `loft://` 那行，提醒链接里有密钥，**分享** 和 **拷贝链接**。多条前置或多条连接用导出的 `.loft` 文件。 |
| 封面 | 桌面上的 Silica 封面：灯、**Loft**、名称 · overlay IP（`Home · 10.8.0.14`）、断开（关着时是连接）。 |
| 设置 → VPN | 系统 ConnMan 列表。**Loft** 已连接，类型 loft；OpenVPN 和 IPsec 仍是下面的系统行。开关是 ConnMan 的 Loft 行。 |

**Remorse。** 删除连接、还原默认、清除日志都不弹确认页。顶上一条横栏写出动作（「正在删除 Home」「还原」「清除」），下面是倒计时线和「点按取消」；倒计时走完，动作执行。

**简化界面**（设置 → 通用）：主页是标志、胶囊、连接 / 断开；下拉菜单还在。卡片和火花藏起来。列表仍有连接/断开和名字。设置还是全的。

模型导航里有平台分区 **Sailfish**：**封面** 和 **设置 → VPN**。

只有 macOS 才有、这个模型不画的外壳：菜单栏弹出层、SOCKS5 卡片和端口、Dock 图标、登录项、同时多条出站、拖放托盘。

### 分流

沿用 macOS 的模型（macOS DESIGN §5）：**区域** 经 loft 或绕过 loft，**服务**（名称 + CIDR）只是属于某个区域，**排除** 是不写入路由、拉取列表后仍保留的 CIDR。内置区域 Local（绕过，局域网）和 Global（经 loft，`global.bat`），自己的区域在编辑器里加。Sailfish 上「经 loft」是 ConnMan Loft 行上的路由，「绕过」留在系统默认路由上；隧道关着时本来就全部直连。没有 macOS 那种记住的直连下一跳。

入口：设置 → 路由 → 开关和金色的 **列表**。这些屏压进设置的栈，顶上是 **‹**。

| 屏 | 内容 |
|----|------|
| 列表 | 标题和同一个开关，一行说明（经 loft 进隧道，绕过直连）。**区域** + **区域** → 编辑器。区域胶囊带服务数。安静的一行 `Global · 经 loft`。搜索。行：名称、前几个 CIDR、网段数、来源（列表 / 自定义 / 排除）；点一下编辑。**+ 添加**。底部 **导入 · 导出 · 规则集**。 |
| 网络 | 条目 路由 / 排除，网段或地址（编辑时每行一个 CIDR），服务，区域。添加 / 保存。 |
| 区域 | 行：名称、网段数 · 列表文件、方向、「内置」。**+ 添加区域**。 |
| 区域编辑 | 名称（内置的锁住）、方向 经 loft / 绕过 loft、列表地址 + **列表**、自动更新、保存；自己的区域有 删除区域（服务 → Global）。新区域同样的字段。 |
| 规则集 | 当前区域的列表地址 + **列表**、自动更新、上次拉取、立即更新。 |
| 目录 | loft-routes 列表：全球、俄罗斯、中国、伊朗、哈萨克斯坦、白俄罗斯；当前的高亮。点一下填入地址并返回。 |
| 导入 | 从文件（文档），识别出的格式和行数，区域，添加。 |
| 导出 | 所有区域写进一个 loft-routes 文件（区域在 `REM zone:`，服务名在注释里），示例，保存（分享）。 |

---

## 3. Sailfish 外壳

不要改 设置 → VPN 的样子。模型只说明 **Loft** 在 *他们* 的 ConnMan 列表里长什么样：状态 已连接，类型 loft，开关是 ConnMan 的 Loft 行，不是我们应用里第二个开关。

我们的连接 写 YAML，请 ConnMan 激活 loft 连接。没有插件时，应用启动 `loft-front`，SOCKS 开在回环上。用户在 设置 → VPN 里关了 Loft，应用显示已关闭。

VPN 同意是 ConnMan 的系统提示，一次，如果系统要问。之后连接 不再问。

相机权限是第一次打开扫描时的系统页。文件：文档权限；选择器认图片、文本、YAML、`.loft`。相册走系统选择器。

URL scheme `loft`，点 `loft://…` 会提议导入。

不要恢复直连 对应 ConnMan 默认路由一直被占着。文案用 *路由* / *直连互联网*。

封面就是桌面上的预览：灯、名称 · overlay IP、一个 连接 / 断开 动作。

### 列表手势

macOS 列表行上有淡色编辑；Sailfish 用长按，因为没有指针。没有滑动操作；添加在下拉菜单里，不在按钮上。

| 手势 | 动作 |
|---------|--------|
| 点一下 | 打开这条连接（编辑）。连接靠紧凑的「连接」按钮；另一条开着的话先断开。 |
| 紧凑连接 / 断开 | 跟 macOS 列表按钮一样（`连接` / `断开`）。 |
| 长按 | 菜单：编辑；这条选中时导出 `.loft`；删除（remorse）。 |
| 往下拉 | 下拉菜单：扫描二维码 · 粘贴链接 · 从文件 · 手动。 |
| 选中行上的导出 | 页：这条连接存成 `Name.loft`；活前置的二维码 `loft://`；所有连接存成 `Family.loft`。页上再写一遍导出提醒。 |

---

## 4. 视觉

跟 iOS、Android、macOS 同一套 loft 记号：Ink `#1A1714`，Lamp `#E8B84A`，Paper `#E8DFD2`，Avenir Next。模型里的手机外壳是 loft（深色纸）。下拉菜单、页面栈、对话框顶栏、remorse 和封面是 Silica 的；不照搬 Material 或 iOS 导航。

字体：标题 Demi，正文 Regular，密钥 SF Mono。已连接 = 灯窗亮。已关闭 = 灯灭，石头色 L。

安全区：状态栏 + 下拉菜单。一列。拇指够得到：连接 / 断开是主页上的大控件。没有标签栏：主页是根，连接和设置从它的下拉菜单进入，其余都带左上角 **‹** 压栈。页面标题靠右，用高亮色。编辑是顶上有 取消 / 保存 的对话框；引导是全屏。

扫描：深色取景，灯色角括号，取景下面两个动作（从文件 / 从相册）。

VPN 类型是 **胶囊**（选中的类型用灯色填），连接/高级和设置页签跟 macOS 同一套分段。

模型里的系统界面（封面、设置 → VPN）保持系统外壳，让产品边界看得见。

---

## 5. 文案

共用的文案——按钮、设置、路由——来自 `clients/macOS/lang/*.lang`，macOS、iOS 和 Sailfish 上说法一样。Sailfish 只在 macOS 没有对应的地方用自己的文案：主页卡片和详细信息、引导开始屏、下拉菜单、remorse、封面和 ConnMan 同意那一行。模型默认俄语（`?lang=en` 或 RU/EN/中文 控件）。主页、引导和列表紧凑按钮用同一个词：**连接**；进行中是 **正在连接…**。密钥是 **访问密钥**，不是成员密钥。中文走 VPN/分流习惯说法（分流、断线重连、无 VPN 时禁止联网）。

| id | EN | RU |
|----|----|----|
| connect | Connect | Подключить |
| pop_connect | Connect | Подключить |
| disconnect | Disconnect | Отключить |
| connecting | Connecting… | Подключение… |
| pill_on | Connected | Подключено |
| pill_off | Not connected · Home › | Не подключено · Home › |
| journal | Log | Журнал |
| reset | Reset routes | Сбросить маршруты |
| settings | Settings | Настройки |
| conns | Connections | Подключения |
| conns_sub | Each has its own key and address. Only one is active at a time. | У каждого свой ключ и адрес. Одновременно активно одно. |
| active | active | активно |
| edit | Edit | Править |
| delete | Delete | Удалить |
| del_remorse | Deleting %s | Удаление %s |
| remorse_cancel | Tap to cancel | Нажмите, чтобы отменить |
| import / export | Import / Export | Импорт / Экспорт |
| warn_export | The file contains the access key. Do not share it. | В файле есть ключ доступа. Никому не передавайте. |
| vpn_type | VPN type | Тип VPN |
| loft_uri | loft:// address | Адрес loft:// |
| edit_main / edit_adv | Main / Advanced | Основные / Дополнительно |
| key_title | Access key | Ключ доступа |
| url_title | Address | Адрес |
| url_go | Connect | Подключить |
| simple | Simplified interface | Упрощённый интерфейс |
| hold | No Internet without VPN | Без VPN — без интернета |
| auto | Connect at Launch | Подключаться при запуске |
| reconnect | Reconnect on Disconnect | Переподключаться при обрыве |
| split | Split Routing | Раздельная маршрутизация |
| default_route | Default Route | Маршрут по умолчанию |
| from_gallery | From gallery | Из галереи |
| onb_vpn | If the system asks to allow the VPN, tap Allow. Only once. | Если система спросит разрешение на VPN — нажмите «Разрешить». Это один раз. |
| blurb | On the outside, ordinary HTTPS. Inside, a network of its own. | Снаружи — обычный HTTPS. Внутри — своя сеть. |

状态卡片随协议变化（见主页）；速度在火花条形旁边，用选定的单位。loft 网络地址和 MTU 在点开的详细信息里。

---

## 6. 对照 macOS GUI

| macOS | Sailfish 模型 |
|-------|---------------|
| 状态窗 | 主页（根页面） |
| 点胶囊 → 连接 | 一样 |
| OVERLAY / FRONT / SOCKS5 | 协议 / 前置 / 选项（按协议）；Overlay IP 和路由在下面；MTU 在详细信息里 |
| 连接 + 「多个」开关 | 连接，只能互斥 |
| 添加（`+`） | 连接页的下拉菜单（Aurora 5：AppBar 里的 `+`） |
| 拖入 `client.yaml` / `.loft` / 二维码 | 导入 / 扫描 / 文档 / 相册 |
| 编辑 SOCKS5 + 隧道 | 只有隧道（ConnMan 那一行） |
| 确认对话框 | remorse |
| 设置里的登录项、Dock、SOCKS、多个 | 没有 |
| 设置里的故障切换 | 没有（隧道就一条） |
| 关于 / 日志 | 同一套文案和布局，手机一列 |

---

## 7. ОС Аврора 5

模型：[`aurora.html`](aurora.html)。和 Sailfish 同一棵包树（`aurora/`、`app.loft.aurora`）。按 [Aurora OS 5.0 发布说明](https://developer.auroraos.ru/release_notes/os_5.0.0.60)，系统应用从下拉菜单和操作菜单改成了 **AppBar** 加 **PopupMenu**。模型照这个画；其余跟 Sailfish 模型一样。

| 屏幕 | Aurora 5 |
|--------|----------|
| 主页 | 顶上 AppBar：图标按钮 **连接** 和 **设置**，还有 **⋮** → PopupMenu，里面是 **扫描二维码**（`aurora.html#home-menu`）。没有下拉菜单，也没有亮线。 |
| 连接 | AppBar：**‹**、标题「连接」、**+** → PopupMenu：**扫描二维码 · 粘贴链接 · 从文件 · 手动**（`#list-menu`）。 |
| 长按 | 上下文菜单（编辑 / 导出 `.loft` / 删除）以 PopupMenu 浮在内容上。 |
| 引导 | 同意提示写成「如果 Aurora 请求允许 VPN，点「允许」。只问一次。」 |
| 其余 | 跟 Sailfish 模型一样：编辑对话框顶栏（取消 / 保存）、remorse、页面标题、封面、设置 → VPN。模型导航分区叫 **Аврора**。 |

Aurora 5 还有给平板的 **SplitView** 和重画的封面；模型里没画。
