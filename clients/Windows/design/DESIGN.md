# Loft · Windows client

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

A notification-area (tray) agent; the window is secondary. Mark, palette and type: [`brand/`](../../../docs/brand/). Interactive mock: [`index.html`](index.html). Protocol: [docs/protocol.md](../../../docs/protocol.md).

The client follows macOS v1: [`../../macOS/design/DESIGN.md`](../../macOS/design/DESIGN.md). Product (§1), protocol support (§2), profiles (§3), route and TUN (§4), split routing (§5), first run (§8), main window (§9) and texts (§10) are the same. This file covers only what differs on Windows.

---

## 1. Platform

The minimum version is **Windows 7**. It looks the same on 7, 8.1, 10 and 11 because the app draws its own UI. No Mica, Acrylic, Aero Glass, system theme or system accent colour. Window ground is Ink `#1A1714`; the accent is the lamp.

UI type is **Segoe UI** (in Windows since Vista). Avenir Next is not bundled. Weights follow the brand: headings Semibold 600, buttons and tabs 500, body Regular 400. The word `loft` in About is drawn from the brand SVG outlines, not set as text. Monospace is Consolas.

Builds: **x86 and x64**. The UI is **native Win32** with its own drawing (no WebView, no system visual styles).

---

## 2. Tray

The icon is [`docs/brand/svg/loft-tray.svg`](../../../docs/brand/svg/loft-tray.svg): copper body, lamp window (brand rule 4). Rasters are in [`docs/brand/tray/`](../../../docs/brand/tray/): 16 / 20 / 24 / 32 px for 100 / 125 / 150 / 200 % scale. Do not downscale from 1024 (rule 2).

| State | Body | Window |
|-------|------|--------|
| disconnected | copper | copper (`loft-tray-off`) |
| connecting | copper | lamp, blinking |
| connected | copper | lamp `#E8B84A` (`loft-tray`) |
| error / route closed | copper | red `#E24B4B` |

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
- The update window says "Download and run the installer" instead of replacing a DMG. The check is GitHub latest, as on macOS.

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

## 10. Open questions

Decided: native Win32 UI; x86 and x64 builds; `.loft` and `loft://` are an option in the installer and in Settings (§6); Segoe UI type.

Still to choose before code:

1. **Go and Windows 7.** The session runs in the Go process `loft-front`. Go 1.21 and later do not support Windows 7 / 8: build with Go 1.20.x or a patched toolchain.
   Checked 2026-10-26 with [`wintun-check`](../wintun-check/): a build with the patched XTLS/go-win7 (go1.27.3) works on Windows 7 SP1 x64 just like a Go 1.20.14 build. So the client can be written in current Go (its `go.mod` dependencies need it) and built for Windows with the patched toolchain. Not signed off yet.
2. **TUN driver.** Plan: **Wintun on every version** (Go: `golang.zx2c4.com/wireguard/tun`, L3, no Ethernet emulation). Microsoft no longer signs new kernel drivers for Windows 7, so the installer ships a prebuilt signed binary at a pinned version; we do not build the driver.
   Checked 2026-10-26 on Windows 7 SP1 **x64** with KB4474419 and KB4490628: Wintun 0.14.1 installs the driver, creates the adapter, receives and sends packets (ping through the adapter works), and uninstalls cleanly. **x86 is not checked yet.**
   For the client: set the adapter address with the IP Helper API (`CreateUnicastIpAddressEntry`), not `netsh`. `netsh` saves the address in the registry under the adapter GUID, and Windows restores it by itself the next time the adapter is created.
   If it does not: signed TAP-Windows6 9.24.x on Windows 7, Wintun on 8 and later. Costs of the fallback: two interface code paths, and CVE-2024-1305 in TAP ≤ 9.26 with no fixed Windows 7 build.
3. **Privileged part.** Routes, hold and reset are set by a Windows service (the `loft-priv` counterpart). Service name and installer (MSI / own installer) are not chosen.
4. **Key storage.** Windows Credential Manager or DPAPI (`CryptProtectData`) in a file.

---

## 11. Out of v1

As macOS §12. Also: Windows toast notifications are not in v1.
