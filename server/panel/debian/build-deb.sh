#!/bin/sh
# Build loft-panel_VERSION_ARCH.deb from Linux loft-panel and loft-panel-adm.
# Prefer dpkg-deb; without it (a Mac) the ar archive is written in Python.
set -eu

HERE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$HERE/../../.." && pwd)
VERSION=$(tr -d ' \n' < "$ROOT/VERSION")
ARCH=${ARCH:-amd64}
BINDIR=${BINDIR:-"$ROOT/dist/panel/linux-$ARCH/loft-panel-$VERSION-linux-$ARCH"}

case "$ARCH" in
	amd64|arm64) ;;
	*) echo "ARCH must be amd64 or arm64" >&2; exit 2 ;;
esac
for b in loft-panel loft-panel-adm; do
	[ -f "$BINDIR/$b" ] || { echo "no $BINDIR/$b. Build: make dist-panel" >&2; exit 1; }
done

STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT
pkg=$STAGING/pkg
mkdir -p "$pkg/DEBIAN" "$pkg/usr/bin" "$pkg/usr/share/loft-panel" "$pkg/usr/share/doc/loft-panel" "$pkg/lib/systemd/system"

install -m 0755 "$BINDIR/loft-panel" "$pkg/usr/bin/loft-panel"
install -m 0755 "$BINDIR/loft-panel-adm" "$pkg/usr/bin/loft-panel-adm"
install -m 0644 "$HERE/loft-panel.service" "$pkg/lib/systemd/system/loft-panel.service"
install -m 0644 "$HERE/loft-panel-adm.service" "$pkg/lib/systemd/system/loft-panel-adm.service"
install -m 0644 "$HERE/loft-panel-net.service" "$pkg/lib/systemd/system/loft-panel-net.service"
install -m 0644 "$HERE/../README.md" "$pkg/usr/share/doc/loft-panel/README.md"
for s in postinst prerm postrm; do
	install -m 0755 "$HERE/deb/$s" "$pkg/DEBIAN/$s"
done
sed -e "s/@VERSION@/$VERSION/g" -e "s/@ARCH@/$ARCH/g" "$HERE/deb/control" > "$pkg/DEBIAN/control"
chmod 0644 "$pkg/DEBIAN/control"
printf 'Installed-Size: %s\n' "$(du -sk "$pkg" | awk '{print $1}')" >> "$pkg/DEBIAN/control"

OUTDIR=${OUTDIR:-"$ROOT/dist/panel/linux-$ARCH"}
mkdir -p "$OUTDIR"
DEB="$OUTDIR/loft-panel_${VERSION}_${ARCH}.deb"

if command -v dpkg-deb >/dev/null 2>&1; then
	dpkg-deb --root-owner-group -b "$pkg" "$DEB" >/dev/null
else
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
        f.write(name.encode().ljust(16) + b"0".ljust(12) + b"0".ljust(6) + b"0".ljust(6)
                + b"100644".ljust(8) + str(len(data)).encode().ljust(10) + b"`\n")
        f.write(data)
        if len(data) % 2:
            f.write(b"\n")
PY
fi
echo "$DEB"
