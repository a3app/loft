# loft 在 Android 上

[English](android.md) · [Русский](android_ru.md) · **中文**

loft 在手机上怎么跑，然后是 Android TV / Google TV。目录：[clients/Android/](../clients/Android/)。计划：[clients/Android/PLAN.md](../clients/Android/PLAN.md)。引擎：[clients/mobile/](../clients/mobile/)。协议：[protocol_zh.md](protocol_zh.md)。导入 URI：[share_zh.md](share_zh.md)。UX：[clients/Android/design/DESIGN_zh.md](../clients/Android/design/DESIGN_zh.md)。手机模型：[clients/Android/design/index.html](../clients/Android/design/index.html)。电视模型：[clients/Android/design/tv/](../clients/Android/design/tv/)。

适配器是 Java 17 Views。会话是 Go。overlay 是 Android 上的一条网络（一条 VPN）：`protect(fd)`，`NetworkCallback` → `NotifyPathChange`，Snapshot 双栈。

---

## 1. 一个进程

```
应用
  → VpnService 拥有的 tun0
      → LoftVpnService  (app.loft.android)
          → clients/mobile  (gomobile AAR)
              → loft / SSTP / VLESS / Trojan
              → 前置套接字上 protect(fd)
```

没有 `loft-front`。服务*就是*会话。以 `systemExempted` 前台服务类型启动（API 34+ 上的 VpnService）。

## 2. Bind

`make -C clients/mobile android` 写出 `clients/mobile/build/engine.aar`（`-androidapi 24`，16 KiB 页）。应用依赖这个文件。bind 需要 NDK。

Java 包 `app.loft.engine`：`Engine`、`Snapshot`、`PacketSink`、`SocketProtector`、`Mobile.parseShare`、`Mobile.parseShareAll`。Snapshot 带 `IP` / `IP6`、`Prefix` / `Prefix6`、`Routes`、`FrontIPs`。

## 3. 发夹和路径

通过 `Engine.SetProtector` 调 `VpnService.protect(fd)`。前置 IP 从 TUN 路由里减去，API 33+ 再 `excludeRoute`。

路径切换：默认网络上的 `ConnectivityManager.NetworkCallback`。服务先 `setUnderlyingNetworks`，再 `NotifyPathChange`。loft：新的 `0x02`，同一个 `instance_id`。overlay IP 不变。SSTP/VLESS/Trojan 重拨；IPCP 若发新地址就重建 TUN。

## 4. 门槛

| | |
|--|--|
| 引擎 AAR | API **24** |
| 手机应用 | API **26**，target **36** |
| 语言 | Java 17 |
| Android TV / Google TV | 同一个 AAR；十英尺模型 [`design/tv/`](../clients/Android/design/tv/)；Leanback flavor 以后再说（[计划](../clients/Android/PLAN.md) 第 4 阶段） |

## 5. 模拟器

`make android` 把 `loft.app` 和 `app-debug.apk` 写到 [`dist/clients/Android/`](../dist/clients/Android/)。Gradle 中间文件在那里的 `gradle/`，不进 `clients/Android/`。在 macOS 上双击 `loft.app`：启动 AVD `loft-phone`，`adb install -r` 这份 APK，把 loft 和 Browser（AOSP WebView Shell）钉到主屏，打开 `app.loft.android/.MainActivity`。内容在 [`clients/Android/emulator/`](../clients/Android/emulator/)。SDK：`ANDROID_HOME` 或 `/usr/local/share/android-commandlinetools`。日志：`~/Library/Logs/loft-emulator.log`。Intel Mac 配 AMD GPU 时 `-gpu auto`（gfxstream/host GLES）会黑屏；`loft.app` 用 `-gpu swiftshader_indirect` 启动 AVD。
