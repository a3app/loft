#!/usr/bin/env python3
"""Write the three PNGs an MSIX VisualElements block requires. Ink, copper, lamp."""

import struct
import sys
import zlib
from pathlib import Path


def chunk(tag: bytes, data: bytes) -> bytes:
    return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)


def png(w: int, h: int) -> bytes:
    ink = (0x1A, 0x17, 0x14)
    copper = (0xC6, 0x7B, 0x3A)
    lamp = (0xE8, 0xB8, 0x4A)
    raw = bytearray()
    m = min(w, h)
    bx0 = (w - m) // 2 + m // 6
    by0 = (h - m) // 2 + m // 6
    bw, bh = m * 2 // 3, m * 2 // 3
    lx0, ly0 = bx0 + bw // 5, by0 + bh // 4
    lw, lh = bw * 3 // 5, bh // 3
    for y in range(h):
        raw.append(0)
        for x in range(w):
            if lx0 <= x < lx0 + lw and ly0 <= y < ly0 + lh:
                raw.extend(lamp)
            elif bx0 <= x < bx0 + bw and by0 <= y < by0 + bh:
                raw.extend(copper)
            else:
                raw.extend(ink)
    ihdr = struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(bytes(raw), 9)) + chunk(b"IEND", b"")


def main() -> None:
    out = Path(sys.argv[1])
    out.mkdir(parents=True, exist_ok=True)
    (out / "Square44x44Logo.png").write_bytes(png(44, 44))
    (out / "Square150x150Logo.png").write_bytes(png(150, 150))
    (out / "StoreLogo.png").write_bytes(png(50, 50))


if __name__ == "__main__":
    main()
