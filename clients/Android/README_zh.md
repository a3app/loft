# loft · Android

[English](README.md) · [Русский](README_ru.md) · **中文**

手机应用 + `VpnService`。会话是 Go [`clients/mobile`](../mobile/)。**Java 17**，不是 Kotlin：gomobile 已经吐 Java。

协议：[docs/protocol_zh.md](../../docs/protocol_zh.md)。对接：[docs/android_zh.md](../../docs/android_zh.md)。导入 URI：[docs/share_zh.md](../../docs/share_zh.md)。UX：[design/DESIGN_zh.md](design/DESIGN_zh.md)。手机模型：[design/index.html](design/index.html)。电视模型：[design/tv/](design/tv/)。

overlay 是 Android 上的一条网络：系统里一条 VPN，每个前置套接字 `protect(fd)`，`NetworkCallback` → `NotifyPathChange`（新的 `0x02`，同一个 `instance_id`）。Snapshot 双栈。前置：get、ws、h2、quic、h3、l3。`connect` 只在节点上。

## 布局

```
clients/Android/
  design/                  手机 UX 说明和 HTML 模型
  design/tv/               十英尺 UX 说明和 HTML 模型
  emulator/                macOS loft.app 内容（复制进 dist）
  app/src/main/java/app/loft/android/
    LoftVpnService.java    tun ↔ Engine，路径，双栈，protect
    MainActivity.java      主页 / 连接 / 设置
    EditActivity.java      loft / SSTP / VLESS / Trojan
    ScanActivity.java      相机二维码、粘贴、文件
  ../mobile/               共用 Go 引擎
```

minSdk **26**（GUI / 通知渠道）。引擎 AAR 按 API **24** 编。targetSdk **36**。arm64 `.so` 兼容 16 KiB 页。

## 编译

```bash
make android                 # 在仓库根目录；需要 Android NDK
# 或：make -C clients/Android
```

写出 `dist/clients/Android/app-debug.apk` 和 `loft.app`。中间文件在 `dist/clients/Android/gradle/`，不进 `clients/Android/`。双击 `loft.app` 会启动 AVD `loft-phone`、安装这份 APK、把 loft、Browser、Apps 和 Settings 钉到主屏并打开 loft。Apps 打开启动器应用抽屉；Settings 打开系统设置。AOSP 没有 Chrome；Browser 是 WebView Shell。Intel Mac + AMD GPU 走 SwiftShader（`-gpu swiftshader_indirect`）；host GLES 会黑屏。

Id：手机 `app.loft.android`，电视 `app.loft.android.tv`。VPN 类型：`VpnService`。每台设备一条系统 VPN。flavor `tv` 是 Leanback。渠道 `github`（默认）是 Dist/旁路安装，带应用内更新；`store` 是 Play，更新代码不编进包。`make -C clients/Android release` 用 `secrets/android-github.p12` 签名，写出 `loft-{ver}-android.apk` 和 `loft-{ver}-android-tv.apk`。第一次钥匙：`make -C clients/Android github-key`。
