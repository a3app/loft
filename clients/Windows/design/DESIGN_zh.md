# Loft · Windows 客户端

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

通知区域（托盘）代理；窗口是次要的。标志、配色和字体：[`brand/`](../../../docs/brand/)。交互式样稿：[`index.html`](index.html)。协议：[docs/protocol_zh.md](../../../docs/protocol_zh.md)。

本客户端沿用 macOS v1：[`../../macOS/design/DESIGN_zh.md`](../../macOS/design/DESIGN_zh.md)。产品（§1）、协议支持（§2）、配置（§3）、路由与 TUN（§4）、分流（§5）、首次运行（§8）、主窗口（§9）和文案（§10）相同。本文只写 Windows 上的不同之处。

---

## 1. 平台

最低版本为 **Windows 7**。界面由应用自行绘制，因此在 7、8.1、10 和 11 上外观一致。不使用 Mica、Acrylic、Aero Glass、系统主题和系统强调色。窗口底色为 Ink `#1A1714`，强调色为灯光色。

界面字体为 **Segoe UI**（Windows Vista 起自带），不内置 Avenir Next。字重沿用品牌：标题 Semibold 600，按钮和标签 500，正文 Regular 400。“关于”窗口中的 `loft` 用灯色 Segoe UI 绘制：品牌 SVG 目前仍是 `<text>`，v1 不描轮廓。等宽字体为 Consolas。

构建：**x86 和 x64**。界面为**原生 Win32** 自绘（不用 WebView，不用系统视觉样式）。

---

## 2. 托盘

图标为 [`docs/brand/svg/loft-tray.svg`](../../../docs/brand/svg/loft-tray.svg)：主体为 snow `#F3EDE4`，窗口为灯光色（品牌规则 4）；浅色任务栏上主体为墨色（`loft-tray-light*`）。位图位于 [`docs/brand/tray/`](../../../docs/brand/tray/)：16 / 20 / 24 / 32 px，对应 100 / 125 / 150 / 200 % 缩放。不要从 1024 缩小（规则 2）。

| 状态 | 主体 | 窗口 |
|------|------|------|
| disconnected | snow | snow（`loft-tray-off`） |
| connecting | snow | 灯光色，闪烁 |
| connected | snow | 灯光色 `#E8B84A`（`loft-tray`） |
| error / route closed | snow | 红色 `#E24B4B`（`loft-tray-err`） |

多连接模式沿用 macOS §7 的规则：至少一个会话在线为黄色；至少一个出错为红色。

鼠标：

| 操作 | 打开 |
|------|------|
| 左键单击 | 托盘面板（§3） |
| 右键单击 | 托盘菜单（§4） |
| 双击 | 打开 Loft（状态窗口） |

悬停提示：`Loft — 已连接：办公室` / `Loft — 没有活动连接`。

---

## 3. 托盘面板

即 macOS §7.1 的弹窗，内容与行为相同：标题、连接 / 断开 / 重试、hold 生效时的重置路由、连接 / 外部 IP / Overlay / 路由 / SOCKS5 各行、多连接模式下的折叠列表、底部的打开 Loft | 设置 | 退出。

不同之处：

- 面板贴在任务栏一侧的屏幕边缘，靠近通知区域。任务栏在顶部或侧边时，面板在该边打开。
- 没有箭头。
- 纯色底 `#201c19`，无模糊。
- 失去焦点或按 Esc 时关闭。
- “连接 ›”在面板左侧打开配置列表（macOS 为 `NSMenu`）。

尺寸为 300 × hug，与 macOS 相同。

---

## 4. 托盘菜单

Windows 没有应用菜单栏。macOS 的 **Loft**、**连接** 和 **工具** 菜单合并到图标的右键菜单：

```
断开 · 办公室                 （未连接时为“连接”）
连接                     ›    配置：当前项打勾，右侧显示状态
───
打开 Loft
设置
✓ 分流                        与设置中的开关相同
路由
日志
───
语言 (Language)          ›    本地语言名称，当前项打勾
关于 Loft
───
退出 Loft
```

图标与 macOS 菜单相同（窗口、齿轮、分支、文档、地球）。macOS 的 **编辑** 菜单未移入：编辑从连接列表打开。

---

## 5. 窗口

边框和标题栏由应用绘制。标题栏高 32 px：16 px 托盘标志、`Loft`，右侧为 **最小化** 和 **关闭** 按钮，各 46 px。没有最大化：窗口尺寸固定（macOS §11）。关闭按钮悬停时为红色 `#C42B1C`。圆角 8 px，阴影由应用绘制。

关闭窗口不会退出 Loft：代理留在托盘。只能从托盘菜单或托盘面板（**退出**）退出。

窗口尺寸同 macOS §11，另加 32 px 标题栏。

---

## 6. 设置

标签页和条目与 macOS 相同。“通用”标签页中有两项不同：

| macOS | Windows | 说明 |
|-------|---------|------|
| 登录时打开（Login item） | **随 Windows 启动** | 在托盘中，不打开窗口 |
| 显示 Dock 图标：始终 / 窗口打开时 / 从不 | **任务栏按钮**：始终 / 窗口打开时 / 从不 | 托盘图标始终保留 |

“扩展日志”记录 loft 服务的日志行，而非 `loft-priv`。

“通用”标签页末尾有两项 macOS 没有的条目：

| 条目 | 说明 | 默认 |
|------|------|------|
| **打开 .loft 文件** | 双击即用 Loft 打开 | 开 |
| **打开 loft:// 链接** | 浏览器中的链接用 Loft 打开 | 开 |

文件或链接会打开导入（同连接列表中的“导入”）。关闭即取消注册，打开即重新注册。“恢复默认”会同时打开两项。

安装程序：安装步骤中有同样的选项，复选框“用 Loft 打开 .loft 和 loft://”，**默认勾选**。未勾选 = 设置中两项均为关闭。

---

## 7. 导入、导出、更新

- 导入和导出使用 Windows 标准的“打开” / “另存为”对话框。样稿中的拖放区表示可以把文件拖到窗口上。
- 更新窗口为“下载并替换这个副本”。检查方式同 macOS：GitHub latest。下载 `loft-<版本>-windows-x64.zip` 或 x86 zip，替换正在运行的 exe 所在目录，然后重新启动。Program Files 里的副本会请求管理员。服务文件在磁盘上被替换；正在运行的服务保持旧映像，直到重启。
- `v*` 标签上的 Dist 发布两份 portable zip 和 `loft-windows-<版本>-SHA256SUMS.txt`。`make release` 另外生成安装包和 Store 布局。
  - Portable：`loft-<版本>-windows-x64.zip` 和 `loft-<版本>-windows-x86.zip`。解压即用。Windows 7 SP1 及更高版本。第一次打开隧道时安装服务。
  - 安装程序：`loft-windows-x64-setup.exe` 和 `loft-windows-x86-setup.exe`。复制到 Program Files，注册服务，添加开始菜单快捷方式和卸载项。没有 MSI。系统下限相同。复选框“用 Loft 打开 .loft 和 loft://”默认勾选；取消则在 exe 旁写入 `assoc.off`，首次启动关闭这两项关联。
  - GitHub 渠道是 portable zip。应用读取 `a3app/loft` 的最新发布。安装包不是更新载荷。
  - Microsoft Store 是单独的 x64 包，Windows 10 版本 1809 及更高。没有 Wintun，也没有 `loft-svc`：商店包不能安装内核驱动和 LocalSystem 服务。SOCKS 可用。更新来自 Store。`make store` 写出带 `AppxManifest.xml` 的 `dist/clients/Windows/store/x64`。`makeappx` 和 Partner Center 签名在 Windows 上完成。`Publisher` 的 `CN=a3app` 由商店证书替换。

---

## 8. 存储

| 内容 | 位置 |
|------|------|
| 配置 | `%APPDATA%\loft\profiles\<id>.yaml` |
| 当前配置指针 | `%APPDATA%\loft\active` |
| 会话 | `%APPDATA%\loft\sessions\<id>` |
| 设置 | `%APPDATA%\loft\prefs.yaml` |
| 分流列表 | `%APPDATA%\loft\routes.txt`（格式见 macOS §5.5） |
| 密钥 | Windows 受保护存储（见 §10）。文件和界面常驻字段中不出现明文密钥。 |

---

## 9. 文案

字符串来自 macOS 共用语言包 [`../../macOS/lang/`](../../macOS/lang/)，其上叠加 [`lang/win.lang`](lang/win.lang)：仅包含在 Windows 上措辞不同的键（`login`、`login_d`、`dock`、`dock_d`、`diag_d`、`upd.offer.body`、`win.min`、`win.close`、`assoc_file`、`assoc_link` 及其说明、托盘场景名）。查找顺序：该语言的 Windows 包 → 英文 Windows 包 → macOS 包。已翻译 en / ru / zh；其他语言中这些键暂显示英文。

与 macOS 相同，文案使用 *route*、*direct Internet*、*reset*；不用 “kill switch” 和 “shield”。

---

## 10. 已定

已在 v1 目录 [`../`](../) 落地。编译和运行见 [`../README_zh.md`](../README_zh.md)。

1. **界面。** C11、Win32、GDI。不用 WebView，不用系统视觉样式。`_WIN32_WINNT=0x0601`。用 mingw-w64 出 x86 和 x64（`-mwindows -static -municode`）。字体为 Segoe UI 和 Consolas。Windows 7 只带 Regular 和 Bold；界面请求 Medium 和 Semibold，并接受 GDI 的映射。DPI 用 `GetDeviceCaps(LOGPIXELSX)` 加 `SetProcessDPIAware`。清单列出 Windows 7、8、8.1 和 10，`dpiAware` 为 true，`asInvoker`。没有 Vista 的 id。“关于”显示 [`../VERSION`](../VERSION) 里的 `LOFT_VERSION`（1.0.0），不是 macOS 的版本字符串。
2. **会话。** `loft-front.exe` 与 `loft.exe` 放在一起。模块是 Go 1.26。官方 Go 1.21+ 不能以 Windows 7 为目标，因此 front 用打补丁的 XTLS/go-win7 构建（`make front GO_WIN7=...`）。外壳不链接 Go。未设置 `GO_WIN7` 时 `make` 跳过该二进制。go1.27.3 经该工具链已在 [`wintun-check`](../wintun-check/) 的 Windows 7 SP1 x64 上跑过。
3. **TUN。** 只用 Wintun 0.14.1。不用 songgao/water，也不用 `netsh`。服务创建名为 `Loft`、类型为 `Loft` 的适配器，用 `CreateUnicastIpAddressEntry` 设地址，用 `SetIpInterfaceEntry` 设 MTU 1280，用 `CreateIpForwardEntry2` 设路由。数据包走 `\\.\pipe\loft-tun`，字节模式：u32le 长度加 IP 包，最大 65535。用户态栈由 `loft-front` 运行。`WintunDeleteDriver` 只在 `--uninstall-driver` 时调用。适配器 GUID 与 `wintun-check` 的 16 字节相同（`6c 6f 66 74 2d 77 69 6e 74 75 6e 2d 63 68 6b 01`）；Windows 显示为 `{74666F6C-772D-6E69-7475-6E2D63686B01}`。会话容量 `0x400000`。外壳把覆盖地址安装为 /32。默认路由关闭时，再发送覆盖网段 /24。
4. **服务。** 名称 `loft`，显示名 `Loft`，独立进程，自动启动，依赖 Tcpip。二进制 `loft-svc.exe`。控制管道 `\\.\pipe\loft-ctl`（消息模式）。没有 MSI。需要时 `--install` 提权。SOCKS 不需要服务；第一次打开隧道时提议安装。管道 SDDL：`D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;IU)`。hold 打开时 `tun-close` 回复 `ok held`，不关适配器。`reset` 总是恢复保存的 IPv4 默认路由和 DNS。替换之前先快照物理 IPv4 默认路由。DNS 是适配器接口键上的 `NameServer`，然后调用 `DnsFlushResolverCache` 和 `DhcpNotifyConfigChange`。日志 `%ProgramData%\loft\svc.log`，上限 1 MB，崩溃后不自动重启。
5. **密钥。** 当前用户的 DPAPI `CryptProtectData`，文件 `%APPDATA%\loft\keys\<id>.bin`。配置 YAML 不含 token、uuid 和密码。导出 `.loft` 会带上密钥并先警告。`loft-front` 仍在命令行收到 `-token`，与 macOS 相同。
6. **存储和首选项。** 配置、当前 id、实例文件、首选项、路由和日志都在 `%APPDATA%\loft`，如 §8。默认值与 macOS 客户端相同（语言 ru、hold 关、failover 开、split 关、默认路由开、共享 SOCKS 1080、检查更新开）。Windows 另加当前用户对 `.loft` 和 `loft://` 的关联（默认开），以及登录启动（`HKCU\...\Run`，值 `Loft`，命令 `"exe" --tray`）。更新检查读取 GitHub `a3app/loft` 的最新发布；标签更新且发布里有 portable zip 时，用 `loft-<版本>-windows-x64.zip` 或 x86 zip 替换本副本。Dist 用仓库 `VERSION` 写入 `LOFT_VERSION`，以便和标签一致。Store 版本不查询 GitHub。
7. **仍保留的限制。** 不删除 IPv6 默认路由。x86 的 Wintun 未做实机验证。服务日志超过上限后不再滚动。本树的界面已链接，未在 Windows 上运行。

---

## 11. v1 之外

同 macOS §12。另外：Windows 通知（toast）不在 v1 内。
