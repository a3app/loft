#!/bin/sh
# Build loft-node_VERSION_ARCH.deb from a Linux loft-node binary.
# Prefer dpkg-deb; fall back to ar(1) so a Mac can still emit a .deb.
set -eu

HERE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$HERE/../.." && pwd)
VERSION=$(tr -d ' \n' < "$ROOT/VERSION")
ARCH=${ARCH:-amd64}
BIN=${BIN:-}

case "$ARCH" in
	amd64|arm64) ;;
	*) echo "ARCH must be amd64 or arm64" >&2; exit 2 ;;
esac

if [ -z "$BIN" ]; then
	for c in \
		"$ROOT/dist/node/linux-$ARCH/loft-node-$VERSION-linux-$ARCH/loft-node" \
		"$ROOT/dist/node/linux-$ARCH/loft-node" \
		"$ROOT/dist/loft-node-$VERSION-linux-$ARCH/loft-node" \
		"$ROOT/dist/linux-$ARCH/loft-node" \
		"$ROOT/loft-node"
	do
		if [ -f "$c" ]; then BIN=$c; break; fi
	done
fi
[ -n "$BIN" ] && [ -f "$BIN" ] || {
	echo "no loft-node binary. Build: make dist-debian" >&2
	exit 1
}

STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT

pkg=$STAGING/pkg
mkdir -p \
	"$pkg/DEBIAN" \
	"$pkg/usr/bin" \
	"$pkg/usr/share/loft-node" \
	"$pkg/usr/share/doc/loft-node" \
	"$pkg/lib/systemd/system" \
	"$pkg/etc/sysctl.d"

install -m 0755 "$BIN" "$pkg/usr/bin/loft-node"
install -m 0644 "$HERE/loft-node.service" "$pkg/lib/systemd/system/loft-node.service"
install -m 0644 "$HERE/sysctl.d/99-loft.conf" "$pkg/etc/sysctl.d/99-loft.conf"
install -m 0644 "$HERE/configs/controller.conf" "$pkg/usr/share/loft-node/controller.conf"
install -m 0644 "$HERE/configs/relay.conf" "$pkg/usr/share/loft-node/relay.conf"
install -m 0644 "$HERE/configs/network.conf" "$pkg/usr/share/loft-node/network.conf"
install -m 0644 "$HERE/README.md" "$pkg/usr/share/doc/loft-node/README.md"
install -m 0755 "$HERE/deb/postinst" "$pkg/DEBIAN/postinst"
install -m 0755 "$HERE/deb/prerm" "$pkg/DEBIAN/prerm"
install -m 0755 "$HERE/deb/postrm" "$pkg/DEBIAN/postrm"

sed -e "s/@VERSION@/$VERSION/g" -e "s/@ARCH@/$ARCH/g" \
	"$HERE/deb/control" > "$pkg/DEBIAN/control"
chmod 0644 "$pkg/DEBIAN/control"

# Installed-Size in KiB.
size=$(du -sk "$pkg" | awk '{print $1}')
printf 'Installed-Size: %s\n' "$size" >> "$pkg/DEBIAN/control"

OUTDIR=${OUTDIR:-"$ROOT/dist/node/linux-$ARCH"}
mkdir -p "$OUTDIR"
DEB="$OUTDIR/loft-node_${VERSION}_${ARCH}.deb"

if command -v dpkg-deb >/dev/null 2>&1; then
	dpkg-deb --root-owner-group -b "$pkg" "$DEB"
else
	# macOS ar(1) writes Mach-O archives. Emit a System V .deb in Python.
	work=$STAGING/ar
	mkdir -p "$work"
	printf '2.0\n' > "$work/debian-binary"
	# mktar dest [tar-args] — gzip tar owned by root, no macOS xattrs.
	mktar() {
		dest=$1; shift
		export COPYFILE_DISABLE=1
		if tar --uid 0 --gid 0 --numeric-owner --no-xattrs -czf "$dest" "$@" 2>/dev/null; then
			return 0
		fi
		tar -czf "$dest" "$@"
	}
	(CDPATH= cd -- "$pkg/DEBIAN" && mktar "$work/control.tar.gz" .)
	(CDPATH= cd -- "$pkg" && mktar "$work/data.tar.gz" --exclude DEBIAN .)
	python3 - "$DEB" "$work/debian-binary" "$work/control.tar.gz" "$work/data.tar.gz" <<'PY'
import sys
out, *files = sys.argv[1:]
names = ["debian-binary", "control.tar.gz", "data.tar.gz"]
with open(out, "wb") as f:
    f.write(b"!<arch>\n")
    for name, path in zip(names, files):
        data = open(path, "rb").read()
        header = (
            name.encode().ljust(16)
            + b"0".ljust(12)
            + b"0".ljust(6)
            + b"0".ljust(6)
            + b"100644".ljust(8)
            + str(len(data)).encode().ljust(10)
            + b"`\n"
        )
        f.write(header)
        f.write(data)
        if len(data) % 2:
            f.write(b"\n")
PY
fi

echo "$DEB"
