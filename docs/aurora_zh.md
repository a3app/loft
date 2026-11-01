# loft 在 Aurora OS 上

[English](aurora.md) · [Русский](aurora_ru.md) · **中文**

Aurora OS 是 Sailfish OS 的 OMP 分支。ConnMan 客户端在 [clients/Sailfish/](../clients/Sailfish/)。对接：[sailfish_zh.md](sailfish_zh.md)。计划：[clients/Sailfish/PLAN.md](../clients/Sailfish/PLAN.md)。模型：[clients/Sailfish/design/index.html](../clients/Sailfish/design/index.html)。

Aurora 打包在 [`clients/Sailfish/aurora/`](../clients/Sailfish/aurora/)：

| | |
|--|--|
| 应用 id | `app.loft.aurora` |
| SDK | Aurora SDK（`mb2` / Aurora IDE） |
| CMake | `-DLOFT_OS=aurora`（找到 `auroraapp` 也会自动选） |
| GUI | 和 Sailfish 同一棵 Silica 树 |
| 会话 | 同一个 `loft-front`（`linux/arm64`）；glibc 不同就在 Aurora Build Engine 里重编 |

Sailfish 打包是 [`clients/Sailfish/sailfish/`](../clients/Sailfish/sailfish/) 下的 `harbour-loft`。协议、mux 和 ConnMan 类型 `loft` 相同。
