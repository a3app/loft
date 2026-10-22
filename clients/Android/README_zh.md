# loft · Android

[English](README.md) · [Русский](README_ru.md) · **中文**

手机应用 + `VpnService`。会话是 Go [`clients/mobile`](../mobile/)。**Java 17**，不是 Kotlin：gomobile 已经吐 Java。

协议：[docs/protocol_zh.md](../../docs/protocol_zh.md)。对接：[docs/android_zh.md](../../docs/android_zh.md)。导入 URI：[docs/share_zh.md](../../docs/share_zh.md)。计划：[PLAN.md](PLAN.md)。UX：[design/DESIGN_zh.md](design/DESIGN_zh.md)。手机模型：[design/index.html](design/index.html)。电视模型：[design/tv/](design/tv/)。

overlay 是 Android 上的一条网络：系统里一条 VPN，每个前置套接字 `protect(fd)`，`NetworkCallback` → `NotifyPathChange`（新的 `0x02`，同一个 `instance_id`）。Snapshot 双栈。前置：get、ws、h2、quic、h3、l3。`connect` 只在节点上。

## 布局

```
clients/Android/
  PLAN.md                  手机已完成；电视模型在 design/tv；Leanback flavor 以后
  design/                  手机 UX 说明和 HTML 模型
  design/tv/               十英尺 UX 说明和 HTML 模型
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
make -C ../mobile android    # 需要 Android NDK；写出 ../mobile/build/engine.aar
# 然后 Android Studio → open clients/Android
# 或：gradle :app:assembleDebug
```

Id：`app.loft.android`。VPN 类型：`VpnService`。系统里一条 VPN。
