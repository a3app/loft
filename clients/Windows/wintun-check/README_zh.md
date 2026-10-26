# loft-wintun-check

[English](README.md) · [Русский](README_ru.md) · **中文**

在基于 Wintun 构建 Windows 客户端之前，检查该驱动能否在 Windows 7 SP1（x86 和 x64）上工作。见 [`../design/DESIGN_zh.md`](../design/DESIGN_zh.md) §10。

程序安装 Wintun 0.14.1，创建地址为 `10.255.0.1/24` 的适配器 `LoftTest`，ping `10.255.0.2` 并通过适配器自行应答，从而验证双向收发。结束时删除适配器和驱动。所有信息（包括 Wintun 自身的日志）写入 exe 旁的 `loft-wintun-check.txt`。屏幕输出为俄语。

## 构建

```
make GO=~/sdk/go1.20.14/bin/go zip
make GO=/path/to/go-win7/bin/go GOROOT=/path/to/go-win7 zip
```

模块独立（`go 1.20`），官方 Go 1.20.14 和为 Windows 7 打补丁的 Go 都能构建。`wintun.dll` 不在 git 中：`make` 从 wintun.net 下载 `wintun-0.14.1.zip` 并校验 SHA-256。

## 运行

1. 解压 `x86`（32 位 Windows）或 `x64`（64 位 Windows）；exe 与 `wintun.dll` 放在一起。
2. 运行 `loft-wintun-check.exe` 并同意管理员提示。
3. 等待结果行出现后按 Enter，发回 `loft-wintun-check.txt`。

若无法创建适配器，请检查更新 KB4474419（SHA-2 代码签名）。缺少该更新时 Windows 7 会拒绝驱动签名。
