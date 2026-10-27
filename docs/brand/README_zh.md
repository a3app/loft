# loft · 品牌

[English](README.md) · [Русский](README_ru.md) · **中文**

编译和跑起来：[docs/guide_zh.md](../guide_zh.md)。协议：[docs/protocol_zh.md](../protocol_zh.md)。

标志是带灯的 L 形屋顶：字母 L，覆盖网打开时窗口亮着。

实心填色只用在标志和托盘上。窗口上那点微光只用在应用图标。

## 色板

| Token | Hex | 用法 |
|-------|-----|-----|
| Ink | `#1A1714` | 应用图标底，浅底上的标志 |
| Stone | `#B7A99A` | 深色应用图标上的 L 身 |
| Copper | `#C67B3A` | Windows 安装程序盒子 |
| Lamp | `#E8B84A` | 窗口，已打开 |
| Lamp hi | `#F3D27A` | 应用图标上的窗口高光 |
| Snow | `#F3EDE4` | 深底上的标志，深底上的托盘 |
| Paper | `#E8DFD2` | 字段、预览 |
| Mute | `#8A7E72` | 说明文字 |

## 字体

**Avenir Next** (Linotype, Adrian Frutiger / Akira Kobayashi, 2004)。几何人文无衬线：圆的 “o” 跟灯的窗口对得上。

| 角色 | 字重 | 规则 |
|------|-----|------|
| Wordmark / lockup | Demi 600 | 小写 `loft`，字距 −0.04em |
| 标语 | Regular 400 | *Loft Overlay For TLS*，title case，字距 −0.02em |
| UI 标题 | Demi 600 | sentence case，字距 −0.02em |
| 按钮、标签 | Medium 500 | 不用全大写，短状态除外 |
| 正文、文档 | Regular 400 | 行高 1.45 |
| 代码、密钥 `lt_…` | SF Mono / Menlo / Consolas | 从不用 Avenir |

Lockup 里不要用 Ultra Light、Heavy 或斜体。

栈：`"Avenir Next", Avenir, "Helvetica Neue", "Segoe UI", ui-sans-serif, system-ui, sans-serif`。

macOS 上这是系统字体，带西里尔文。Windows 上要像素级一样的 wordmark，得用 SVG 轮廓或装上字体文件。

## 文件

| 文件 | 用途 |
|------|-----|
| `svg/loft-mark.svg` | 协议、站点 |
| `svg/loft-lockup.svg` | 标志 + 单词 *loft* |
| `svg/loft-lockup-readme.svg` / `png/loft-lockup-readme.png` | README：与 social preview 相同的 lockup（裁切）+ *Loft Overlay For TLS* |
| `png/type-specimen.png` | 字体表 |
| `svg/loft-og.svg` / `png/github-social-preview.png` | GitHub social preview 1280×640. Settings → General → Social preview |
| `png/a3-github-avatar.png` | GitHub 头像 a3app（A³，2048×2048）。Settings → Profile picture |
| `svg/a3-mark.svg` | A³ 出品标志，Avenir Next 未转曲 |
| `svg/a3-heart.svg` | 像素心 7×6，loft 灯 |
| `svg/a3-colophon.svg` | `Made with love` + 心 + `by A³` |
| `png/a3-colophon.png` | Colophon 概念（About 那一行） |
| `svg/loft-icon.svg` | 应用图标（深色） |
| `svg/loft-icon-light.svg` | 浅底上的图标 |
| `svg/loft-favicon.svg` | 32px |
| `svg/loft-menubar-template.svg` | macOS `NSStatusItem` 模板 |
| `app/loft.icns` | macOS bundle |
| `windows/loft.ico` | Windows 应用：`loft.exe`、安装程序、快捷方式。16–256 px，16 px 来自像素网格 |
| `windows/loft-setup.ico` | 安装程序 `loft-windows-*-setup.exe`：铜色盒子，正面是点亮的标志 |
| `windows/loft-svc.ico` | 服务 `loft-svc.exe`：应用图标 + 墨色圆标上的灯光色齿轮 |
| `windows/loft-front.ico` | `loft-front.exe`：应用图标 + 墨色圆标上的灯光色 ⇄ 箭头 |
| `windows/loft-file.ico` | 资源管理器中的 `.loft` 文件：纸张加点亮的标志 |
| `windows/loft-tray{,-off,-err}.ico` | Windows 托盘资源：已连接、已断开、错误（窗口 `#E24B4B`） |
| `windows/loft-tray-light{,-off,-err}.ico` | 同上，墨色主体，用于浅色任务栏 |
| `windows/store/` | MSIX 资源：`Square44x44Logo`（targetsize + unplated）、`Square71x71`、`Square150x150`、`Wide310x150`、`Square310x310`、`StoreLogo`、`SplashScreen`，缩放 100–400 |
| `tray/loft-tray-16.png` | Windows 通知，已打开 |
| `tray/loft-tray-off-16.png` | Windows 通知，已断开 |
| `tray/loft-tray{,-light}{,-off,-err}-{16,20,24,32}.png` | 全部托盘状态，100 / 125 / 150 / 200 %，由 `tray.c` 读取 |
| `menubar/loftTemplate.png`（`@2x` `@3x`） | macOS 菜单栏，已打开 |
| `menubar/loftTemplate-off-*.png` | macOS 菜单栏，已断开 |

## 规则

1. 屋顶只盖住 L 的竖笔；脚往右伸。这是 L。
2. ≤16px 用 `generate.py` 里的 `PIX16` 网格。不要从 1024 缩小。
3. macOS 菜单栏是 **template image**（黑 + alpha）。系统自己上色。
4. Windows 托盘：深色任务栏（及 Windows 7）用 **snow + lamp**，浅色任务栏用 **ink + lamp**（`loft-tray-light*`）。
5. 已打开：灯亮。已断开：同一个 L，窗口灭（macOS：不挖空）。
6. 单词 *loft* 小写，Avenir Next Demi，字距 −0.04em。
7. 标语是递归缩写 *Loft Overlay For TLS*（LOFT）。Title case。wordmark 仍为小写 `loft`。
8. Colophon 只用英文：`Made with love` + 像素心 + `by A³`。心是 7×6 灯格。A³ 是出品标志，不是协议标志。

重新生成：`/tmp/loft-brand/bin/python generate.py`。仅 Windows 套件，无需 Chrome：`generate.py windows`
