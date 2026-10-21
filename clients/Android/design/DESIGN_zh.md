# loft · Android 客户端

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

手机应用加一个 `VpnService`。标志、色板、Avenir Next：[`brand/`](../../../docs/brand/)。交互模型：[`index.html`](index.html)。协议：[docs/protocol_zh.md](../../../docs/protocol_zh.md)。导入 URI：[docs/share_zh.md](../../../docs/share_zh.md)。

Java 容器在 [`../app/`](../app/)。活会话是 `LoftVpnService` 里的 Go，不是 `loft-front`。薄宿主已经能粘贴 YAML；这个模型是要换掉粘贴屏的 GUI。

---

## 1. 产品

入站是系统 VPN（`VpnService`）。一次一条配置：loft（`lt_…`，没有密码）、SSTP（用户 + 密码）、VLESS（`vless://`）或 Trojan（`trojan://`）。

v1：磁盘上的命名连接，导入/导出 `.loft`，打开/断开，不要恢复直连，按协议列表分流。loft YAML 里 `select: auto`。一次一条隧道。

`.loft` 文件里是 **一条或多条连接**。类型可以混：`loft`、`sstp`、`vless`、`trojan`。列表副标题是 **类型 · 传输 · 主机**（`loft · get · cdn.example.com`）。loft 地址是字符串 `loft://`。规格：[docs/share_zh.md](../../../docs/share_zh.md)。

分享：扫二维码，或载入带 `loft://`、`sstp://`、`vless://`、`trojan://` 或 `.loft` 的文件/图片。

Android 设置里显示的就是应用拉起的那条 VPN。应用不替代设置。

适配器语言是 **Java 17**。gomobile 已经吐 Java；这个进程不装 Kotlin runtime。模型是 HTML；后面的 GUI 是 Views（不用 Compose）。

Android TV / Google TV 是同一个 `VpnService` 和 AAR，加 Leanback flavor。不是这个模型；[计划](../PLAN_zh.md) 第 4 阶段。VIDAA / Tizen / webOS 不做。

## 2. 屏幕

| 屏幕 | 内容 |
|--------|------|
| 引导 | 第一次打开：粘贴 `lt_…` + URL，扫二维码，或选 `.loft` / 图片。然后系统那张 VPN 连接请求。 |
| 主页 | 灯、连接名、打开/断开。Overlay IP、前置、字节。 |
| 正在打开 | 灯暗，文案「正在连接…」。取消回到主页，已关闭。 |
| 连接 | 配置列表。副标题：类型 · 传输 · 主机。点一下选中（并会重连）。**向右滑** 编辑，**向左滑** 删除（`ItemTouchHelper`）。**长按** 菜单：编辑 / 导出 `.loft`（仅选中） / 删除。选中行（灯）显示导出。添加 → 扫描或文件。 |
| 扫描 | 相机二维码。从文件（SAF）。从相册（Photo Picker）。粘贴 `loft://` / `sstp://` / `vless://` / `trojan://`。确认：名称、类型、传输、主机；密钥藏着。 |
| 编辑 | 名称、类型（`loft` / `sstp` / `vless` / `trojan`）。字段跟类型走。loft 和 VLESS：胶囊 **链接 / 手动**。loft 链接是 `loft://` 字符串；手动是访问密钥、主机、端口、前置 get / ws / h2 / quic / h3、路径。两边写同一份配置。VLESS 链接是 `vless://` 字符串；手动是 UUID、主机、端口、TLS·REALITY、TCP·XHTTP、TLS 主机名、指纹，以及该模式要的字段。证书校验。两边都写成 `vless://`。SSTP：用户、密码、服务器、TLS 主机名、证书校验、PAP/CHAP。Trojan：密码、`trojan://` 服务器、TLS 主机名、证书校验。把选中出站分享成二维码。导出这条连接的 `.loft`。 |
| 设置 | 不要恢复直连、分流、关于（软件 + 协议 1.0）。导入 `.loft`（一条或多条连接）。全部导出成 `Family.loft`。 |
| 日志 | 服务最近几行。没有密钥材料。藏在「高级」后面。 |
| VLESS / SSTP | 跟扫描同一个扫描器。文案：「同一条隧道。」 |

## 3. Android 外壳

不要改 设置 → 网络和互联网 → VPN 的样子。模型只说明 **loft** 在 *他们* 的 VPN 列表里长什么样：状态 Connected，类型 VPN，开关是系统隧道，不是我们应用里第二个开关。

我们的打开先调一次 `VpnService.prepare`，然后 `startForegroundService(LoftVpnService)`。用户在设置里关了 loft，应用显示已关闭。

VPN 权限是系统那张 **Connection request** 页，一次。之后打开不再问。

相机权限是第一次打开扫描时的系统页。相册走系统 Photo Picker（不要宽泛的媒体权限）。SAF 认图片、文本、YAML、`.loft`。

Intent filter `loft`，点 `loft://…` 会提议导入。`.loft` 文件走同一个识别器。

### 列表手势

跟 iOS 同一套。Views 的 `ItemTouchHelper` 加长按 `PopupMenu`。不要只靠行上那支铅笔。

| 手势 | 动作 |
|---------|--------|
| 点一下 | 选中。如果另一条已经开着，先断开再打开这条。 |
| 向左滑 | 删除，带确认对话框。 |
| 向右滑 | 编辑。 |
| 长按 | 菜单：编辑；这条选中时导出 `.loft`；删除。 |
| 选中行上的导出 | 页：这条连接存成 `Name.loft`；活前置的二维码 `loft://`；所有连接存成 `Family.loft`。 |

已打开的会话是 **foreground service**（类型 `connectedDevice`）。通知栏显示名称、Overlay IP、断开。API 33+ 还要一次通知权限页。

不要恢复直连，落到系统开关 **Always-on VPN** 和 **Block connections without VPN**。应用可以深链进那页设置；开关用户自己拨。文案用 *路由* / *直连网络*，不写「kill switch」。

快捷设置磁贴：点一下打开/断开当前配置。灯亮 = 已打开。

## 4. 视觉

跟 iOS 和 macOS 同一套 loft 记号：Ink `#1A1714`，Lamp `#E8B84A`，Paper `#E8DFD2`，Avenir Next。模型里的手机外壳是 loft（深色纸），不是 Material You 紫。

字体：标题 Demi，正文 Regular，密钥 SF Mono / Roboto Mono。已连接 = 灯窗亮。已关闭 = 灯灭，石头色 L。

安全区：状态栏 + 手势条。一列。拇指够得到：打开是主页上的大控件。

扫描：深色取景，灯色角括号，取景下面两个动作（从文件 / 从相册）。

模型里的系统面（设置、连接请求、通知栏、磁贴）保留 Android 原外壳，好看出产品边界。

## 5. 文案

| EN | RU |
|----|----|
| loft | loft |
| Connect | Подключить |
| Disconnect | Отключить |
| Connecting… | Подключаем… |
| Connection | Подключение |
| Member key | Ключ участника |
| All traffic | Весь трафик |
| Hold the route | Удерживать маршрут |
| Direct Internet | Прямой Интернет |
| Reset routes | Сбросить маршруты |
| Scan QR | Сканировать QR |
| From file | Из файла |
| From gallery | Из галереи |
| Share QR | QR of the import URI |
| Edit | Править |
| Delete | Удалить |
| Export | Экспорт |
| VLESS | VLESS |
| VPN is in Android Settings | VPN — в Настройках Android |
| Connection request | Запрос подключения |
| Always-on VPN | VPN всегда включён |
| Block connections without VPN | Блокировать без VPN |
