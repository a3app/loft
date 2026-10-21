# loft · Android

[English](README.md) · [Русский](README_ru.md) · **中文**

薄宿主 + `VpnService`。会话是 Go [`clients/mobile`](../mobile/)。**Java**，不是 Kotlin：gomobile 已经吐 Java；这个进程不装 Kotlin runtime。

协议：[docs/protocol_zh.md](../../docs/protocol_zh.md)。对接：[docs/android_zh.md](../../docs/android_zh.md)。导入 URI：[docs/share_zh.md](../../docs/share_zh.md)。计划：[PLAN_zh.md](PLAN_zh.md)。UX：[design/DESIGN_zh.md](design/DESIGN_zh.md)。模型：[design/index.html](design/index.html)。

## 布局

```
clients/Android/
  PLAN.md                  手机，然后 Android TV / Google TV
  design/                  UX 说明和 HTML 模型
  app/src/main/java/app/loft/android/
    MainActivity.java      粘贴 YAML / URI，打开 / 断开
    LoftVpnService.java    tun fd ↔ Engine, protect(fd)
  ../mobile/               共用 Go 引擎
```

minSdk **26**（GUI / 通知渠道）。引擎 AAR 按 API **24** 编。targetSdk **36**。arm64 `.so` 兼容 16 KiB 页。

## 编译

```bash
make -C ../mobile android    # 需要 Android NDK；写出 ../mobile/build/engine.aar
# 然后 Android Studio → open clients/Android
# 或：gradle :app:assembleDebug
```

Id：`app.loft.android`。VPN 类型：`VpnService`。系统里一条 VPN。
