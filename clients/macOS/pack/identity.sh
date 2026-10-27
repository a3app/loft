# CODESIGN_ID: Developer ID when CODESIGN_IDENTITY is set, else ad-hoc "-".
# Sourced (not run) by sign.sh and make-dmg.sh so both pick the same signing
# identity. "Ad-hoc" signing needs no Apple certificate: the app runs on the
# build machine, but Gatekeeper warns on other Macs. A Developer ID
# certificate plus notarization (make-dmg.sh) is what ships to users.
#   CODESIGN_IDENTITY=-                         ad-hoc (default)
#   CODESIGN_IDENTITY=auto                      first "Developer ID Application" in the keychain
#   CODESIGN_IDENTITY="Developer ID Application: Name (TEAMID)"
#
# shellcheck disable=SC2034

if [ -z "${CODESIGN_IDENTITY:-}" ] || [ "${CODESIGN_IDENTITY}" = "-" ]; then
	CODESIGN_ID="-"
elif [ "${CODESIGN_IDENTITY}" = "auto" ]; then
	CODESIGN_ID=$(security find-identity -v -p codesigning 2>/dev/null \
		| sed -n 's/.*"\(Developer ID Application: .*\)"/\1/p' | head -1)
	if [ -z "${CODESIGN_ID}" ]; then
		echo "pack: no Developer ID Application in keychain; ad-hoc" >&2
		CODESIGN_ID="-"
	fi
else
	CODESIGN_ID="${CODESIGN_IDENTITY}"
fi
