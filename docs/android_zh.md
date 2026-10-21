# loft 在 Android 上

[English](android.md) · [Русский](android_ru.md) · **中文**

loft 在手机上怎么跑，然后是 Android TV / Google TV。目录：[clients/Android/](../clients/Android/)。计划：[clients/Android/PLAN_zh.md](../clients/Android/PLAN_zh.md)。引擎：[clients/mobile/](../clients/mobile/)。协议：[protocol_zh.md](protocol_zh.md)。导入 URI：[share_zh.md](share_zh.md)。UX：[clients/Android/design/DESIGN_zh.md](../clients/Android/design/DESIGN_zh.md)。模型：[clients/Android/design/index.html](../clients/Android/design/index.html)。

适配器是 Java。会话是 Go。

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

没有 `loft-front`。服务*就是*会话。

## 2. Bind

`make -C clients/mobile android` 写出 `clients/mobile/build/engine.aar`（`-androidapi 24`，16 KiB 页）。应用依赖这个文件。bind 需要 NDK。

Java 包 `app.loft.engine`：`Engine`、`Snapshot`、`PacketSink`、`SocketProtector`、`Mobile.parseShare`。

## 3. 发夹

通过 `Engine.SetProtector` 调 `VpnService.protect(fd)`。前置 IP 在 API 33+ 上也走 `excludeRoute`。

路径切换：以后可以用 `ConnectivityManager.NetworkCallback` 调 `NotifyPathChange`；薄适配器现在靠套接字死后 Go 里重连。

## 4. 门槛

| | |
|--|--|
| 引擎 AAR | API **24** |
| 手机应用 | API **26**，target **36** |
| 语言 | Java 17 |
| Android TV / Google TV | 同一个 AAR；Leanback flavor 以后再说（[计划](../clients/Android/PLAN_zh.md) 第 4 阶段） |
