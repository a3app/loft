# lab

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Public debug stands that import `internal/`. They stay in this module: a second Go module cannot import `github.com/a3app/loft/internal/…`.

Dist CLI (`loft`, `loft-front`, `loft-probe`, `loft-bench`) stays in [`clients/go/cmd`](../clients/go/cmd/). `make client` / `make front` / `make probe` / `make bench` write `dist/clients/go/`. Root `go build` dumps are gitignored.

| | |
|--|--|
| [`devstand`](devstand/) | Real panel + adm on a laptop. `make panel-dev`. Fake systemctl. |

Non-public measurement stands live next to this repo in `../loft-research/stands` (local, not git).
