#!/bin/sh
# loft-panel installer for Debian / Ubuntu. The panel comes first: when
# loft-node is missing, the copy bundled in node/ is installed for --host.
# On a server that already runs loft-node the panel goes on top of it.
#
# POSIX /bin/sh (dash on a fresh netinst). bash is not required.
#
#   ./install.sh --host panel.example.net
#   ./install.sh --dry-run
#   ./install.sh --unattended --host panel.example.net
#   ./install.sh --no-nginx          # bring your own reverse proxy
#   ./install.sh --no-node           # never install loft-node, require it
#   ./install.sh --uninstall         # remove services, keep users and data
#   ./install.sh --remove            # also delete panel users and data
#
# Re-running is the upgrade path: binaries, units and the nginx site are
# replaced; users.json, panel.env and node files are left alone.

set -eu

HERE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ORIG_ARGS="$*"
VERSION="dev"
if [ -f "$HERE/VERSION" ]; then
	VERSION=$(tr -d ' \n' < "$HERE/VERSION")
elif [ -f "$HERE/../../../VERSION" ]; then
	VERSION=$(tr -d ' \n' < "$HERE/../../../VERSION")
fi
PANEL_USER="loft-panel"
PANEL_HOME="/var/lib/loft-panel"
PANEL_ETC="/etc/loft-panel"
PANEL_ENV="/etc/loft-panel/panel.env"
BIN_DIR="/usr/local/bin"
UNIT_DIR="/etc/systemd/system"
NGINX_SITE="/etc/nginx/sites-available/loft-panel.conf"
NGINX_LINK="/etc/nginx/sites-enabled/loft-panel.conf"
NODE_CONF="/etc/loft/node.conf"
DECOY="127.0.0.1:8443"
LOG="/var/log/loft-panel-install.log"

DRY=0; UNATTENDED=0; UNINSTALL=0; PURGE=0; NO_NGINX=0; NO_NODE=0
HOST=""
LISTEN="127.0.0.1:8091"

while [ $# -gt 0 ]; do
	a=$1; shift
	case "$a" in
		--dry-run) DRY=1 ;;
		--unattended|-y) UNATTENDED=1 ;;
		--uninstall) UNINSTALL=1 ;;
		--remove) UNINSTALL=1; PURGE=1 ;;
		--no-nginx) NO_NGINX=1 ;;
		--no-node) NO_NODE=1 ;;
		--host|--listen)
			[ $# -gt 0 ] || { echo "$a needs a value" >&2; exit 2; }
			v=$1; shift
			case "$a" in
				--host) HOST=$v ;;
				--listen) LISTEN=$v ;;
			esac ;;
		--host=*) HOST="${a#--host=}" ;;
		--listen=*) LISTEN="${a#--listen=}" ;;
		--help|-h)
			awk 'NR==1{next} /^#/{sub(/^# ?/,""); print; next} {exit}' "$0"
			exit 0 ;;
		*) echo "unknown argument: $a (--help)" >&2; exit 2 ;;
	esac
done

if [ -n "$HOST" ]; then
	case "$HOST" in
		*[!A-Za-z0-9.-]*|.*|*.|*..*) echo "--host must be a DNS name, e.g. panel.example.net" >&2; exit 2 ;;
	esac
fi
case "$LISTEN" in
	127.0.0.1:[0-9]*|localhost:[0-9]*|\[::1\]:[0-9]*) ;;
	*) echo "--listen must be a loopback address: the panel is reached through nginx" >&2; exit 2 ;;
esac

# ── output ───────────────────────────────────────────────────────────────
if [ -t 1 ] && [ "${TERM:-dumb}" != "dumb" ]; then
	C_R=$(printf '\033[31m'); C_G=$(printf '\033[32m'); C_Y=$(printf '\033[33m')
	C_B=$(printf '\033[1m');  C_0=$(printf '\033[0m')
else
	C_R=''; C_G=''; C_Y=''; C_B=''; C_0=''
fi

if ! ( : >>"$LOG" ) 2>/dev/null; then LOG=/dev/null; fi
# log appends a line to the install log.
log()  { printf '%s\n' "$*" >>"$LOG" 2>/dev/null || true; }
# say prints a line to stdout and the log.
say()  { printf '%s\n' "$*"; log "$*"; }
# step prints a bold section heading.
step() { printf '\n%s%s%s\n' "$C_B" "$*" "$C_0"; log "== $* =="; }
# ok prints a green success line.
ok()   { printf '  %s*%s %s\n' "$C_G" "$C_0" "$*"; log "ok: $*"; }
# warn prints a yellow warning line.
warn() { printf '  %s!%s %s\n' "$C_Y" "$C_0" "$*"; log "warn: $*"; }
# die prints a red error and exits 1.
die()  { printf '\n  %s* %s%s\n\n' "$C_R" "$*" "$C_0" >&2; log "die: $*"; exit 1; }
# run executes a command, or prints it in --dry-run. Failure is fatal.
run()  {
	log "+ $*"
	if [ "$DRY" = 1 ]; then printf '  %s. %s%s\n' "$C_Y" "$*" "$C_0"; return 0; fi
	"$@" >>"$LOG" 2>&1 || die "command failed: $* (see $LOG)"
}
# try runs a command and warns on failure instead of exiting.
try() {
	log "+ $*"
	if [ "$DRY" = 1 ]; then printf '  %s. %s%s\n' "$C_Y" "$*" "$C_0"; return 0; fi
	if ! "$@" >>"$LOG" 2>&1; then
		warn "failed: $* (see $LOG)"
		return 1
	fi
	return 0
}
# ask prompts Y/n on a tty unless --unattended. Default is yes.
ask() {
	[ "$UNATTENDED" = 1 ] && return 0
	if ! ( true </dev/tty ) 2>/dev/null; then return 0; fi
	printf '  %s [Y/n] ' "$1"
	read -r a </dev/tty || return 0
	case "$a" in [nN]*) return 1 ;; *) return 0 ;; esac
}
# have reports whether a command is on PATH.
have() { command -v "$1" >/dev/null 2>&1; }
# confirm_code requires typing a random 6-digit code (used by --remove).
confirm_code() {
	code=$(od -An -N4 -tu4 /dev/urandom 2>/dev/null | tr -d ' \n')
	code=$(( ${code:-0} % 900000 + 100000 ))
	printf '\n  Type %s%s%s to confirm: ' "$C_B" "$code" "$C_0"
	read -r typed </dev/tty || return 1
	[ "$typed" = "$code" ]
}
# write_file path mode — body on stdin. Skipped in --dry-run.
write_file() {
	path=$1; mode=$2
	if [ "$DRY" = 1 ]; then
		printf '  %s. write %s (%s)%s\n' "$C_Y" "$path" "$mode" "$C_0"
		cat >/dev/null
		return 0
	fi
	tmp=$(mktemp)
	cat >"$tmp"
	chmod "$mode" "$tmp"
	mv "$tmp" "$path"
}
# unit_from src dest — copy a packaged unit, pointing /usr/bin at BIN_DIR.
unit_from() {
	sed -e "s|/usr/bin/loft-panel|$BIN_DIR/loft-panel|g" -e "s|-node-bin /usr/bin/loft-node|-node-bin $NODE_BIN|" "$1" | write_file "$2" 0644
}

# ── 0. root ──────────────────────────────────────────────────────────────
[ "$(id -u)" = 0 ] || die "root required: sudo sh $0"

printf '\n%sloft-panel %s%s\n' "$C_B" "$VERSION" "$C_0"
[ "$DRY" = 1 ] && say "dry run — nothing will be changed"
log "--- $(date -u +%FT%TZ) install.sh $VERSION args:$ORIG_ARGS ---"

# ── uninstall ────────────────────────────────────────────────────────────
if [ "$UNINSTALL" = 1 ]; then
	step "Remove loft-panel"
	if [ "$PURGE" = 1 ]; then
		say "  this deletes panel users and settings in $PANEL_HOME and $PANEL_ETC"
		say "  the node and its members are not touched"
		if [ "$UNATTENDED" != 1 ]; then
			( true </dev/tty ) 2>/dev/null || die "--remove needs a tty, or pass --unattended"
			confirm_code || die "confirmation mismatch"
		fi
	fi
	if have systemctl; then
		try systemctl disable --now loft-panel
		try systemctl disable --now loft-panel-adm
		try systemctl disable loft-panel-net
	fi
	try rm -f "$UNIT_DIR/loft-panel.service" "$UNIT_DIR/loft-panel-adm.service" "$UNIT_DIR/loft-panel-net.service"
	try rm -f "$BIN_DIR/loft-panel" "$BIN_DIR/loft-panel-adm"
	if [ -e "$NGINX_LINK" ] || [ -e "$NGINX_SITE" ]; then
		try rm -f "$NGINX_LINK" "$NGINX_SITE"
		have nginx && nginx -t >/dev/null 2>&1 && try systemctl reload nginx
	fi
	have systemctl && try systemctl daemon-reload
	if [ "$PURGE" = 1 ]; then
		try rm -rf "$PANEL_HOME" "$PANEL_ETC"
		if id "$PANEL_USER" >/dev/null 2>&1; then try deluser --system "$PANEL_USER"; fi
		ok "panel data removed"
	else
		ok "services removed; users and settings kept in $PANEL_HOME and $PANEL_ETC"
	fi
	say "  $NODE_CONF still sends non-loft TLS to $DECOY; remove decoy/decoy_proxy there if nginx goes too"
	exit 0
fi

# ── 1. requirements ──────────────────────────────────────────────────────
step "Requirements"
[ -r /etc/os-release ] || die "no /etc/os-release — distribution unknown"
. /etc/os-release
case "${ID:-}:${ID_LIKE:-}" in
	debian:*|ubuntu:*|*:*debian*|*:*ubuntu*) ok "${PRETTY_NAME:-$ID}" ;;
	*) die "Distribution «${ID:-?}» is not supported. Need Debian 12/13 or Ubuntu 22.04+." ;;
esac
have systemctl || die "no systemd"
have apt-get || die "no apt-get"
case "$(uname -m)" in x86_64|amd64|aarch64|arm64) ;; *) die "architecture $(uname -m) is not supported" ;; esac

# find_node sets NODE_BIN to an installed loft-node, or "".
find_node() {
	NODE_BIN=""
	for b in /usr/local/bin/loft-node /usr/bin/loft-node; do
		if [ -x "$b" ]; then NODE_BIN=$b; return 0; fi
	done
	return 1
}
NODE_UPGRADE=0
if find_node && [ -f "$NODE_CONF" ]; then
	ok "loft-node already installed: $NODE_BIN — the panel goes on top"
	NODE_FRESH=0
	# The bundled node is newer when its binary differs; a .deb node is apt's.
	if [ "$NO_NODE" = 0 ] && [ "$NODE_BIN" = /usr/local/bin/loft-node ] && [ -x "$HERE/node/loft-node" ] && ! cmp -s "$HERE/node/loft-node" "$NODE_BIN"; then
		NODE_UPGRADE=1
		ok "the bundled loft-node differs — it will be updated (configs stay)"
	fi
else
	[ "$NO_NODE" = 0 ] || die "loft-node is not installed and --no-node was given"
	[ -f "$HERE/node/install.sh" ] || die "loft-node is not installed and this bundle has no node/ — use the full loft-panel tarball"
	NODE_FRESH=1
	ok "loft-node is not installed — the bundled one will be installed"
fi

for b in loft-panel loft-panel-adm; do
	[ -x "$HERE/$b" ] || die "$b is not next to install.sh. Build: make dist-panel"
done
ok "binaries next to install.sh"

# ── 1b. node, when missing ───────────────────────────────────────────────
if [ "$NODE_FRESH" = 1 ] || [ "$NODE_UPGRADE" = 1 ]; then
	step "loft-node"
	set -- --unattended
	[ "$DRY" = 1 ] && set -- "$@" --dry-run
	# A fresh node is named --host; an update keeps node.conf (and its name).
	[ "$NODE_FRESH" = 1 ] && [ -n "$HOST" ] && set -- "$@" --advertise "https://$HOST"
	log "+ LOFT_VIA_PANEL=1 sh $HERE/node/install.sh $*"
	if ! LOFT_VIA_PANEL=1 sh "$HERE/node/install.sh" "$@"; then
		die "loft-node install failed (see /var/log/loft-install.log)"
	fi
	if [ "$DRY" = 0 ]; then
		find_node || die "loft-node install did not leave a binary"
		[ -f "$NODE_CONF" ] || die "loft-node install did not write $NODE_CONF"
		ok "loft-node: $NODE_BIN"
	else
		NODE_BIN=/usr/local/bin/loft-node
	fi
fi

if [ -z "$HOST" ] && [ -f "$NODE_CONF" ]; then
	HOST=$(awk '$1=="advertise"{print $2; exit}' "$NODE_CONF" 2>/dev/null | sed -e 's|^[a-zA-Z][a-zA-Z0-9+.-]*://||' -e 's|/.*||' -e 's|:.*||')
	case "$HOST" in
		*[a-zA-Z]*) ok "panel name from node advertise: $HOST" ;;
		*) HOST="" ;; # an IP: nginx needs a name
	esac
fi
if [ -z "$HOST" ]; then
	HOST=$(hostname -f 2>/dev/null || hostname)
	warn "no --host; using $HOST — pass --host panel.example.net for a real name"
fi

# ── 2. packages ──────────────────────────────────────────────────────────
step "Packages"
PKGS="adduser"
[ "$NO_NGINX" = 1 ] || PKGS="$PKGS nginx"
NEED=""
for p in $PKGS; do
	dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'ok installed' || NEED="$NEED $p"
done
if [ -n "$NEED" ]; then
	export DEBIAN_FRONTEND=noninteractive
	# A fresh server is often busy with unattended-upgrades: wait for the dpkg lock.
	APT_WAIT="-o DPkg::Lock::Timeout=600"
	# shellcheck disable=SC2086
	run apt-get $APT_WAIT update -y
	# shellcheck disable=SC2086
	run apt-get $APT_WAIT install -y --no-install-recommends $NEED
	ok "installed$NEED"
else
	ok "already present"
fi

# ── 3. user and directories ──────────────────────────────────────────────
step "User and directories"
if id "$PANEL_USER" >/dev/null 2>&1; then
	ok "user $PANEL_USER"
else
	run adduser --system --group --home "$PANEL_HOME" --no-create-home --shell /usr/sbin/nologin "$PANEL_USER"
	ok "created $PANEL_USER"
fi
if [ "$DRY" = 0 ]; then
	mkdir -p "$PANEL_HOME" "$PANEL_ETC"
	chown "$PANEL_USER":"$PANEL_USER" "$PANEL_HOME"
	chmod 0700 "$PANEL_HOME"
	chown root:"$PANEL_USER" "$PANEL_ETC"
	chmod 0750 "$PANEL_ETC"
fi
ok "$PANEL_HOME  $PANEL_ETC"
if [ -f "$PANEL_ENV" ]; then
	ok "$PANEL_ENV already present — leaving it"
else
	write_file "$PANEL_ENV" 0640 <<EOF
# loft-panel environment (systemd EnvironmentFile).
PANEL_LISTEN=$LISTEN
# Names the panel answers to; other Host headers get 421.
PANEL_HOSTS=$HOST
EOF
	[ "$DRY" = 1 ] || chown root:"$PANEL_USER" "$PANEL_ENV"
	ok "wrote $PANEL_ENV"
fi

# ── 4. binaries ──────────────────────────────────────────────────────────
step "Binaries"
if [ "$DRY" = 0 ]; then
	install -m 0755 "$HERE/loft-panel" "$BIN_DIR/loft-panel"
	install -m 0755 "$HERE/loft-panel-adm" "$BIN_DIR/loft-panel-adm"
fi
ok "$BIN_DIR/loft-panel  $BIN_DIR/loft-panel-adm"

# ── 5. node: send non-loft TLS to the panel's nginx ──────────────────────
step "Node"
NODE_RESTART=0
if [ "$NO_NGINX" = 1 ]; then
	ok "--no-nginx: $NODE_CONF left alone"
elif [ "$DRY" = 1 ]; then
	printf '  %s. %s wire-node -conf %s -decoy %s%s\n' "$C_Y" "$BIN_DIR/loft-panel-adm" "$NODE_CONF" "$DECOY" "$C_0"
else
	set +e
	note=$("$BIN_DIR/loft-panel-adm" wire-node -conf "$NODE_CONF" -decoy "$DECOY" 2>&1)
	rc=$?
	set -e
	log "wire-node rc=$rc: $note"
	case "$rc" in
		0) ok "$note" ;;
		10) ok "$NODE_CONF: $note (old file: $NODE_CONF.prev)"; NODE_RESTART=1 ;;
		*) die "$NODE_CONF: $note" ;;
	esac
	case "$note" in *"left as is"*|*YAML*) warn "the panel will see every client as 127.0.0.1 until decoy_proxy is on" ;; esac
fi

# The panel's name: the first of PANEL_HOSTS (the wizard may have moved it
# off the node's name), else the node's.
PANEL_NAME=$(sed -n 's/^PANEL_HOSTS=//p' "$PANEL_ENV" 2>/dev/null | cut -d, -f1 | tr -d ' ')
[ -n "$PANEL_NAME" ] || PANEL_NAME=$HOST

# ── 6. nginx ─────────────────────────────────────────────────────────────
if [ "$NO_NGINX" = 0 ]; then
	step "nginx"
	# The site comes from the helper's template (names: node.conf advertise, PANEL_HOSTS).
	if [ "$DRY" = 1 ]; then
		printf '  %s. %s nginx-site%s\n' "$C_Y" "$BIN_DIR/loft-panel-adm" "$C_0"
	elif ! "$BIN_DIR/loft-panel-adm" nginx-site >>"$LOG" 2>&1; then
		die "the panel's nginx site did not pass nginx -t (see $LOG); it was left out"
	fi
	run systemctl enable nginx
	run systemctl reload-or-restart nginx
	ok "https://$PANEL_NAME/ → $LISTEN"
fi

# ── 6b. firewall: Let's Encrypt checks the name on :80 ───────────────────
if [ "$NO_NGINX" = 0 ]; then
	if have ufw && ufw status 2>/dev/null | grep -q 'Status: active'; then
		try ufw allow 80/tcp && ok "ufw allowed 80/tcp (Let's Encrypt)"
	else
		ok "no active ufw — keep 80/tcp open in front of this host for Let's Encrypt"
	fi
fi

# ── 7. systemd ───────────────────────────────────────────────────────────
step "systemd"
unit_from "$HERE/loft-panel-adm.service" "$UNIT_DIR/loft-panel-adm.service"
unit_from "$HERE/loft-panel.service" "$UNIT_DIR/loft-panel.service"
unit_from "$HERE/loft-panel-net.service" "$UNIT_DIR/loft-panel-net.service"
run systemctl daemon-reload
run systemctl enable loft-panel-adm loft-panel loft-panel-net
run systemctl restart loft-panel-adm
run systemctl restart loft-panel
if [ "$NODE_RESTART" = 1 ]; then
	run systemctl restart loft-node
fi
if [ "$DRY" = 0 ]; then
	sleep 2
	for u in loft-panel-adm loft-panel loft-node; do
		if systemctl is-active --quiet "$u"; then ok "$u is active"; else warn "$u is not active — journalctl -u $u"; fi
	done
fi

# ── 8. done ──────────────────────────────────────────────────────────────
step "Done"
CODE=""
if [ "$DRY" = 0 ] && [ -f "$PANEL_HOME/setup.code" ]; then
	CODE=$(tr -d ' \n' < "$PANEL_HOME/setup.code")
fi
say ""
say "  panel       https://$PANEL_NAME/"
if [ -n "$CODE" ]; then
	say "  setup code  ${C_B}$CODE${C_0}   (first owner; also in $PANEL_HOME/setup.code)"
else
	say "  sign in     with an existing panel user; reset: sudo loft-panel passwd LOGIN"
fi
say "  logs        journalctl -u loft-panel -u loft-panel-adm -f"
if [ -n "$CODE" ]; then
	say "  next        the wizard asks for the server names and issues Let's Encrypt certificates"
fi
say ""
