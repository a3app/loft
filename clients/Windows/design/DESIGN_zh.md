# Loft · Windows 客户端

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

通知区域（托盘）代理；窗口是次要的。标志、配色和字体：[`brand/`](../../../docs/brand/)。交互式样稿：[`index.html`](index.html)。协议：[docs/protocol_zh.md](../../../docs/protocol_zh.md)。

本客户端沿用 macOS v1：[`../../macOS/design/DESIGN_zh.md`](../../macOS/design/DESIGN_zh.md)。产品（§1）、协议支持（§2）、配置（§3）、路由与 TUN（§4）、分流（§5）、首次运行（§8）、主窗口（§9）和文案（§10）相同。本文只写 Windows 上的不同之处。

---

## 1. 平台

最低版本为 **Windows 7**。界面由应用自行绘制，因此在 7、8.1、10 和 11 上外观一致。不使用 Mica、Acrylic、Aero Glass、系统主题和系统强调色。窗口底色为 Ink `#1A1714`，强调色为灯光色。

字体：品牌字体栈 `"Avenir Next", Avenir, "Helvetica Neue", "Segoe UI", sans-serif`。Windows 没有 Avenir Next，若不内置字体文件，界面回退到 Segoe UI（Windows Vista 起自带）。“关于”窗口中的 `loft` 字样使用品牌 SVG 轮廓，而非文字。等宽字体为 Consolas。

---

## 2. 托盘

图标为 [`docs/brand/svg/loft-tray.svg`](../../../docs/brand/svg/loft-tray.svg)：铜色主体，窗口为灯光色（品牌规则 4）。位图位于 [`docs/brand/tray/`](../../../docs/brand/tray/)：16 / 20 / 24 / 32 px，对应 100 / 125 / 150 / 200 % 缩放。不要从 1024 缩小（规则 2）。

| 状态 | 主体 | 窗口 |
|------|------|------|
| disconnected | 铜色 | 铜色（`loft-tray-off`） |
| connecting | 铜色 | 灯光色，闪烁 |
| connected | 铜色 | 灯光色 `#E8B84A`（`loft-tray`） |
| error / route closed | 铜色 | 红色 `#E24B4B` |

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

---

## 7. 导入、导出、更新

- 导入和导出使用 Windows 标准的“打开” / “另存为”对话框。样稿中的拖放区表示可以把文件拖到窗口上。
- 更新窗口为“下载并运行安装程序”，而非替换 DMG。检查方式同 macOS：GitHub latest。

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

字符串来自 macOS 共用语言包 [`../../macOS/lang/`](../../macOS/lang/)，其上叠加 [`lang/win.lang`](lang/win.lang)：仅包含在 Windows 上措辞不同的键（`login`、`login_d`、`dock`、`dock_d`、`diag_d`、`upd.offer.body`、`win.min`、`win.close`、托盘场景名）。查找顺序：该语言的 Windows 包 → 英文 Windows 包 → macOS 包。已翻译 en / ru / zh；其他语言中这些键暂显示英文。

与 macOS 相同，文案使用 *route*、*direct Internet*、*reset*；不用 “kill switch” 和 “shield”。

---

## 10. 待定问题

以下影响实现而非外观，需在编码前决定。

1. **Go 与 Windows 7。** 会话由 Go 进程 `loft-front` 运行。Go 1.21 及以上不支持 Windows 7 / 8：需用 Go 1.20.x 或打补丁的工具链构建。
2. **TUN 驱动。** 必须能在 Windows 7 上运行：TAP-Windows6 或合适版本的 Wintun。选择前先确认 Windows 7 支持。
3. **特权部分。** 路由、hold 和重置由 Windows 服务设置（对应 `loft-priv`）。服务名和安装方式（MSI / 自有安装程序）未定。
4. **界面工具包。** WebView2 109 之后的版本不支持 Windows 7。可选：自绘原生 Win32、.NET Framework 4.8 上的 WPF、Go GUI。需要选择。
5. **密钥存储。** Windows 凭据管理器，或文件中的 DPAPI（`CryptProtectData`）。
6. **字体。** 内置 Avenir Next（需嵌入授权）或使用 Segoe UI。
7. **架构。** 仅 x64，还是也支持 x86（32 位 Windows 7 很常见）。
8. **关联。** 是否注册 `.loft` 和 `loft://` 协议，以便双击和点击链接打开。

---

## 11. v1 之外

同 macOS §12。另外：Windows 通知（toast）不在 v1 内。
