#!/bin/sh
# Build a UDZO disk image: loft.app, Applications symlink, README.txt.
# Volume layout (Finder): app left, Applications right — drag to install.
set -eu

PACK=$(cd "$(dirname "$0")" && pwd)
MACOS=$(cd "$PACK/.." && pwd)
ROOT=$(cd "$MACOS/../.." && pwd)
APP="$ROOT/dist/clients/macOS/loft.app"
OUTDIR="$ROOT/dist/clients/macOS"
VER=$(tr -d ' \n' < "$MACOS/VERSION")
ARCH=${ARCH:-$(uname -m)}
case "$ARCH" in
arm64|x86_64) ;;
*)
	echo "dmg: ARCH must be arm64 or x86_64 (got $ARCH)" >&2
	exit 1
	;;
esac
VOL=loft
DMG="$OUTDIR/loft-${VER}-macos-${ARCH}.dmg"
RW="$OUTDIR/loft-${VER}-macos-${ARCH}.rw.dmg"

# shellcheck disable=SC1091
. "$PACK/identity.sh"

if [ ! -d "$APP/Contents/MacOS" ]; then
	echo "dmg: build the app first (make app)" >&2
	exit 1
fi

"$PACK/sign.sh" "$APP"

STAGE=$(mktemp -d "${TMPDIR:-/tmp}/loft-dmg.XXXXXX")
cleanup() {
	if [ -n "${MOUNT:-}" ] && [ -d "$MOUNT" ]; then
		hdiutil detach "$MOUNT" -quiet 2>/dev/null || true
	fi
	rm -rf "$STAGE"
	rm -f "$RW"
}
trap cleanup EXIT

ditto "$APP" "$STAGE/loft.app"
ln -s /Applications "$STAGE/Applications"
cp "$PACK/README.txt" "$STAGE/README.txt"
if [ -f "$MACOS/Resources/loft.icns" ]; then
	cp "$MACOS/Resources/loft.icns" "$STAGE/.VolumeIcon.icns"
fi

rm -f "$DMG" "$RW"
hdiutil create -ov -volname "$VOL" -fs HFS+ -srcfolder "$STAGE" -format UDRW "$RW" >/dev/null

hdiutil detach "/Volumes/$VOL" -quiet 2>/dev/null || true
# CI has no Finder session; osascript would hang the runner.
if [ "${CI:-}" = "true" ]; then
	ATTACH=$(hdiutil attach -readwrite -noverify -noautoopen -nobrowse "$RW")
else
	ATTACH=$(hdiutil attach -readwrite -noverify -noautoopen "$RW")
fi
MOUNT=$(printf '%s\n' "$ATTACH" | grep -o '/Volumes/.*' | tail -1)
if [ -z "$MOUNT" ] || [ ! -d "$MOUNT" ]; then
	echo "dmg: failed to mount $RW" >&2
	exit 1
fi

if [ -f "$MOUNT/.VolumeIcon.icns" ]; then
	SetFile -a C "$MOUNT" 2>/dev/null || true
fi
# Hide README from the icon grid if Finder layout runs; keep the file visible.
# Users still see README.txt in the window.

if [ "${CI:-}" != "true" ] && command -v osascript >/dev/null 2>&1; then
	osascript <<EOF || true
tell application "Finder"
	tell disk "$VOL"
		open
		set current view of container window to icon view
		set toolbar visible of container window to false
		set statusbar visible of container window to false
		set bounds of container window to {280, 160, 920, 560}
		set theView to icon view options of container window
		set arrangement of theView to not arranged
		set icon size of theView to 96
		set text size of theView to 12
		delay 0.4
		set position of item "loft.app" of container window to {160, 180}
		set position of item "Applications" of container window to {480, 180}
		set position of item "README.txt" of container window to {320, 340}
		close
		open
		update without registering applications
		delay 1
		close
	end tell
end tell
EOF
fi

sync
hdiutil detach "$MOUNT" -quiet
MOUNT=""
hdiutil convert "$RW" -format UDZO -imagekey zlib-level=9 -o "$DMG" >/dev/null
rm -f "$RW"

if [ "$CODESIGN_ID" != "-" ]; then
	codesign --force --sign "$CODESIGN_ID" --timestamp "$DMG"
	echo "dmg: signed $DMG"
	if [ -n "${NOTARY_PROFILE:-}" ]; then
		xcrun notarytool submit "$DMG" --keychain-profile "$NOTARY_PROFILE" --wait
		xcrun stapler staple "$DMG"
		echo "dmg: notarized $DMG"
	fi
fi

echo "dmg: $DMG"
