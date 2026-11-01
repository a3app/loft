# loft-wintun-check

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Checks that the Wintun driver works on Windows 7 SP1 (x86 and x64). The client in [`../`](../) uses the same 0.14.1 driver. See [`../design/DESIGN.md`](../design/DESIGN.md) §10.

The program installs Wintun 0.14.1, creates the adapter `LoftTest` with `10.255.0.1/24`, pings `10.255.0.2` and answers the ping itself through the adapter, so both directions are tested. At the end it removes the adapter and the driver. Everything goes to `loft-wintun-check.txt` next to the exe, including Wintun's own log lines. Screen output is in Russian.

## Build

```
make GO=~/sdk/go1.20.14/bin/go zip
make GO=/path/to/go-win7/bin/go GOROOT=/path/to/go-win7 zip
```

The module is separate (`go 1.20`) so that both official Go 1.20.14 and a patched Go for Windows 7 build it. `wintun.dll` is not in git: `make` fetches `wintun-0.14.1.zip` from wintun.net and checks its SHA-256.

## Run

1. Unpack `x86` (32-bit Windows) or `x64` (64-bit Windows); the exe and `wintun.dll` stay together.
2. Run `loft-wintun-check.exe` and accept the administrator prompt.
3. Wait for the result line and press Enter. Send back `loft-wintun-check.txt`.

If the adapter cannot be created, check update KB4474419 (SHA-2 code signing). Without it Windows 7 rejects the driver signature.
