# lab

[English](README.md) · **Русский** · [中文](README_zh.md)

Публичные отладочные стенды, которые импортируют `internal/`. Они живут в этом модуле: второй модуль Go не может импортировать `github.com/a3app/loft/internal/…`.

Dist CLI (`loft`, `loft-front`, `loft-probe`, `loft-bench`) остаётся в [`clients/go/cmd`](../clients/go/cmd/). `make client` / `make front` / `make probe` / `make bench` пишут в `dist/clients/go/`. Свалка `go build` в корне в `.gitignore`.

| | |
|--|--|
| [`devstand`](devstand/) | Настоящая панель и adm на ноутбуке. `make panel-dev`. systemctl подменён. |

Непубличные замерные стенды — рядом, в `../loft-research/stands` (локально, без git).
