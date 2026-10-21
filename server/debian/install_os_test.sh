#!/bin/sh
# Check how the installer classifies the host OS.
#
# The test runs the real install.sh fragment, cut by section headings.
set -eu

HERE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
SRC="$HERE/install.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

sed -n '/^# ── 1\. operating system/,/^# ── 3\. requirements/p' "$SRC" \
	| grep -v '^\. /etc/os-release$' \
	| grep -v '^\[ -r /etc/os-release \]' \
	| sed '$d' > "$TMP/block.sh"

[ -s "$TMP/block.sh" ] || { echo "failed to cut a section from install.sh" >&2; exit 1; }
grep -q 'OS_MAJOR' "$TMP/block.sh" || { echo "cut the wrong block" >&2; exit 1; }

PASS=0
FAIL=0

# run want desc — source the cut OS block and check accept/ask/refuse.
run() {
	want=$1; desc=$2
	out=$(sh -c '
		set -eu
		die()  { printf "DIE %s\n" "$*"; exit 3; }
		warn() { printf "WARN %s\n" "$*"; }
		ok()   { printf "OK %s\n" "$*"; }
		say()  { :; }
		step() { :; }
		log()  { :; }
		ask()  { printf "ASK\n"; return 0; }
		have() { command -v "$1" >/dev/null 2>&1; }
		. "$0"
	' "$TMP/block.sh" 2>&1) && code=0 || code=$?

	got=refuse
	case "$code" in
		0) case "$out" in
			*ASK*) got=ask ;;
			*) got=accept ;;
		   esac ;;
		3) got=refuse ;;
		*) got="error($code)" ;;
	esac

	if [ "$got" = "$want" ]; then
		PASS=$((PASS + 1))
		printf '  ok    %-42s → %s\n' "$desc" "$got"
	else
		FAIL=$((FAIL + 1))
		printf '  FAIL  %-42s → %s, wanted %s\n' "$desc" "$got" "$want"
		printf '        %s\n' "$out"
	fi
}

echo
echo "Distribution"

export ID=debian VERSION_ID=12 PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"; unset ID_LIKE
run accept "Debian 12"
export ID=debian VERSION_ID=13 PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
run accept "Debian 13"
export ID=ubuntu VERSION_ID=22.04 PRETTY_NAME="Ubuntu 22.04.5 LTS"
run accept "Ubuntu 22.04"
export ID=ubuntu VERSION_ID=24.04 PRETTY_NAME="Ubuntu 24.04.4 LTS"
run accept "Ubuntu 24.04"
export ID=ubuntu VERSION_ID=26.04 PRETTY_NAME="Ubuntu 26.04.1 LTS"
run accept "Ubuntu 26.04"

export ID=debian VERSION_ID=11 PRETTY_NAME="Debian 11"
run refuse "Debian 11 out of support"
export ID=ubuntu VERSION_ID=20.04 PRETTY_NAME="Ubuntu 20.04"
run refuse "Ubuntu 20.04 out of support"

export ID=debian VERSION_ID=14 PRETTY_NAME="Debian 14"
run ask "Debian 14 untested"
export ID=ubuntu VERSION_ID=25.10 PRETTY_NAME="Ubuntu 25.10"
run ask "Ubuntu 25.10 not LTS"
export ID=debian PRETTY_NAME="Debian GNU/Linux trixie/sid"; unset VERSION_ID
run ask "Debian sid without VERSION_ID"

export ID=raspbian VERSION_ID=13 ID_LIKE=debian PRETTY_NAME="Raspberry Pi OS"
run ask "Raspberry Pi OS (ID_LIKE=debian)"
export ID=linuxmint VERSION_ID=22 ID_LIKE="ubuntu debian" PRETTY_NAME="Linux Mint 22"
run ask "Linux Mint (ID_LIKE=ubuntu debian)"

export ID=fedora VERSION_ID=42 PRETTY_NAME="Fedora 42"; unset ID_LIKE
run refuse "Fedora"
export ID=alpine VERSION_ID=3.21 PRETTY_NAME="Alpine 3.21"
run refuse "Alpine"
export ID=arch PRETTY_NAME="Arch Linux"; unset VERSION_ID
run refuse "Arch"
export ID=centos VERSION_ID=9 ID_LIKE="rhel fedora" PRETTY_NAME="CentOS Stream 9"
run refuse "CentOS (ID_LIKE=rhel fedora)"

echo
echo "Architecture"
export ID=debian VERSION_ID=13 PRETTY_NAME="Debian 13"; unset ID_LIKE

# arch_case kernel dpkg — stub uname and optional dpkg on PATH.
arch_case() {
	rm -f "$TMP/bin/uname" "$TMP/bin/dpkg"
	mkdir -p "$TMP/bin"
	printf '#!/bin/sh\necho %s\n' "$1" > "$TMP/bin/uname"
	chmod 0755 "$TMP/bin/uname"
	if [ -n "$2" ]; then
		printf '#!/bin/sh\necho %s\n' "$2" > "$TMP/bin/dpkg"
		chmod 0755 "$TMP/bin/dpkg"
	fi
	PATH="$TMP/bin:$PATH"; export PATH
}

OLDPATH=$PATH
arch_case x86_64 amd64;   run accept "x86_64 + dpkg amd64"; PATH=$OLDPATH
arch_case aarch64 arm64;  run accept "aarch64 + dpkg arm64"; PATH=$OLDPATH
arch_case x86_64 "";      run accept "x86_64 without dpkg"; PATH=$OLDPATH
arch_case riscv64 "";     run refuse "riscv64 unsupported"; PATH=$OLDPATH
arch_case aarch64 armhf;  run refuse "aarch64 kernel, armhf packages"; PATH=$OLDPATH
arch_case aarch64 amd64;  run accept "aarch64 kernel, amd64 packages"; PATH=$OLDPATH

echo
if [ "$FAIL" -gt 0 ]; then
	printf 'failed %d of %d\n\n' "$FAIL" "$((PASS + FAIL))"
	exit 1
fi
printf 'all %d checks passed\n\n' "$PASS"
