# Loft · Windows

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Tray client for Windows 7 SP1 and later. The window is secondary. Product, profiles, route and texts match the macOS client. This tree is the Windows part: a Win32 shell, a service that owns Wintun, and the existing `loft-front` session.

Operator overlay for a stable admin channel. Not a bypass product.

## Pieces

| File | Runs as | Role |
|------|---------|------|
| `loft.exe` | the user | tray, windows, DPAPI keys, starts `loft-front` |
| `loft-svc.exe` | LocalSystem | Wintun adapter `Loft`, address, routes, packet pipe |
| `loft-front.exe` | the user | session. Separate Go binary. Not linked into the UI |
| `wintun.dll` | loaded by the service | Wintun 0.14.1, same directory as `loft-svc.exe`. Not in git |

SOCKS works without the service. The first tunnel connect offers to install it. There is no MSI. The service manifest is `asInvoker`, so the SCM can start it; `--install` elevates with `runas` when the caller is not an administrator.

## Build

From `clients/Windows`, with mingw-w64:

```
make
make test
make front GO_WIN7=/path/to/go-win7/bin/go GOROOT=/path/to/go-win7
```

`make` writes `dist/clients/Windows/x86` and `dist/clients/Windows/x64`: `loft.exe`, `loft-svc.exe`, `lang/`, `tray/`, `wintun.dll`. `dist/` is gitignored. `wintun.dll` is copied from `wintun-check/dist`. That Makefile fetches `wintun-0.14.1.zip` and checks SHA-256 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`.

`make test` compiles the portable parsers with the host compiler and runs them. It does not open a window.

`make front` does nothing when `GO_WIN7` is unset. The module is Go 1.26. Official Go 1.21 and later cannot target Windows 7. Use the patched toolchain that `wintun-check` already ran on Windows 7 SP1 x64 (go1.27.3). Set `GOROOT` to that tree.

The client version is `clients/Windows/VERSION` (1.0.0). It is not the repository `VERSION`.

## Ship

`make release` writes two channels.

GitHub, Windows 7 SP1 and later, x86 and x64:

| File | What it is |
|------|------------|
| `dist/clients/Windows/github/loft-<version>-windows-x64.zip` and `loft-<version>-windows-x86.zip` | Portable. Unzip and run. The first tunnel connect installs the service. |
| `dist/clients/Windows/github/loft-windows-x64-setup.exe` and `loft-windows-x86-setup.exe` | Installer. Program Files, the loft service, Start menu, uninstaller. No MSI. |

`make portable` writes those zips and `loft-windows-<version>-SHA256SUMS.txt`. The GitHub build checks the latest `a3app/loft` release at launch. A newer tag with the matching zip replaces the directory of the running exe and starts again. Dist on a `v*` tag builds the zips with `VER` from the repository `VERSION` and attaches them to the Release. `loft-front.exe` is included when `make front` has already produced it. The workflow builds that binary with the pinned XTLS/go-win7 SDK.

Microsoft Store, Windows 10 version 1809 and later, x64 only: `dist/clients/Windows/store/x64`. No Wintun and no service. SOCKS works. The tunnel stays in the GitHub build. This machine writes the layout and `AppxManifest.xml`. Pack and sign on Windows:

```
makeappx pack /d dist\clients\Windows\store\x64 /p Loft.msix
```

Replace `Publisher="CN=a3app"` with the Partner Center certificate subject before packing. Store updates replace the GitHub update check.

## Run

Windows 7 needs KB4474419 and KB4490628, or it rejects the Wintun signature. Copy one architecture directory onto the machine. Keep `loft.exe`, `loft-svc.exe`, `loft-front.exe` and `wintun.dll` together, with `lang` and `tray` beside them.

`loft-svc.exe` (or `--install`) registers the service `loft` (display name `Loft`, auto start, depends on Tcpip). `--uninstall` stops and deletes it. `--uninstall-driver` also calls `WintunDeleteDriver`. A normal disconnect does not remove the driver.

Double-click `loft.exe`. A second launch passes its command line to the running instance (`Local\LoftClient`).

User files live under `%APPDATA%\loft`: profiles, DPAPI keys, prefs, routes, journal, and the per-profile instance file that `loft-front` receives as `-instance-file`. Profile YAML does not contain the key. Export to `.loft` includes the key and asks first.

## Limits

- This build host does not run the UI. Checked here: `make test`, and a mingw link of `loft.exe` and `loft-svc.exe` for x86 and x64.
- Wintun 0.14.1 is hardware-checked on Windows 7 SP1 x64 only. The x86 binary is built and not run on hardware.
- IPv6 default routes are left in place, so an IPv6 path can pass around the IPv4 tunnel.
- `%ProgramData%\loft\svc.log` stops at 1 MB. The service has no failure restart.
- The overlay address is installed as /32. External IP lookup goes through SOCKS as HTTP, including when the preference URL is https: Windows 7 may not have TLS 1.2.
- About draws the word loft in Segoe UI. The brand SVG is still a text element, so v1 does not stroke outlines.

Decisions are recorded in [`design/DESIGN.md`](design/DESIGN.md) §10.
