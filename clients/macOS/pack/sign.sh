#!/bin/sh
# Sign loft.app inside-out. Ad-hoc unless CODESIGN_IDENTITY is a Developer ID.
# Usage: pack/sign.sh path/to/loft.app
set -eu

APP=${1:?usage: sign.sh loft.app}
PACK=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
. "$PACK/identity.sh"
ENT="$PACK/entitlements.plist"

if [ ! -d "$APP/Contents/MacOS" ]; then
	echo "sign: not an app bundle: $APP" >&2
	exit 1
fi

extra="--entitlements $ENT"
if [ "$CODESIGN_ID" != "-" ]; then
	extra="$extra --options runtime --timestamp"
	echo "sign: $CODESIGN_ID"
else
	echo "sign: ad-hoc (-)"
fi

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
codesign --force --sign "$CODESIGN_ID" --identifier app.loft.macos $extra "$APP"
codesign --verify --strict "$APP"
echo "sign: ok $APP"
