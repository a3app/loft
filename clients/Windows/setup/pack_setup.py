#!/usr/bin/env python3
"""Append a stored zip of <payload-dir> to the setup stub.

Usage: pack_setup.py <payload-dir> <stub.exe> <out.exe>
"""

# Part of the Windows installer build (see the Makefile "release" targets).
# The Makefile compiles setup/loft-setup.c into a stub exe; this script turns it
# into the final loft-windows-<arch>-setup.exe. Resulting layout:
#   [stub exe][zip of payload-dir, stored, no compression][zip size: 8 bytes LE]["LOFTPK01"]
# loft-setup.c reads the footer from the end of its own file to find the zip.
# The zip is stored (not compressed) because the installer's small zip reader
# only handles stored entries.

import os
import pathlib
import subprocess
import sys


# main zips the payload folder with `zip -0` (no compression, no extra file
# attributes, no macOS ._ files thanks to COPYFILE_DISABLE), then writes
# stub + zip + footer to the output path and prints its size.
def main() -> None:
    payload_dir, stub, out = sys.argv[1:]
    root = pathlib.Path(payload_dir)
    z = (root.parent / (root.name + ".stored.zip")).resolve()
    if z.exists():
        z.unlink()
    env = os.environ.copy()
    env["COPYFILE_DISABLE"] = "1"
    subprocess.check_call(["zip", "-0", "-X", "-r", str(z), "."], cwd=root, env=env)
    payload = z.read_bytes()
    blob = pathlib.Path(stub).read_bytes() + payload + len(payload).to_bytes(8, "little") + b"LOFTPK01"
    pathlib.Path(out).parent.mkdir(parents=True, exist_ok=True)
    pathlib.Path(out).write_bytes(blob)
    print(out, len(blob))


if __name__ == "__main__":
    main()
