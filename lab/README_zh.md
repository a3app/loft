# lab

[English](README.md) · [Русский](README_ru.md) · **中文**

公开调试台，会 import `internal/`。它们留在本模块：另一个 Go 模块不能 import `github.com/a3app/loft/internal/…`。

发行 CLI（`loft`、`loft-front`、`loft-probe`、`loft-bench`）仍在 [`clients/go/cmd`](../clients/go/cmd/)。`make client` / `make front` / `make probe` / `make bench` 写到 `dist/clients/go/`。根目录的 `go build` 产物已进 `.gitignore`。

| | |
|--|--|
| [`devstand`](devstand/) | 笔记本上的真实面板与 adm。`make panel-dev`。假 systemctl。 |

非公开测量台在旁边的 `../loft-research/stands`（本地，无 git）。
