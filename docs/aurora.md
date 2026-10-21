# loft on Aurora OS

**English** · [Русский](aurora_ru.md) · [中文](aurora_zh.md)

Aurora OS is the OMP fork of Sailfish OS. The ConnMan client is [clients/Sailfish/](../clients/Sailfish/). Integration: [sailfish.md](sailfish.md). Plan: [clients/Sailfish/PLAN.md](../clients/Sailfish/PLAN.md). Mock: [clients/Sailfish/design/index.html](../clients/Sailfish/design/index.html).

Aurora packaging is [`clients/Sailfish/aurora/`](../clients/Sailfish/aurora/):

| | |
|--|--|
| App id | `app.loft.aurora` |
| SDK | Aurora SDK (`mb2` / Aurora IDE) |
| CMake | `-DLOFT_OS=aurora` (also auto when `auroraapp` is found) |
| GUI | same Silica tree as Sailfish |
| Session | same `loft-front` (`linux/arm64`); rebuild in the Aurora Build Engine if glibc differs |

Sailfish packaging is `harbour-loft` under [`clients/Sailfish/sailfish/`](../clients/Sailfish/sailfish/). Protocol, mux, and ConnMan type `loft` are the same.
