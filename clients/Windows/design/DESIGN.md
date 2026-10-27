# Loft · Windows client

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

A notification-area (tray) agent; the window is secondary. Mark, palette and type: [`brand/`](../../../docs/brand/). Interactive mock: [`index.html`](index.html). Protocol: [docs/protocol.md](../../../docs/protocol.md).

The client follows macOS v1: [`../../macOS/design/DESIGN.md`](../../macOS/design/DESIGN.md). Product (§1), protocol support (§2), profiles (§3), route and TUN (§4), split routing (§5), first run (§8), main window (§9) and texts (§10) are the same. This file covers only what differs on Windows.

---

## 1. Platform

The minimum version is **Windows 7**. It looks the same on 7, 8.1, 10 and 11 because the app draws its own UI. No Mica, Acrylic, Aero Glass, system theme or system accent colour. Window ground is Ink `#1A1714`; the accent is the lamp.

UI type is **Segoe UI** (in Windows since Vista). Avenir Next is not bundled. Weights follow the brand: headings Semibold 600, buttons and tabs 500, body Regular 400. The word `loft` in About is Segoe UI in the lamp colour: the brand SVG is still a `<text>` element, so v1 does not stroke outlines. Monospace is Consolas.

Builds: **x86 and x64**. The UI is **native Win32** with its own drawing (no WebView, no system visual styles).

---

## 2. Tray

The icon is [`docs/brand/svg/loft-tray.svg`](../../../docs/brand/svg/loft-tray.svg): snow `#F3EDE4` body, lamp window (brand rule 4); on a light taskbar the body is ink (`loft-tray-light*`). Rasters are in [`docs/brand/tray/`](../../../docs/brand/tray/): 16 / 20 / 24 / 32 px for 100 / 125 / 150 / 200 % scale. Do not downscale from 1024 (rule 2).

| State | Body | Window |
|-------|------|--------|
| disconnected | snow | snow (`loft-tray-off`) |
| connecting | snow | lamp, blinking |
| connected | snow | lamp `#E8B84A` (`loft-tray`) |
| error / route closed | snow | red `#E24B4B` (`loft-tray-err`) |

In multi mode the macOS §7 rule applies: yellow if at least one session is up; red if at least one has failed.

Mouse:

| Action | Opens |
|--------|-------|
| Left click | tray flyout (§3) |
| Right click | tray menu (§4) |
| Double click | Open Loft (status window) |

Hover tooltip: `Loft — Connected: Office` / `Loft — No active connections`.

---

## 3. Tray flyout

This is the macOS §7.1 popover with the same content and behaviour: title, Connect / Disconnect / Retry, Reset routes when hold is armed, Connection / External IP / Overlay / Route / SOCKS5 rows, the accordion in multi mode, footer Open Loft | Settings | Quit.

Differences:

- The flyout sits at the screen edge on the taskbar side, next to the notification area. If the taskbar is at the top or a side, the flyout opens at that edge.
- No arrow.
- Solid `#201c19` ground, no blur.
- Closes on focus loss and on Esc.
- "Connection ›" opens the profile list to the left of the flyout (macOS uses an `NSMenu`).

Size is 300 × hug, as on macOS.

---

## 4. Tray menu

Windows has no app menu bar. The macOS **Loft**, **Connections** and **Tools** menus are gathered into the icon's context menu:

```
Disconnect · Office           (Connect when closed)
Connections              ›    profiles: check on the current one, status on the right
───
Open Loft
Settings
✓ Split routing               the same switch as in Settings
Routes
Journal
───
Language                 ›    native names, check on the current one
About Loft
───
Quit Loft
```

Icons are the same as in the macOS menus (window, gear, branch, document, globe). The macOS **Edit** menu is not carried over: editing opens from the Connections list.

---

## 5. Windows

The app draws its own frame and title bar. Title bar height is 32 px: the 16 px tray mark, `Loft`, then **Minimize** and **Close** buttons, 46 px each, on the right. No Maximize: windows have a fixed size (macOS §11). Close turns red `#C42B1C` on hover. Corner radius is 8 px. The shadow is the app's own.

Closing a window does not quit Loft: the agent stays in the tray. Quit only from the tray menu or the flyout (**Quit**).

Window sizes are as in macOS §11, plus the 32 px title bar.

---

## 6. Settings

Tabs and items are as on macOS. Two items on the General tab differ:

| macOS | Windows | Caption |
|-------|---------|---------|
| Open at login (Login item) | **Start with Windows** | In the tray, no window |
| Show Dock icon: always / when a window is open / never | **Taskbar button**: always / when a window is open / never | The tray icon stays |

"Extended journal" writes lines from the loft service instead of `loft-priv`.

The General tab ends with two items that macOS does not have:

| Item | Caption | Default |
|------|---------|---------|
| **Open .loft files** | Double click opens Loft | on |
| **Open loft:// links** | Links in the browser open Loft | on |

A file or link opens import (as Import in Connections). Off removes the registration, on sets it again. Defaults turns both on.

Installer: the install step has the same option, a checkbox "Open .loft and loft:// in Loft", **checked by default**. Unchecked = both items in Settings are off.

---

## 7. Import, export, update

- Import and export use the standard Windows Open / Save As dialogs. The drop zone in the mock shows that a file can be dropped on the window.
- The update window says "Download and replace this copy." The check is GitHub latest, as on macOS. It downloads `loft-<version>-windows-x64.zip` or the x86 zip, replaces the directory of the running exe, and starts again. A copy under Program Files asks for administrator. The service image is replaced on disk; a running service keeps the old image until it is restarted.
- Dist on a `v*` tag publishes the two portable zips and `loft-windows-<version>-SHA256SUMS.txt`. `make release` also builds the installers and the Store layout.
  - Portable: `loft-<version>-windows-x64.zip` and `loft-<version>-windows-x86.zip`. Unzip and run. Windows 7 SP1 and later. The first tunnel connect installs the service.
  - Installer: `loft-windows-x64-setup.exe` and `loft-windows-x86-setup.exe`. Copies into Program Files, registers the service, adds a Start menu shortcut and an uninstaller. No MSI. The same OS floor. A checkbox "Open .loft and loft:// in Loft" is on by default; off writes `assoc.off` beside the exe, and the first launch turns both associations off.
  - The GitHub channel is the portable zips. The app reads the latest `a3app/loft` release. The installers are not the update payload.
  - Microsoft Store is a separate x64 package, Windows 10 version 1809 and later. It has no Wintun and no `loft-svc`: a Store package cannot install a kernel driver or a LocalSystem service. SOCKS works. Updates come from the Store. `make store` writes `dist/clients/Windows/store/x64` with `AppxManifest.xml`. `makeappx` and the Partner Center signature are done on Windows. The `Publisher` value `CN=a3app` is replaced by the store certificate.

---

## 8. Storage

| What | Where |
|------|-------|
| Profiles | `%APPDATA%\loft\profiles\<id>.yaml` |
| Active pointer | `%APPDATA%\loft\active` |
| Sessions | `%APPDATA%\loft\sessions\<id>` |
| Settings | `%APPDATA%\loft\prefs.yaml` |
| Split lists | `%APPDATA%\loft\routes.txt` (format: macOS §5.5) |
| Key | Windows protected storage (see §10). The key is never in plain text in files or in persistent UI fields. |

---

## 9. Texts

Strings come from the shared macOS packs [`../../macOS/lang/`](../../macOS/lang/). On top sits [`lang/win.lang`](lang/win.lang): only the keys that read differently on Windows (`login`, `login_d`, `dock`, `dock_d`, `diag_d`, `upd.offer.body`, `win.min`, `win.close`, `assoc_file`, `assoc_link` and their captions, tray scene labels). Lookup order: Windows pack for the language → English Windows pack → macOS pack. Translated for en / ru / zh; other languages show these keys in English for now.

As on macOS, texts use *route*, *direct Internet*, *reset*; never "kill switch" or "shield".

---

## 10. Decisions

Closed in the v1 tree under [`../`](../). Build and run notes: [`../README.md`](../README.md).

1. **UI.** C11, Win32, GDI. No WebView and no system visual styles. `_WIN32_WINNT=0x0601`. x86 and x64 with mingw-w64 (`-mwindows -static -municode`). Segoe UI and Consolas. Windows 7 ships Regular and Bold only; the UI asks for Medium and Semibold and takes the GDI mapping. DPI is `GetDeviceCaps(LOGPIXELSX)` plus `SetProcessDPIAware`. The manifest lists Windows 7, 8, 8.1 and 10, `dpiAware` true, `asInvoker`. No Vista id. About shows `LOFT_VERSION` from [`../VERSION`](../VERSION) (1.0.0), not the macOS about string.
2. **Session.** `loft-front.exe` next to `loft.exe`. The module is Go 1.26. Official Go 1.21+ cannot target Windows 7, so the front is built with patched XTLS/go-win7 (`make front GO_WIN7=...`). The UI does not link Go. Without `GO_WIN7`, `make` skips that binary. go1.27.3 via that toolchain already ran on Windows 7 SP1 x64 in [`wintun-check`](../wintun-check/).
3. **TUN.** Wintun 0.14.1 only. Not songgao/water and not `netsh`. The service creates adapter name `Loft`, type `Loft`, sets the address with `CreateUnicastIpAddressEntry`, MTU 1280 with `SetIpInterfaceEntry`, and routes with `CreateIpForwardEntry2`. Packets cross `\\.\pipe\loft-tun` in byte mode: u32le length plus the IP packet, maximum 65535. `loft-front` runs the userspace stack. `WintunDeleteDriver` runs only on `--uninstall-driver`. The adapter GUID is the same 16 bytes as `wintun-check` (`6c 6f 66 74 2d 77 69 6e 74 75 6e 2d 63 68 6b 01`); Windows shows `{74666F6C-772D-6E69-7475-6E2D63686B01}`. Session capacity `0x400000`. The UI installs the overlay address as /32. If the default route is off, it also sends the overlay /24.
4. **Service.** Name `loft`, display name `Loft`, own process, auto start, depends on Tcpip. Binary `loft-svc.exe`. Control pipe `\\.\pipe\loft-ctl` (message mode). No MSI. `--install` elevates when needed. SOCKS does not need the service; the first TUN connect offers to install it. Pipe SDDL `D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;IU)`. `tun-close` while hold is on replies `ok held` and leaves the adapter. `reset` always restores the saved IPv4 default and DNS. The physical IPv4 default is snapshotted before it is replaced. DNS is the `NameServer` value on the adapter interface key, then `DnsFlushResolverCache` and `DhcpNotifyConfigChange`. Log `%ProgramData%\loft\svc.log`, cap 1 MB, no failure restart.
5. **Keys.** DPAPI `CryptProtectData` for the current user, `%APPDATA%\loft\keys\<id>.bin`. Profile YAML has no token, uuid or password. `.loft` export includes the key and warns. `loft-front` still receives `-token` on its command line, as on macOS.
6. **Storage and prefs.** Profiles, active id, instance file, prefs, routes and journal under `%APPDATA%\loft`, as §8. Defaults match the macOS client (language ru, hold off, failover on, split off, default route on, shared SOCKS 1080, update check on). Windows adds per-user associations for `.loft` and `loft://` (on by default) and Start with Windows (`HKCU\...\Run`, value `Loft`, command `"exe" --tray`). Update check reads GitHub latest `a3app/loft` and, when the tag is newer and the portable zip is on that release, replaces this copy from `loft-<version>-windows-x64.zip` or the x86 zip. Dist stamps `LOFT_VERSION` from the repository `VERSION` so the tag matches. The Store build does not check GitHub.
7. **Limits that stay.** IPv6 default routes are not removed. x86 Wintun is not hardware-checked. The service log is not rotated past the cap. The UI on this tree was linked, not run on Windows.

---

## 11. Out of v1

As macOS §12. Also: Windows toast notifications are not in v1.
