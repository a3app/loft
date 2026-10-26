#!/usr/bin/env python3
"""Append a stored zip of <payload-dir> to the setup stub.

Usage: pack_setup.py <payload-dir> <stub.exe> <out.exe>
"""

import os
import pathlib
import subprocess
import sys


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
