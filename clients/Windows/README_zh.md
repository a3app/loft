# Loft · Windows

[English](README.md) · [Русский](README_ru.md) · **中文**

Windows 7 SP1 及更新版本的托盘客户端。窗口是次要的。产品、配置、路由和文案与 macOS 客户端一致。本目录是 Windows 部分：Win32 外壳、持有 Wintun 的服务，以及已有的 `loft-front` 会话。

面向运营方的 overlay，用来维持稳定的管理通道。不是绕过封锁的产品。

## 组成

| 文件 | 运行身份 | 作用 |
|------|----------|------|
| `loft.exe` | 用户 | 托盘、窗口、DPAPI 密钥、启动 `loft-front` |
| `loft-svc.exe` | LocalSystem | Wintun 适配器 `Loft`、地址、路由、数据包管道 |
| `loft-front.exe` | 用户 | 会话。独立的 Go 程序，不链进外壳 |
| `wintun.dll` | 由服务加载 | Wintun 0.14.1，与 `loft-svc.exe` 同目录。不进 git |

没有服务时 SOCKS 仍可用。第一次打开隧道时会提议安装服务。没有 MSI。服务清单是 `asInvoker`，这样 SCM 可以启动它。调用者不是管理员时，`--install` 通过 `runas` 提权。

## 编译

在 `clients/Windows` 下，需要 mingw-w64：

```
make
make test
make front GO_WIN7=/path/to/go-win7/bin/go GOROOT=/path/to/go-win7
```

`make` 写入 `dist/clients/Windows/x86` 和 `dist/clients/Windows/x64`：`loft.exe`、`loft-svc.exe`、`lang/`、`tray/`、`wintun.dll`。`dist/` 被 gitignore。`wintun.dll` 从 `wintun-check/dist` 复制。该测试的 Makefile 下载 `wintun-0.14.1.zip`，并核对 SHA-256 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`。

`make test` 用主机编译器编译可移植解析器并运行。不打开窗口。

未设置 `GO_WIN7` 时 `make front` 什么也不做。模块是 Go 1.26。官方 Go 1.21 及更新版本不能以 Windows 7 为目标。使用已在 Windows 7 SP1 x64 上通过 `wintun-check` 验证的补丁工具链（go1.27.3）。`GOROOT` 指向那棵树。

客户端版本是 `clients/Windows/VERSION`（1.0.0）。不是仓库根目录的 `VERSION`。

## 发布

`make release` 产出两个渠道。

GitHub，Windows 7 SP1 及更高，x86 和 x64：

| 文件 | 是什么 |
|------|--------|
| `dist/clients/Windows/github/loft-<版本>-windows-x64.zip` 和 `loft-<版本>-windows-x86.zip` | Portable。解压即用。第一次打开隧道时安装服务。 |
| `dist/clients/Windows/github/loft-windows-x64-setup.exe` 和 `loft-windows-x86-setup.exe` | 安装程序。Program Files、loft 服务、开始菜单、卸载。没有 MSI。 |

`make portable` 写出这些 zip 和 `loft-windows-<版本>-SHA256SUMS.txt`。GitHub 版本启动时读取 `a3app/loft` 最新发布。更新的标签若带匹配的 zip，就替换正在运行的 exe 所在目录并重新启动。`v*` 标签上的 Dist 用仓库 `VERSION` 作为 `VER` 编译 zip，并挂到 Release。若已执行 `make front`，包内含 `loft-front.exe`。workflow 用固定的 XTLS/go-win7 SDK 编译这个二进制。

Microsoft Store，Windows 10 版本 1809 及更高，仅 x64：`dist/clients/Windows/store/x64`。没有 Wintun，也没有服务。SOCKS 可用。隧道留在 GitHub 版本里。这里写出布局和 `AppxManifest.xml`。打包和签名在 Windows 上：

```
makeappx pack /d dist\clients\Windows\store\x64 /p Loft.msix
```

打包前把 `Publisher="CN=a3app"` 换成 Partner Center 证书的 subject。Store 更新取代 GitHub 更新检查。

## 运行

Windows 7 需要 KB4474419 和 KB4490628，否则不接受 Wintun 的签名。把一个架构目录拷到机器上。`loft.exe`、`loft-svc.exe`、`loft-front.exe` 和 `wintun.dll` 放在一起，旁边是 `lang` 和 `tray`。

`loft-svc.exe`（或 `--install`）注册服务 `loft`（显示名 `Loft`，自动启动，依赖 Tcpip）。`--uninstall` 停止并删除它。`--uninstall-driver` 还会调用 `WintunDeleteDriver`。普通断开不卸载驱动。

双击 `loft.exe`。第二次启动把命令行交给已经在运行的实例（`Local\LoftClient`）。

用户文件在 `%APPDATA%\loft`：配置、DPAPI 密钥、首选项、路由、日志，以及按配置分开的实例文件（`loft-front` 的 `-instance-file`）。配置 YAML 不含密钥。导出为 `.loft` 会带上密钥，并先询问。

## 限制

- 这台编译机没有运行界面。这里核对的是 `make test`，以及 x86 和 x64 的 mingw 链接（`loft.exe` 和 `loft-svc.exe`）。
- Wintun 0.14.1 只在 Windows 7 SP1 x64 上做过实机验证。x86 已编译，未在实机上运行。
- 不删除 IPv6 默认路由，因此 IPv6 路径可以绕过 IPv4 隧道。
- `%ProgramData%\loft\svc.log` 到 1 MB 停止。服务崩溃后不会自行重启。
- 覆盖地址按 /32 安装。外部 IP 经 SOCKS 用 HTTP 查询；首选项里即使写了 https 也改成 HTTP，因为 Windows 7 可能没有 TLS 1.2。
- “关于”窗口里的 loft 用 Segoe UI 绘制。品牌 SVG 目前仍是文本元素，v1 不描轮廓。

决定记录在 [`design/DESIGN_zh.md`](design/DESIGN_zh.md) §10。
