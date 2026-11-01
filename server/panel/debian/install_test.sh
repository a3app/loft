#!/bin/sh
# Argument checks of the panel installer; they run before the root check.
set -eu
HERE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
S="$HERE/install.sh"
FAIL=0
# expect code desc args… — run install.sh and compare the exit code.
expect() {
	want=$1; desc=$2; shift 2
	if sh "$S" "$@" >/dev/null 2>&1; then got=0; else got=$?; fi
	if [ "$got" = "$want" ]; then echo "ok   $desc"; else echo "FAIL $desc: exit $got, want $want"; FAIL=1; fi
}
expect 0 "help" --help
expect 2 "unknown flag" --frobnicate
expect 2 "host with a slash" --host=panel.example.net/x
expect 2 "host with a leading dot" --host .example.net
expect 2 "host with double dots" --host=a..b
expect 2 "public listen" --listen 0.0.0.0:8091
expect 2 "missing value" --host
sh -n "$S"
for f in "$HERE"/deb/postinst "$HERE"/deb/prerm "$HERE"/deb/postrm "$HERE"/build-deb.sh; do sh -n "$f"; done
echo "syntax ok"
exit $FAIL
