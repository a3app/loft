#!/bin/sh
# Sign Loft.app inside-out. Ad-hoc unless CODESIGN_IDENTITY is a Developer ID.
# Usage: pack/sign.sh path/to/Loft.app
# Called by make-dmg.sh before packing. Code must be signed from the inside
# out: a bundle's signature covers the already-signed files inside it, so
# signing an inner file afterwards would break the outer signature. With a
# Developer ID it also turns on the hardened runtime and a secure timestamp,
# which Apple's notarization requires.
set -eu

APP=${1:?usage: sign.sh Loft.app}
PACK=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
. "$PACK/identity.sh"
ENT="$PACK/entitlements.plist"

if [ ! -d "$APP/Contents/MacOS" ]; then
	echo "sign: not an app bundle: $APP" >&2
	exit 1
fi

# SetFile -a B writes com.apple.FinderInfo. codesign --force treats it as
# detritus ("resource fork, Finder information, or similar detritus not
# allowed") on the inner loft binary. Strip, sign, then SetFile again.
xattr -cr "$APP"

extra="--entitlements $ENT"
if [ "$CODESIGN_ID" != "-" ]; then
	extra="$extra --options runtime --timestamp"
	echo "sign: $CODESIGN_ID"
else
	echo "sign: ad-hoc (-)"
fi

# sign ID PATH: sign one executable with a fixed bundle identifier, so the
# identity macOS sees for loft-priv / loft-front stays stable across builds.
sign() {
	id=$1
	path=$2
	# shellcheck disable=SC2086
	codesign --force --sign "$CODESIGN_ID" --identifier "$id" $extra "$path"
}

# Nested first: Swift dylibs, then helpers, then the UI binary, then the bundle.
if [ -d "$APP/Contents/Frameworks" ]; then
	find "$APP/Contents/Frameworks" -type f \( -name '*.dylib' -o -name '*.so' \) -print \
		| while IFS= read -r f; do
			# shellcheck disable=SC2086
			codesign --force --sign "$CODESIGN_ID" $extra "$f"
		done
fi

if [ -x "$APP/Contents/MacOS/loft-priv" ]; then
	sign app.loft.macos.priv "$APP/Contents/MacOS/loft-priv"
fi
if [ -x "$APP/Contents/MacOS/loft-front" ]; then
	sign app.loft.macos.front "$APP/Contents/MacOS/loft-front"
fi
sign app.loft.macos "$APP/Contents/MacOS/loft"
# shellcheck disable=SC2086
# Finally the bundle itself, then a strict check that everything verifies.
codesign --force --sign "$CODESIGN_ID" --identifier app.loft.macos $extra "$APP"
codesign --verify --strict "$APP"
echo "sign: ok $APP"
