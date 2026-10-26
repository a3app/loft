# loft на ОС Аврора

[English](aurora.md) · **Русский** · [中文](aurora_zh.md)

ОС Аврора — это форк Sailfish OS от OMP. Клиент для ConnMan: [clients/Sailfish/](../clients/Sailfish/). Стыковка: [sailfish_ru.md](sailfish_ru.md). План: [clients/Sailfish/PLAN.md](../clients/Sailfish/PLAN.md). Макет: [clients/Sailfish/design/index.html](../clients/Sailfish/design/index.html).

Пакет для Авроры лежит в [`clients/Sailfish/aurora/`](../clients/Sailfish/aurora/):

| | |
|--|--|
| Id | `app.loft.aurora` |
| SDK | Аврора SDK (`mb2` / Aurora IDE) |
| CMake | `-DLOFT_OS=aurora` (включается и автоматически, если найден `auroraapp`) |
| GUI | то же дерево Silica, что и у Sailfish |
| Сессия | тот же `loft-front` (`linux/arm64`); если glibc отличается, его нужно пересобрать в Aurora Build Engine |

Пакет для Sailfish — `harbour-loft` в [`clients/Sailfish/sailfish/`](../clients/Sailfish/sailfish/). Протокол, mux и тип ConnMan `loft` у них общие.
