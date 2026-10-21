# loft на ОС Аврора

[English](aurora.md) · **Русский** · [中文](aurora_zh.md)

ОС Аврора — форк Sailfish OS от OMP. Клиент ConnMan: [clients/Sailfish/](../clients/Sailfish/). Стыковка: [sailfish_ru.md](sailfish_ru.md). План: [clients/Sailfish/PLAN.md](../clients/Sailfish/PLAN.md). Макет: [clients/Sailfish/design/index.html](../clients/Sailfish/design/index.html).

Пакет Авроры — [`clients/Sailfish/aurora/`](../clients/Sailfish/aurora/):

| | |
|--|--|
| Id | `app.loft.aurora` |
| SDK | Аврора SDK (`mb2` / Aurora IDE) |
| CMake | `-DLOFT_OS=aurora` (также сам, если найден `auroraapp`) |
| GUI | то же дерево Silica, что у Sailfish |
| Сессия | тот же `loft-front` (`linux/arm64`); пересборка в Aurora Build Engine, если glibc другой |

Пакет Sailfish — `harbour-loft` в [`clients/Sailfish/sailfish/`](../clients/Sailfish/sailfish/). Протокол, mux и тип ConnMan `loft` те же.
