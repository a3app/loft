#!/usr/bin/env python3
"""Loft brand kit — mark, app icon, Windows tray, macOS menu bar.

Builds SVG sources and raster icons (PNG, ICNS, ICO) from the L-roof silhouette.
"""

from __future__ import annotations

import math
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont

ROOT = Path(__file__).resolve().parent
SVG = ROOT / "svg"
PNG = ROOT / "png"
APP = ROOT / "app"
TRAY = ROOT / "tray"
MENUBAR = ROOT / "menubar"

INK = "#1A1714"
SLATE = "#2C2723"
SLATE_HI = "#3A332E"
LAMP = "#E8B84A"
LAMP_HI = "#F3D27A"
COPPER = "#C67B3A"
SNOW = "#F3EDE4"
PAPER = "#E8DFD2"
MUTE = "#8A7E72"
# Recursive acronym: LOFT = Loft Overlay For TLS. Wordmark stays lowercase `loft`.
TAGLINE = "Loft Overlay For TLS"

CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

# Unit-space silhouette of the L-roof (x right, y down). Roof covers the stem only.
# Clockwise from the peak. Eaves stay sharp so they don't grow "knobs".
MARK = [
    (0.38, 0.00),  # peak
    (0.62, 0.28),  # right eave
    (0.62, 0.74),  # inner L corner
    (1.00, 0.74),  # foot top-right
    (1.00, 1.00),  # foot bottom-right
    (0.12, 1.00),  # bottom-left
    (0.12, 0.28),  # left eave
]
# Peak slight round; eaves 0; inner L and foot rounded.
MARK_R = [0.012, 0.0, 0.055, 0.040, 0.048, 0.048, 0.0]
WINDOW = (0.26, 0.36, 0.22, 0.26)  # x, y, w, h — taller loft sash
WINDOW_RX = 0.032
STONE = "#B7A99A"  # L body on the dark app icon


def _len(a, b) -> float:
    """Return Euclidean distance between two 2-D points."""
    return math.hypot(b[0] - a[0], b[1] - a[1])


def rounded_polygon_path(points, radii) -> str:
    """Return an SVG path for a polygon with per-vertex corner radii."""
    n = len(points)
    if isinstance(radii, (int, float)):
        radii = [radii] * n
    parts: list[str] = []
    for i in range(n):
        p0 = points[i - 1]
        p1 = points[i]
        p2 = points[(i + 1) % n]
        l1 = _len(p1, p0) or 1.0
        l2 = _len(p1, p2) or 1.0
        r = min(radii[i], l1 * 0.49, l2 * 0.49)
        if r < 1e-6:
            if i == 0:
                parts.append(f"M{p1[0]:.4f} {p1[1]:.4f}")
            else:
                parts.append(f"L{p1[0]:.4f} {p1[1]:.4f}")
            continue
        u1 = ((p0[0] - p1[0]) / l1, (p0[1] - p1[1]) / l1)
        u2 = ((p2[0] - p1[0]) / l2, (p2[1] - p1[1]) / l2)
        a = (p1[0] + u1[0] * r, p1[1] + u1[1] * r)
        b = (p1[0] + u2[0] * r, p1[1] + u2[1] * r)
        cross = u1[0] * u2[1] - u1[1] * u2[0]
        sweep = 0 if cross > 0 else 1
        if i == 0:
            parts.append(f"M{a[0]:.4f} {a[1]:.4f}")
        else:
            parts.append(f"L{a[0]:.4f} {a[1]:.4f}")
        parts.append(f"A{r:.4f} {r:.4f} 0 0 {sweep} {b[0]:.4f} {b[1]:.4f}")
    parts.append("Z")
    return " ".join(parts)


def rounded_rect_path(x, y, w, h, rx) -> str:
    """Return an SVG path for a rounded rectangle."""
    rx = min(rx, w / 2, h / 2)
    return (
        f"M{x+rx:.4f} {y:.4f} "
        f"H{x+w-rx:.4f} A{rx:.4f} {rx:.4f} 0 0 1 {x+w:.4f} {y+rx:.4f} "
        f"V{y+h-rx:.4f} A{rx:.4f} {rx:.4f} 0 0 1 {x+w-rx:.4f} {y+h:.4f} "
        f"H{x+rx:.4f} A{rx:.4f} {rx:.4f} 0 0 1 {x:.4f} {y+h-rx:.4f} "
        f"V{y+rx:.4f} A{rx:.4f} {rx:.4f} 0 0 1 {x+rx:.4f} {y:.4f} Z"
    )


def map_pts(pts, x, y, w, h):
    """Map unit-space points into a rectangle (x, y, w, h)."""
    return [(x + px * w, y + py * h) for px, py in pts]


def map_r(rs, s):
    """Scale a list of unit-space corner radii by s."""
    return [r * s for r in rs]


def mark_paths(x, y, w, h):
    """Return SVG path data for the L-roof body and the lamp window."""
    pts = map_pts(MARK, x, y, w, h)
    s = min(w, h)
    body = rounded_polygon_path(pts, map_r(MARK_R, s))
    wx, wy, ww, wh = WINDOW
    win = rounded_rect_path(x + wx * w, y + wy * h, ww * w, wh * h, WINDOW_RX * s)
    return body, win


def svg_doc(vb, body, extras="") -> str:
    """Wrap SVG body in a square viewBox document of side vb."""
    return (
        f'<?xml version="1.0" encoding="UTF-8"?>\n'
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {vb} {vb}" '
        f'width="{vb}" height="{vb}" fill="none">\n'
        f"{body}{extras}</svg>\n"
    )


def mark_group(x, y, w, h, fill, lamp, hole=False, glow=False) -> str:
    """Return SVG for the mark: filled body plus lamp, or a punched hole."""
    body, win = mark_paths(x, y, w, h)
    if hole:
        return (
            f'  <path fill="{fill}" fill-rule="evenodd" '
            f'd="{body} {win}"/>\n'
        )
    glow_filter = ' filter="url(#lamp-glow)"' if glow else ""
    return (
        f'  <path fill="{fill}" d="{body}"/>\n'
        f'  <path fill="{lamp}"{glow_filter} d="{win}"/>\n'
    )


def write(path: Path, text: str) -> None:
    """Write text to path, creating parent directories."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def write_svgs() -> None:
    """Write protocol mark, app icon, tray, menubar, and lockup SVGs."""
    # Protocol mark — transparent, ink on nothing
    write(
        SVG / "loft-mark.svg",
        svg_doc(
            96,
            f"  <!-- Loft mark: L-roof + lamp. Connected = lamp on. -->\n"
            + mark_group(8, 6, 80, 84, INK, LAMP),
        ),
    )
    write(
        SVG / "loft-mark-on-dark.svg",
        svg_doc(96, mark_group(8, 6, 80, 84, SNOW, LAMP)),
    )
    write(
        SVG / "loft-mark-mono.svg",
        svg_doc(96, mark_group(8, 6, 80, 84, "#000", "#000", hole=True)),
    )
    write(
        SVG / "loft-favicon.svg",
        svg_doc(32, mark_group(2, 1, 28, 30, INK, LAMP)),
    )

    glow = """  <defs>
    <filter id="lamp-glow" x="-40%" y="-40%" width="180%" height="180%">
      <feGaussianBlur in="SourceGraphic" stdDeviation="2.2" result="b"/>
      <feMerge>
        <feMergeNode in="b"/>
        <feMergeNode in="SourceGraphic"/>
      </feMerge>
    </filter>
  </defs>
"""
    wx, wy, ww, wh = WINDOW
    inner = rounded_rect_path(
        18 + (wx + 0.04) * 64,
        11 + (wy + 0.04) * 72,
        (ww - 0.08) * 64,
        (wh - 0.08) * 72,
        0.02 * 64,
    )
    write(
        SVG / "loft-icon.svg",
        svg_doc(
            96,
            glow
            + f'  <rect width="96" height="96" rx="22" fill="{INK}"/>\n'
            + mark_group(18, 11, 64, 72, STONE, LAMP, glow=True)
            + f'  <path fill="{LAMP_HI}" d="{inner}"/>\n',
        ),
    )
    write(
        SVG / "loft-icon-light.svg",
        svg_doc(
            96,
            f'  <rect width="96" height="96" rx="22" fill="{SNOW}"/>\n'
            + mark_group(18, 11, 64, 72, INK, LAMP),
        ),
    )

    # Tray source (aligned to 16 grid conceptually, drawn large)
    write(
        SVG / "loft-tray.svg",
        svg_doc(16, mark_group(1, 0.5, 14, 15, COPPER, LAMP)),
    )
    write(
        SVG / "loft-tray-off.svg",
        svg_doc(16, mark_group(1, 0.5, 14, 15, COPPER, COPPER)),
    )
    write(
        SVG / "loft-menubar-template.svg",
        svg_doc(18, mark_group(1.2, 0.6, 15.6, 16.6, "#000", "#000", hole=True)),
    )
    write(
        SVG / "loft-menubar-template-off.svg",
        svg_doc(18, mark_group(1.2, 0.6, 15.6, 16.6, "#000", "#000", hole=False)),
    )

    # Lockup
    mx, my, mw, mh = 6, 16, 58, 64
    lockup = f'''<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 160 96" width="160" height="96" fill="none">
{mark_group(mx, my, mw, mh, INK, LAMP)}  <text x="76" y="64" font-family="Avenir Next, Avenir, Helvetica Neue, sans-serif"
        font-size="44" font-weight="600" letter-spacing="-0.04em" fill="{INK}">loft</text>
</svg>
'''
    write(SVG / "loft-lockup.svg", lockup)
    write(
        SVG / "loft-lockup-on-dark.svg",
        f'''<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 160 96" width="160" height="96" fill="none">
  <rect width="160" height="96" rx="18" fill="{INK}"/>
{mark_group(mx, my, mw, mh, SNOW, LAMP)}  <text x="76" y="64" font-family="Avenir Next, Avenir, Helvetica Neue, sans-serif"
        font-size="44" font-weight="600" letter-spacing="-0.04em" fill="{SNOW}">loft</text>
</svg>
''',
    )
    write(
        SVG / "loft-wordmark.svg",
        f'''<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 160 48" width="160" height="48" fill="none">
  <text x="0" y="36" font-family="Avenir Next, Avenir, Helvetica Neue, sans-serif"
        font-size="36" font-weight="600" letter-spacing="-0.04em" fill="{INK}">loft</text>
</svg>
''',
    )
    # Social-preview lockup (1280×640 space): large mark, word to the right,
    # tagline under the word at the L foot. README crops that field.
    face = "Avenir Next, Avenir, Helvetica Neue, sans-serif"
    tag = TAGLINE
    og_body = (
        "M37.7640 6.7191 A0.9600 0.9600 0 0 1 39.0071 6.7437 L57.6000 29.5200 "
        "L57.6000 63.7600 A4.4000 4.4000 0 0 0 62.0000 68.1600 L84.8000 68.1600 "
        "A3.2000 3.2000 0 0 1 88.0000 71.3600 L88.0000 86.1600 A3.8400 3.8400 0 0 1 "
        "84.1600 90.0000 L21.4400 90.0000 A3.8400 3.8400 0 0 1 17.6000 86.1600 "
        "L17.6000 29.5200 Z"
    )
    og_lamp = (
        "M31.3600 36.2400 H43.8400 A2.5600 2.5600 0 0 1 46.4000 38.8000 "
        "V55.5200 A2.5600 2.5600 0 0 1 43.8400 58.0800 H31.3600 A2.5600 2.5600 0 0 1 "
        "28.8000 55.5200 V38.8000 A2.5600 2.5600 0 0 1 31.3600 36.2400 Z"
    )

    def og_lockup(mark_fill: str, word_fill: str) -> str:
        return f'''  <g transform="translate(318 196) scale(2.58)">
    <path fill="{mark_fill}" d="{og_body}"/>
    <path fill="{LAMP}" d="{og_lamp}"/>
  </g>
  <text x="590" y="365" font-family="{face}"
        font-size="168" font-weight="600" letter-spacing="-0.04em" fill="{word_fill}">loft</text>
  <text x="596" y="418" font-family="{face}"
        font-size="28" font-weight="400" letter-spacing="-0.02em" fill="{MUTE}">{tag}</text>
'''

    write(
        SVG / "loft-og.svg",
        f'''<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1280 640" width="1280" height="640" fill="none">
  <!-- GitHub social preview 1280×640. Ink ground, lockup, lamp on. -->
  <rect width="1280" height="640" fill="{INK}"/>
  <radialGradient id="glow" cx="38%" cy="50%" r="42%">
    <stop offset="0%" stop-color="{LAMP}" stop-opacity="0.12"/>
    <stop offset="100%" stop-color="{INK}" stop-opacity="0"/>
  </radialGradient>
  <rect width="1280" height="640" fill="url(#glow)"/>
{og_lockup(SNOW, SNOW)}</svg>
''',
    )
    # README: same lockup, cropped to the mark+word+tagline, transparent.
    vb, rw, rh = "340 188 546 256", 546, 256
    write(
        SVG / "loft-lockup-readme.svg",
        f'''<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vb}" width="{rw}" height="{rh}" fill="none">
{og_lockup(INK, INK)}</svg>
''',
    )
    write(
        SVG / "loft-lockup-readme-on-dark.svg",
        f'''<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vb}" width="{rw}" height="{rh}" fill="none">
{og_lockup(SNOW, SNOW)}</svg>
''',
    )


# --- pixel-hinted 16×16 occupancy (1 = body, 2 = lamp) ---
# Designed on a 16 grid with 1px padding. Roof over the stem only.
PIX16 = [
    "................",
    ".......11.......",
    "......1111......",
    ".....111111.....",
    "....11222211....",
    "....11222211....",
    "....11222211....",
    "....11222211....",
    "....11111111....",
    "....11111111....",
    "....11111111111.",
    "....11111111111.",
    "....11111111111.",
    "................",
    "................",
    "................",
]


def paint_grid(grid, body_rgb, lamp_rgb, scale=1) -> Image.Image:
    """Rasterize an occupancy grid: 1 is body, 2 is lamp, '.' is empty."""
    n = len(grid)
    img = Image.new("RGBA", (n * scale, n * scale), (0, 0, 0, 0))
    px = img.load()
    for y, row in enumerate(grid):
        for x, ch in enumerate(row):
            if ch == ".":
                continue
            color = lamp_rgb if ch == "2" else body_rgb
            for dy in range(scale):
                for dx in range(scale):
                    px[x * scale + dx, y * scale + dy] = color
    return img


def hex_rgba(h, a=255):
    """Parse #RRGGBB into an RGBA tuple."""
    h = h.lstrip("#")
    r, g, b = int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16)
    return (r, g, b, a)


def outline_from_grid(grid) -> Image.Image:
    """Disconnected tray: copper outline of the L, empty window."""
    n = len(grid)
    body = {(x, y) for y, row in enumerate(grid) for x, ch in enumerate(row) if ch != "."}
    img = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    px = img.load()
    copper = hex_rgba(COPPER)
    for x, y in body:
        for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1)):
            nx, ny = x + dx, y + dy
            if (nx, ny) not in body:
                px[x, y] = copper
                break
        else:
            # interior of window stays empty; interior of body stays empty too (outline only)
            pass
    return img


def raster_supersampled(size: int, fill, lamp, hole=False, pad=0.07) -> Image.Image:
    """Draw the mark via polygon at 8× and downsample."""
    over = max(size * 8, 256)
    img = Image.new("RGBA", (over, over), (0, 0, 0, 0))
    dr = ImageDraw.Draw(img)
    p = pad * over
    box = (p, p * 0.7, over - p, over - p)
    x, y, r, b = box
    w, h = r - x, b - y
    pts = [(x + px * w, y + py * h) for px, py in MARK]
    dr.polygon(pts, fill=fill)
    wx, wy, ww, wh = WINDOW
    wr = [
        x + wx * w,
        y + wy * h,
        x + (wx + ww) * w,
        y + (wy + wh) * h,
    ]
    if hole:
        dr.rounded_rectangle(wr, radius=WINDOW_RX * min(w, h), fill=(0, 0, 0, 0))
    else:
        dr.rounded_rectangle(wr, radius=WINDOW_RX * min(w, h), fill=lamp)
    return img.resize((size, size), Image.Resampling.LANCZOS)


def chrome_shot(svg_path: Path, out: Path, w: int, h: int) -> None:
    """Render an SVG to PNG via headless Chrome screenshot."""
    html = f"""<!doctype html><meta charset="utf-8">
<style>
  html,body{{margin:0;padding:0;background:transparent;width:{w}px;height:{h}px;overflow:hidden}}
  svg{{display:block;width:{w}px;height:{h}px}}
</style>
{Path(svg_path).read_text().split('?>', 1)[-1]}
"""
    tmp = Path(tempfile.mkdtemp()) / "shot.html"
    tmp.write_text(html)
    out.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        [
            CHROME,
            "--headless=new",
            "--disable-gpu",
            "--hide-scrollbars",
            "--force-device-scale-factor=1",
            f"--window-size={w},{h}",
            "--default-background-color=00000000",
            f"--screenshot={out}",
            f"file://{tmp}",
        ],
        check=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )


def save(img: Image.Image, path: Path) -> None:
    """Save img as PNG, creating parent directories."""
    path.parent.mkdir(parents=True, exist_ok=True)
    img.save(path, "PNG")


def build_rasters() -> None:
    """Render PNG app icons, marks, Windows tray, and macOS menu bar assets."""
    # Chrome once at 1024 (filters + fonts), then downsample.
    chrome_shot(SVG / "loft-icon.svg", APP / "icon_1024.png", 1024, 1024)
    chrome_shot(SVG / "loft-icon-light.svg", APP / "icon-light_1024.png", 1024, 1024)
    icon = Image.open(APP / "icon_1024.png").convert("RGBA")
    icon_l = Image.open(APP / "icon-light_1024.png").convert("RGBA")
    for size in (512, 256, 128, 64, 32):
        icon.resize((size, size), Image.Resampling.LANCZOS).save(APP / f"icon_{size}.png")
        icon_l.resize((size, size), Image.Resampling.LANCZOS).save(APP / f"icon-light_{size}.png")

    chrome_shot(SVG / "loft-mark.svg", PNG / "loft-mark-1024.png", 1024, 1024)
    chrome_shot(SVG / "loft-mark-on-dark.svg", PNG / "loft-mark-on-dark-1024.png", 1024, 1024)
    chrome_shot(SVG / "loft-lockup.svg", PNG / "loft-lockup.png", 640, 384)
    chrome_shot(SVG / "loft-lockup-on-dark.svg", PNG / "loft-lockup-on-dark.png", 640, 384)
    chrome_shot(SVG / "loft-lockup-readme.svg", PNG / "loft-lockup-readme.png", 1092, 512)
    chrome_shot(SVG / "loft-lockup-readme-on-dark.svg", PNG / "loft-lockup-readme-on-dark.png", 1092, 512)
    chrome_shot(SVG / "loft-og.svg", PNG / "github-social-preview.png", 1280, 640)

    # Windows tray — pixel-hinted 16, scaled integer for 32; supersample for others
    on16 = paint_grid(PIX16, hex_rgba(COPPER), hex_rgba(LAMP), 1)
    off16 = paint_grid(PIX16, hex_rgba(COPPER), hex_rgba(COPPER), 1)  # lamp off
    save(on16, TRAY / "loft-tray-16.png")
    save(off16, TRAY / "loft-tray-off-16.png")
    save(paint_grid(PIX16, hex_rgba(COPPER), hex_rgba(LAMP), 2), TRAY / "loft-tray-32.png")

    for size in (20, 24, 48, 64):
        save(
            raster_supersampled(size, hex_rgba(COPPER), hex_rgba(LAMP)),
            TRAY / f"loft-tray-{size}.png",
        )
        save(
            raster_supersampled(size, hex_rgba(COPPER), hex_rgba(COPPER)),
            TRAY / f"loft-tray-off-{size}.png",
        )
    save(
        raster_supersampled(32, hex_rgba(COPPER), hex_rgba(COPPER)),
        TRAY / "loft-tray-off-32.png",
    )
    save(paint_grid(PIX16, hex_rgba(COPPER), hex_rgba(COPPER), 2), TRAY / "loft-tray-off-32.png")
    # light-taskbar variant: ink body
    save(paint_grid(PIX16, hex_rgba(INK), hex_rgba(LAMP), 1), TRAY / "loft-tray-16-on-light.png")
    save(paint_grid(PIX16, hex_rgba(SNOW), hex_rgba(LAMP), 1), TRAY / "loft-tray-16-on-dark.png")

    # macOS menu bar template — black + alpha, window is a hole
    for size in (18, 22, 36, 44, 54, 66):
        img = raster_supersampled(size, (0, 0, 0, 255), (0, 0, 0, 0), hole=True, pad=0.08)
        save(img, MENUBAR / f"loftTemplate-{size}.png")
        off = raster_supersampled(size, (0, 0, 0, 255), (0, 0, 0, 255), hole=False, pad=0.08)
        save(off, MENUBAR / f"loftTemplate-off-{size}.png")

    # Convenience names Apple expects
    shutil.copyfile(MENUBAR / "loftTemplate-18.png", MENUBAR / "loftTemplate.png")
    shutil.copyfile(MENUBAR / "loftTemplate-36.png", MENUBAR / "loftTemplate@2x.png")
    shutil.copyfile(MENUBAR / "loftTemplate-54.png", MENUBAR / "loftTemplate@3x.png")
    shutil.copyfile(MENUBAR / "loftTemplate-44.png", MENUBAR / "loftTemplate-22@2x.png")


def menubar_outline(size: int) -> Image.Image:
    """Stroke the L-roof silhouette in black for a menu-bar outline."""
    over = max(size * 8, 256)
    img = Image.new("RGBA", (over, over), (0, 0, 0, 0))
    dr = ImageDraw.Draw(img)
    p = 0.08 * over
    x, y, r, b = p, p * 0.7, over - p, over - p
    w, h = r - x, b - y
    pts = [(x + px * w, y + py * h) for px, py in MARK]
    stroke = max(over // 18, 8)
    dr.line(pts + [pts[0]], fill=(0, 0, 0, 255), width=stroke, joint="curve")
    # round caps at vertices
    rad = stroke / 2
    for px, py in pts:
        dr.ellipse((px - rad, py - rad, px + rad, py + rad), fill=(0, 0, 0, 255))
    return img.resize((size, size), Image.Resampling.LANCZOS)


def build_icns_ico() -> None:
    """Build loft.icns (iconutil) and loft.ico from the 1024 px app icon."""
    # iconset
    iconset = APP / "loft.iconset"
    if iconset.exists():
        shutil.rmtree(iconset)
    iconset.mkdir()
    mapping = {
        "icon_16x16.png": 32,          # we'll generate 16 from 32 downsample if missing
        "icon_16x16@2x.png": 32,
        "icon_32x32.png": 32,
        "icon_32x32@2x.png": 64,
        "icon_128x128.png": 128,
        "icon_128x128@2x.png": 256,
        "icon_256x256.png": 256,
        "icon_256x256@2x.png": 512,
        "icon_512x512.png": 512,
        "icon_512x512@2x.png": 1024,
    }
    src1024 = Image.open(APP / "icon_1024.png").convert("RGBA")
    # 16px from pixel-hinted mark on squircle
    icon16 = make_app_pixel(16)
    icon32 = Image.open(APP / "icon_32.png").convert("RGBA")
    for name, size in mapping.items():
        if size == 16:
            icon16.save(iconset / name)
        else:
            src1024.resize((size, size), Image.Resampling.LANCZOS).save(iconset / name)
    # proper 16
    icon16.save(iconset / "icon_16x16.png")
    subprocess.run(["iconutil", "-c", "icns", "-o", str(APP / "loft.icns"), str(iconset)], check=True)

    src1024.save(
        APP / "loft.ico",
        format="ICO",
        sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (256, 256)],
    )


def make_app_pixel(size: int) -> Image.Image:
    """Draw a squircle app icon; 16 px uses the pixel-hinted grid."""
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    dr = ImageDraw.Draw(img)
    ink = hex_rgba(INK)
    lamp = hex_rgba(LAMP)
    r = max(size * 22 // 96, 3)
    dr.rounded_rectangle((0, 0, size - 1, size - 1), radius=r, fill=ink)
    if size == 16:
        body = paint_grid(PIX16, hex_rgba(STONE), lamp, 1)
        img.alpha_composite(body)
        return img
    mark = raster_supersampled(int(size * 0.72), hex_rgba(STONE), lamp)
    ox = (size - mark.width) // 2
    oy = (size - mark.height) // 2 - size // 32
    img.alpha_composite(mark, (ox, oy))
    return img


def write_readme() -> None:
    """Write brand/README.md with palette, type, and file rules."""
    write(
        ROOT / "README.md",
        f"""# loft · brand

Марка протокола — **L-крыша с лампой**. Буква L (Loft) + чердак overlay над HTTPS + тёплое окно (туннель жив).

Solid fills only на марке и в трее. Лёгкое свечение окна — только на app icon.

## Palette

| Token | Hex | Use |
|-------|-----|-----|
| Ink | `{INK}` | фон app icon, марка на светлом |
| Stone | `{STONE}` | тело L на тёмном app icon |
| Copper | `{COPPER}` | Windows tray |
| Lamp | `{LAMP}` | окно, connected |
| Lamp hi | `{LAMP_HI}` | блик окна на app icon |
| Snow | `{SNOW}` | марка на тёмном, tray on dark |
| Paper | `{PAPER}` | поля, превью |
| Mute | `{MUTE}` | подписи |

## Type

Фирменный шрифт — **Avenir Next** (Linotype, Adrian Frutiger / Akira Kobayashi, 2004). Геометрический гуманистический гротеск: круглая «o» рифмуется с окном-лампой, ровная насыщенность — с L-крышей.

| Роль | Начертание | Правило |
|------|------------|---------|
| Wordmark / lockup | Demi 600 | lowercase `loft`, tracking −0.04em |
| Заголовки UI | Demi 600 | sentence case, tracking −0.02em |
| Кнопки, табы | Medium 500 | без капса, кроме коротких статусов |
| Текст, доки | Regular 400 | интерлиньяж 1.45 |
| Код, ключи `lt_…` | SF Mono / Menlo / Consolas | Avenir не ставим |

Не используем Ultra Light, Heavy и курсив в lockup.

Стек: `"Avenir Next", Avenir, "Helvetica Neue", "Segoe UI", ui-sans-serif, system-ui, sans-serif`.

На macOS шрифт системный, кириллица полная. На Windows для пиксель-идентичного wordmark — контуры в SVG или поставленный файл шрифта.

## Files

| File | Use |
|------|-----|
| `svg/loft-mark.svg` | протокол, README, сайт |
| `svg/loft-lockup.svg` | марка + слово *loft* |
| `png/type-specimen.png` | таблица фирменного шрифта |
| `svg/loft-icon.svg` | иконка приложения (тёмная) |
| `svg/loft-icon-light.svg` | иконка на светлом |
| `svg/loft-favicon.svg` | 32px |
| `svg/loft-menubar-template.svg` | macOS `NSStatusItem` template |
| `app/loft.icns` | macOS bundle |
| `app/loft.ico` | Windows app |
| `tray/loft-tray-16.png` | Windows notify icon, connected |
| `tray/loft-tray-off-16.png` | Windows notify icon, disconnected |
| `menubar/loftTemplate.png` (+ `@2x` `@3x`) | macOS menu bar, connected (окно-дырка) |
| `menubar/loftTemplate-off-*.png` | macOS menu bar, disconnected (без выреза окна) |

## Rules

1. Крыша покрывает только стойку L, «нога» торчит вправо — это L, не домик.
2. ≤16px: пиксельная сетка `PIX16` в `generate.py`, не даунскейл с 1024.
3. macOS menu bar — **template image** (чёрный + альфа). Цвет система красит сама.
4. Windows tray по умолчанию **copper + lamp** (видно на тёмном таскбаре Win11).
5. Connected = лампа горит. Disconnected = то же L, окно выключено (macOS: без выреза).
6. Слово *loft* — lowercase, Avenir Next Demi, tracking −0.04em. См. § Type.

Пересборка: `/tmp/loft-brand/bin/python generate.py`
""",
    )


def composite(src: Path, bg, out: Path) -> None:
    """Flatten src onto a solid background and save as RGB PNG."""
    im = Image.open(src).convert("RGBA")
    base = Image.new("RGBA", im.size, bg if isinstance(bg, tuple) else hex_rgba(bg))
    base.alpha_composite(im)
    save(base.convert("RGB"), out)


def write_previews() -> None:
    """Write preview composites of the mark, lockup, tray, and menu bar."""
    paper = hex_rgba(PAPER)
    dark = hex_rgba(INK)
    composite(PNG / "loft-mark-1024.png", paper, PNG / "preview-mark-on-paper.png")
    composite(PNG / "loft-mark-1024.png", dark, PNG / "preview-mark-on-ink.png")
    composite(PNG / "loft-lockup.png", paper, PNG / "preview-lockup-on-paper.png")
    for name in ("loft-tray-16.png", "loft-tray-off-16.png"):
        im = Image.open(TRAY / name).convert("RGBA")
        big = im.resize((256, 256), Image.Resampling.NEAREST)
        for bg, tag in ((paper, "paper"), ((28, 28, 28, 255), "taskbar")):
            base = Image.new("RGBA", (256, 256), bg)
            base.alpha_composite(big)
            save(base.convert("RGB"), PNG / f"preview-{Path(name).stem}-{tag}.png")
    for name in ("loftTemplate-22.png", "loftTemplate-off-22.png"):
        im = Image.open(MENUBAR / name).convert("RGBA")
        big = im.resize((264, 264), Image.Resampling.NEAREST)
        for bg, tag in (((232, 232, 234, 255), "mac-light"), ((44, 44, 46, 255), "mac-dark")):
            base = Image.new("RGBA", (264, 264), bg)
            # invert on dark to simulate template tint
            layer = big
            if tag == "mac-dark":
                r, g, b, a = layer.split()
                layer = Image.merge("RGBA", (r.point(lambda x: 255), g.point(lambda x: 255), b.point(lambda x: 255), a))
            base.alpha_composite(layer)
            save(base.convert("RGB"), PNG / f"preview-{Path(name).stem}-{tag}.png")


def main() -> None:
    """Generate the full brand kit into this directory."""
    write_svgs()
    build_rasters()
    build_icns_ico()
    write_previews()
    write_readme()
    print("brand kit →", ROOT)


if __name__ == "__main__":
    main()
