# loft · brand

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Build and run: [docs/guide.md](../guide.md). Protocol: [docs/protocol.md](../protocol.md).

The mark is an L-roof with a lamp: the letter L, and a window that is lit when the tunnel is up.

Solid fills only on the mark and in the tray. A light glow on the window is used only on the app icon.

## Palette

| Token | Hex | Use |
|-------|-----|-----|
| Ink | `#1A1714` | app icon ground, mark on light |
| Stone | `#B7A99A` | L body on dark app icon |
| Copper | `#C67B3A` | Windows tray |
| Lamp | `#E8B84A` | window, connected |
| Lamp hi | `#F3D27A` | window highlight on app icon |
| Snow | `#F3EDE4` | mark on dark, tray on dark |
| Paper | `#E8DFD2` | fields, previews |
| Mute | `#8A7E72` | captions |

## Type

**Avenir Next** (Linotype, Adrian Frutiger / Akira Kobayashi, 2004). Geometric humanist sans: the round “o” rhymes with the lamp window.

| Role | Cut | Rule |
|------|-----|------|
| Wordmark / lockup | Demi 600 | lowercase `loft`, tracking −0.04em |
| Tagline | Regular 400 | *Loft Overlay For TLS*, title case, tracking −0.02em |
| UI headings | Demi 600 | sentence case, tracking −0.02em |
| Buttons, tabs | Medium 500 | no all-caps except short statuses |
| Body, docs | Regular 400 | line-height 1.45 |
| Code, keys `lt_…` | SF Mono / Menlo / Consolas | never Avenir |

Do not use Ultra Light, Heavy, or italic in the lockup.

Stack: `"Avenir Next", Avenir, "Helvetica Neue", "Segoe UI", ui-sans-serif, system-ui, sans-serif`.

On macOS the font is system and includes Cyrillic. On Windows, identical wordmark pixels require SVG outlines or an installed font file.

## Files

| File | Use |
|------|-----|
| `svg/loft-mark.svg` | protocol, site |
| `svg/loft-lockup.svg` | mark + word *loft* |
| `svg/loft-lockup-readme.svg` / `png/loft-lockup-readme.png` | README: social-preview lockup (crop) + *Loft Overlay For TLS* |
| `png/type-specimen.png` | type table |
| `svg/loft-og.svg` / `png/github-social-preview.png` | GitHub social preview 1280×640. Settings → General → Social preview |
| `png/a3-github-avatar.png` | GitHub profile avatar for a3app (A³, 2048×2048). Settings → Profile picture |
| `svg/a3-mark.svg` | A³ maker mark, live Avenir Next |
| `svg/a3-heart.svg` | Pixel heart 7×6, loft lamp |
| `svg/a3-colophon.svg` | `Made with love` + heart + `by A³` |
| `png/a3-colophon.png` | Colophon concept (About line) |
| `svg/loft-icon.svg` | app icon (dark) |
| `svg/loft-icon-light.svg` | icon on light |
| `svg/loft-favicon.svg` | 32px |
| `svg/loft-menubar-template.svg` | macOS `NSStatusItem` template |
| `app/loft.icns` | macOS bundle |
| `windows/loft.ico` | Windows app: `loft.exe`, setup, shortcuts. 16–256 px, 16 from the pixel grid |
| `windows/loft-file.ico` | `.loft` files in Explorer: paper sheet with the lit mark |
| `windows/loft-tray{,-off,-err}.ico` | Windows tray as resources: connected, disconnected, error (`#E24B4B` window) |
| `windows/loft-tray-light{,-off,-err}.ico` | the same with an ink body for a light taskbar |
| `windows/store/` | MSIX assets: `Square44x44Logo` (targetsize + unplated), `Square71x71`, `Square150x150`, `Wide310x150`, `Square310x310`, `StoreLogo`, `SplashScreen`, scale 100–400 |
| `tray/loft-tray-16.png` | Windows notify, connected |
| `tray/loft-tray-off-16.png` | Windows notify, disconnected |
| `tray/loft-tray{,-light}{,-off,-err}-{16,20,24,32}.png` | all tray states for 100 / 125 / 150 / 200 %, what `tray.c` loads |
| `menubar/loftTemplate.png` (`@2x` `@3x`) | macOS menu bar, connected |
| `menubar/loftTemplate-off-*.png` | macOS menu bar, disconnected |

## Rules

1. The roof covers only the stem of L; the foot extends right. It is an L.
2. At ≤16px use the `PIX16` grid in `generate.py`. Do not downscale from 1024.
3. macOS menu bar is a **template image** (black + alpha). The system tints it.
4. Windows tray defaults to **copper + lamp**.
5. Connected: the lamp is on. Disconnected: the same L, window off (macOS: no cut-out).
6. The word *loft* is lowercase, Avenir Next Demi, tracking −0.04em.
7. The tagline is the recursive acronym *Loft Overlay For TLS* (LOFT). Title case. The wordmark stays lowercase `loft`.
8. Colophon is English only: `Made with love` + pixel heart + `by A³`. Heart is 7×6 lamp cells. A³ is the maker mark, not the protocol mark.

Rebuild: `/tmp/loft-brand/bin/python generate.py`. Windows set only, no Chrome: `generate.py windows`
