#!/bin/sh
# loft-node installer for Debian.
#
# POSIX /bin/sh (dash on a fresh netinst). bash is not required.
#
#   ./install.sh
#   ./install.sh --dry-run
#   ./install.sh --unattended
#   ./install.sh --role relay --controller wss://10.0.0.2/api/v1/sync --id ru1 \
#       --relay-key lt_… --controller-pub <64 hex>   (both from the controller's panel)
#   ./install.sh --uninstall
#   ./install.sh --remove
#
# Re-running is the upgrade path: the binary is replaced, existing token,
# certificates, and config are left alone.

set -eu

HERE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ORIG_ARGS="$*"
VERSION="1.0.0"
if [ -f "$HERE/VERSION" ]; then
	VERSION=$(tr -d ' \n' < "$HERE/VERSION")
elif [ -f "$HERE/../../VERSION" ]; then
	VERSION=$(tr -d ' \n' < "$HERE/../../VERSION")
fi
LOFT_USER="loft"
LOFT_HOME="/var/lib/loft"
LOFT_ETC="/etc/loft"
LOFT_TLS="/etc/loft/tls"
LOFT_BIN="/usr/local/bin/loft-node"
LOFT_ENV="/etc/loft/loft.env"
LOFT_YAML="/etc/loft/node.conf"
LOFT_UNIT="/etc/systemd/system/loft-node.service"
SYSCTL_D="/etc/sysctl.d/99-loft.conf"
LOG="/var/log/loft-install.log"

DRY=0; UNATTENDED=0; UNINSTALL=0; PURGE=0
# loft-panel installs the node with LOFT_VIA_PANEL=1: its wizard then issues
# the certificate and creates the first member, so the installer does neither.
VIA_PANEL=${LOFT_VIA_PANEL:-0}
NO_FIREWALL=0; NO_SYSCTL=0; MASK=0
ROLE="controller"
LISTEN=":443"
ADVERTISE=""
CONTROLLER=""
RELAY_ID=""
RELAY_KEY=""
CTL_PUB=""
DECOY=""
CERT=""
KEY=""
PEERS="hub"
TOKEN=""

while [ $# -gt 0 ]; do
	a=$1; shift
	case "$a" in
		--dry-run) DRY=1 ;;
		--unattended|-y) UNATTENDED=1 ;;
		--uninstall) UNINSTALL=1 ;;
		--remove) UNINSTALL=1; PURGE=1 ;;
		--no-firewall) NO_FIREWALL=1 ;;
		--no-sysctl) NO_SYSCTL=1 ;;
		--mask) MASK=1 ;;
		--role|--listen|--advertise|--controller|--id|--relay-key|--controller-pub|--decoy|--cert|--key|--peers|--token)
			[ $# -gt 0 ] || { echo "$a needs a value" >&2; exit 2; }
			v=$1; shift
			case "$a" in
				--role) ROLE=$v ;;
				--listen) LISTEN=$v ;;
				--advertise) ADVERTISE=$v ;;
				--controller) CONTROLLER=$v ;;
				--id) RELAY_ID=$v ;;
				--relay-key) RELAY_KEY=$v ;;
				--controller-pub) CTL_PUB=$v ;;
				--decoy) DECOY=$v ;;
				--cert) CERT=$v ;;
				--key) KEY=$v ;;
				--peers) PEERS=$v ;;
				--token) TOKEN=$v ;;
			esac ;;
		--role=*) ROLE="${a#--role=}" ;;
		--listen=*) LISTEN="${a#--listen=}" ;;
		--advertise=*) ADVERTISE="${a#--advertise=}" ;;
		--controller=*) CONTROLLER="${a#--controller=}" ;;
		--id=*) RELAY_ID="${a#--id=}" ;;
		--relay-key=*) RELAY_KEY="${a#--relay-key=}" ;;
		--controller-pub=*) CTL_PUB="${a#--controller-pub=}" ;;
		--decoy=*) DECOY="${a#--decoy=}" ;;
		--cert=*) CERT="${a#--cert=}" ;;
		--key=*) KEY="${a#--key=}" ;;
		--peers=*) PEERS="${a#--peers=}" ;;
		--token=*) TOKEN="${a#--token=}" ;;
		--help|-h)
			awk 'NR==1{next} /^#/{sub(/^# ?/,""); print; next} {exit}' "$0"
			exit 0 ;;
		*) echo "unknown argument: $a (--help)" >&2; exit 2 ;;
	esac
done

case "$ROLE" in controller|relay) ;; *) echo "role must be controller or relay" >&2; exit 2 ;; esac
case "$PEERS" in hub|mesh|vlan) ;; *) echo "peers must be hub, mesh, or vlan" >&2; exit 2 ;; esac

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

# ── 0. root ──────────────────────────────────────────────────────────────
[ "$(id -u)" = 0 ] || die "root required: sudo sh $0"

printf '\n%sloft-node %s%s\n' "$C_B" "$VERSION" "$C_0"
[ "$DRY" = 1 ] && say "dry run — nothing will be changed"
log "--- $(date -u +%FT%TZ) install.sh $VERSION args:$ORIG_ARGS ---"

# ── 1. operating system ──────────────────────────────────────────────────
step "Operating system"
[ -r /etc/os-release ] || die "no /etc/os-release — distribution unknown"
. /etc/os-release
OS_ID="${ID:-}"; OS_VER="${VERSION_ID:-}"; OS_NAME="${PRETTY_NAME:-$OS_ID $OS_VER}"
OS_MAJOR="${OS_VER%%.*}"
say "  $OS_NAME"

case "$OS_ID" in
	debian)
		case "$OS_MAJOR" in
			12|13) ok "supported" ;;
			10|11) die "Debian $OS_MAJOR is out of support. Need Debian 12 or 13." ;;
			*) warn "Debian $OS_MAJOR is untested — install will continue without a guarantee"
			   ask "Continue?" || exit 1 ;;
		esac ;;
	ubuntu)
		case "$OS_VER" in
			22.04|24.04|26.04) ok "supported" ;;
			16.04|18.04|20.04) die "Ubuntu $OS_VER is out of support. Need 22.04 or newer." ;;
			*) warn "Ubuntu $OS_VER is untested — install will continue without a guarantee"
			   ask "Continue?" || exit 1 ;;
		esac ;;
	*)
		case " ${ID_LIKE:-} " in
			*" debian "*|*" ubuntu "*)
				warn "«${OS_NAME}» is a Debian derivative; this combination was not tested"
				ask "Continue?" || exit 1 ;;
			*)
				die "Distribution «${OS_ID}» is not supported. Need Debian 12/13 or Ubuntu 22.04/24.04/26.04." ;;
		esac ;;
esac

# ── 2. architecture ──────────────────────────────────────────────────────
step "Architecture"
M="$(uname -m)"
case "$M" in
	x86_64|amd64) ARCH=amd64 ;;
	aarch64|arm64) ARCH=arm64 ;;
	*) die "architecture $M is not supported (need x86_64 or arm64)" ;;
esac

if have dpkg; then
	DPKG_ARCH="$(dpkg --print-architecture 2>/dev/null || true)"
	case "$DPKG_ARCH" in
		amd64|arm64)
			if [ "$DPKG_ARCH" != "$ARCH" ]; then
				warn "kernel $M, packages built for $DPKG_ARCH — using $DPKG_ARCH"
				ARCH="$DPKG_ARCH"
			fi ;;
		"") ;;
		*) die "system is $DPKG_ARCH — loft-node is built for amd64 and arm64" ;;
	esac
fi
ok "$M → $ARCH"

# ── 3. requirements ──────────────────────────────────────────────────────
if [ "$UNINSTALL" = 0 ]; then

step "Requirements"

have systemctl || die "no systemd — loft-node is a system service"
systemctl is-system-running >/dev/null 2>&1 || warn "systemd is in a degraded state; continuing"
ok "systemd"

have apt-get || die "no apt-get"
ok "apt"

RAM_MB=$(awk '/MemTotal/{printf "%d", $2/1024}' /proc/meminfo 2>/dev/null || echo 0)
if [ "$RAM_MB" -lt 128 ]; then
	die "RAM ${RAM_MB} MB is too small. Need 256 MB or more."
elif [ "$RAM_MB" -lt 256 ]; then
	warn "RAM ${RAM_MB} MB — the node will start; leave headroom for TLS"
else
	ok "memory: ${RAM_MB} MB"
fi

DISK_MB=$(df -Pm / | awk 'NR==2{print $4}')
if [ "${DISK_MB:-0}" -lt 80 ]; then
	die "free disk ${DISK_MB} MB — need about 100 MB"
else
	ok "disk: ${DISK_MB} MB free"
fi

PORT="${LISTEN##*:}"
[ -n "$PORT" ] || PORT=443
if have ss; then
	if ss -lnt "( sport = :$PORT )" 2>/dev/null | grep -q LISTEN; then
		if systemctl is-active --quiet loft-node 2>/dev/null; then
			ok "port $PORT is loft-node — upgrade"
		else
			warn "TCP port $PORT is already in use"
			ask "Continue anyway?" || exit 1
		fi
	else
		ok "TCP $PORT free"
	fi
fi

fi # UNINSTALL=0 requirements

# ── uninstall ────────────────────────────────────────────────────────────
if [ "$UNINSTALL" = 1 ]; then
	step "Remove loft-node"
	if [ "$PURGE" = 1 ]; then
		say "  this deletes token, seed, certificates, and YAML"
		if [ "$UNATTENDED" != 1 ]; then
			if ! ( true </dev/tty ) 2>/dev/null; then
				die "--remove needs a tty, or pass --unattended"
			fi
			confirm_code || die "confirmation mismatch"
		fi
	fi
	if have systemctl; then
		try systemctl stop loft-node
		try systemctl disable loft-node
	fi
	try rm -f "$LOFT_UNIT"
	try rm -f "$LOFT_BIN"
	try rm -f "$SYSCTL_D"
	if have systemctl; then
		try systemctl daemon-reload
	fi
	if [ "$PURGE" = 1 ]; then
		try rm -rf "$LOFT_HOME" "$LOFT_ETC"
		if id "$LOFT_USER" >/dev/null 2>&1; then
			try deluser --system "$LOFT_USER"
		fi
		ok "data removed"
	else
		ok "service removed; data kept in $LOFT_HOME and $LOFT_ETC"
	fi
	exit 0
fi

# ── 4. packages ──────────────────────────────────────────────────────────
step "Packages"
PKGS="ca-certificates openssl curl iproute2 adduser"
NEED=""
for p in $PKGS; do
	if dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'ok installed'; then
		:
	else
		NEED="$NEED $p"
	fi
done
if [ -n "$NEED" ]; then
	export DEBIAN_FRONTEND=noninteractive
	run apt-get update -y
	# shellcheck disable=SC2086
	run apt-get install -y --no-install-recommends $NEED
	ok "installed$NEED"
else
	ok "already present"
fi

# ── 5. user and directories ──────────────────────────────────────────────
step "User and directories"
if id "$LOFT_USER" >/dev/null 2>&1; then
	ok "user $LOFT_USER"
else
	run adduser --system --group --home "$LOFT_HOME" --no-create-home --shell /usr/sbin/nologin "$LOFT_USER"
	ok "created $LOFT_USER"
fi
if [ "$DRY" = 0 ]; then
	mkdir -p "$LOFT_ETC" "$LOFT_TLS" "$LOFT_HOME"
	chown root:"$LOFT_USER" "$LOFT_ETC" "$LOFT_TLS"
	chmod 0750 "$LOFT_ETC" "$LOFT_TLS"
	chown "$LOFT_USER":"$LOFT_USER" "$LOFT_HOME"
	chmod 0750 "$LOFT_HOME"
fi
ok "$LOFT_ETC  $LOFT_HOME"

# ── 6. binary ────────────────────────────────────────────────────────────
step "Binary"
BIN_SRC=""
if [ -x "$HERE/loft-node" ]; then
	BIN_SRC="$HERE/loft-node"
elif [ -x "$HERE/../../loft-node" ]; then
	BIN_SRC="$HERE/../../loft-node"
fi
[ -n "$BIN_SRC" ] || die "loft-node is not next to install.sh. Build a dist tarball: make dist-debian"
if [ "$DRY" = 0 ]; then
	install -m 0755 "$BIN_SRC" "$LOFT_BIN"
fi
ok "$LOFT_BIN"
if [ "$DRY" = 0 ] && [ -x "$LOFT_BIN" ]; then
	ok "$($LOFT_BIN --version 2>/dev/null || echo loft-node)"
fi

# ── 7. token ─────────────────────────────────────────────────────────────
step "Token"
if [ -f "$LOFT_ENV" ]; then
	ok "$LOFT_ENV already present — leaving it"
	TOKEN=$(awk -F= '/^LOFT_TOKEN=/{print substr($0,12); exit}' "$LOFT_ENV" 2>/dev/null || true)
else
	if [ -z "$TOKEN" ]; then
		TOKEN="lt_$(openssl rand -hex 24)"
	fi
	write_file "$LOFT_ENV" 0640 <<EOF
# loft-node environment. systemd EnvironmentFile.
LOFT_TOKEN=$TOKEN
EOF
	if [ "$DRY" = 0 ]; then
		chown root:"$LOFT_USER" "$LOFT_ENV"
	fi
	ok "wrote $LOFT_ENV"
fi

# ── 8. TLS ───────────────────────────────────────────────────────────────
step "TLS"
if [ -n "$CERT" ] && [ -n "$KEY" ]; then
	[ -f "$CERT" ] || die "cert not found: $CERT"
	[ -f "$KEY" ] || die "key not found: $KEY"
	if [ "$DRY" = 0 ]; then
		cp "$CERT" "$LOFT_TLS/fullchain.pem"
		cp "$KEY" "$LOFT_TLS/privkey.pem"
		chown root:"$LOFT_USER" "$LOFT_TLS/fullchain.pem" "$LOFT_TLS/privkey.pem"
		chmod 0640 "$LOFT_TLS/fullchain.pem" "$LOFT_TLS/privkey.pem"
	fi
	ok "installed operator certificate"
elif [ -f "$LOFT_TLS/fullchain.pem" ] && [ -f "$LOFT_TLS/privkey.pem" ]; then
	ok "certificate already present"
else
	CN="localhost"
	SAN="DNS:localhost"
	if [ -n "$ADVERTISE" ]; then
		CN=$(printf '%s' "$ADVERTISE" | sed -e 's|^[a-zA-Z][a-zA-Z0-9+.-]*://||' -e 's|/.*||' -e 's|:.*||')
	fi
	if [ -z "$CN" ] || [ "$CN" = "controller.example" ]; then
		CN=$(hostname -f 2>/dev/null || hostname)
	fi
	case "$CN" in
		*[a-zA-Z]*) SAN="DNS:$CN" ;;
		*) SAN="IP:$CN" ;;
	esac
	if [ "$DRY" = 0 ]; then
		openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
			-keyout "$LOFT_TLS/privkey.pem" \
			-out "$LOFT_TLS/fullchain.pem" \
			-subj "/CN=$CN" \
			-addext "subjectAltName=$SAN" >>"$LOG" 2>&1 \
			|| die "openssl failed to mint a certificate"
		chown root:"$LOFT_USER" "$LOFT_TLS/fullchain.pem" "$LOFT_TLS/privkey.pem"
		chmod 0640 "$LOFT_TLS/fullchain.pem" "$LOFT_TLS/privkey.pem"
	fi
	if [ "$VIA_PANEL" = 1 ]; then
		ok "self-signed certificate for $CN for now — the panel wizard issues a public one"
	else
		warn "self-signed certificate for $CN — replace with a public one for production"
	fi
fi

# ── 9. advertise / public name ───────────────────────────────────────────
step "Public name"
if [ -z "$ADVERTISE" ]; then
	HOST=""
	if have curl; then
		for u in https://ifconfig.me https://icanhazip.com https://api.ipify.org; do
			ip=$(curl -4 -fsS --max-time 3 "$u" 2>/dev/null | tr -d '\r\n ' || true)
			case "$ip" in
				[0-9]*.[0-9]*.[0-9]*.[0-9]*) HOST=$ip; break ;;
			esac
		done
	fi
	[ -n "$HOST" ] || HOST=$(hostname -f 2>/dev/null || hostname)
	ADVERTISE="https://$HOST"
fi
ok "advertise $ADVERTISE"

if [ "$ROLE" = "relay" ]; then
	[ -n "$CONTROLLER" ] || die "relay needs --controller=wss://host/api/v1/sync"
	[ -n "$RELAY_ID" ] || RELAY_ID=$(hostname -s 2>/dev/null || echo relay)
	# The controller issues one key per relay and signs this relay's settings
	# with its own key; both come from the controller's panel (Relays).
	[ -n "$RELAY_KEY" ] || [ -f "$LOFT_HOME/relay.key" ] || die "relay needs --relay-key=lt_… from the controller's panel"
	[ -n "$CTL_PUB" ] || die "relay needs --controller-pub=<64 hex> from the controller's panel"
	ok "relay id=$RELAY_ID controller=$CONTROLLER"
fi

# ── 10. Config ───────────────────────────────────────────────────────────
step "Configuration"
NET="$LOFT_HOME/network.conf"
KEYS="$LOFT_HOME/members.keys"
OP_KEY=""
if [ -f "$KEYS" ]; then
	OP_KEY=$(awk -F': *' '/^operator:/{print $2; exit}' "$KEYS" 2>/dev/null || true)
fi
if [ -f "$NET" ] || [ -f "$LOFT_HOME/network.yaml" ]; then
	ok "$NET already present — leaving it"
elif [ "$VIA_PANEL" = 1 ]; then
	# No members yet: the panel wizard creates the first one.
	write_file "$KEYS" 0600 </dev/null
	[ "$DRY" = 1 ] || chown "$LOFT_USER":"$LOFT_USER" "$KEYS"
	write_file "$NET" 0640 <<EOF
network loft
name loft
enable
ipif overlay 10.8.0.0/24
  gateway 10.8.0.1
  mtu 1280
  peers $PEERS
  p2p disable
  listen 0.0.0.0 ${PORT} front ws
EOF
	[ "$DRY" = 1 ] || chown "$LOFT_USER":"$LOFT_USER" "$NET"
	ok "wrote $NET (no members: the panel adds them)"
else
	# Per-member default: operator lt_ in members.keys. LOFT_TOKEN stays the node secret.
	OP_KEY="lt_$(openssl rand -hex 24)"
	OP_SHA=$(printf '%s' "$OP_KEY" | openssl dgst -sha256 | awk '{print $NF}')
	write_file "$KEYS" 0600 <<EOF
operator: $OP_KEY
EOF
	if [ "$DRY" = 0 ]; then
		chown "$LOFT_USER":"$LOFT_USER" "$KEYS"
	fi
	write_file "$NET" 0640 <<EOF
network loft
name loft
enable
ipif overlay 10.8.0.0/24
  gateway 10.8.0.1
  mtu 1280
  peers $PEERS
  p2p disable
  listen 0.0.0.0 ${PORT} front ws
create member operator
config member operator key_sha256 $OP_SHA
config member operator multi enable 8
enable member operator
EOF
	if [ "$DRY" = 0 ]; then
		chown "$LOFT_USER":"$LOFT_USER" "$NET"
	fi
	ok "wrote $NET and $KEYS"
fi

if [ "$ROLE" = "relay" ] && [ -n "$RELAY_KEY" ]; then
	printf '%s\n' "$RELAY_KEY" | write_file "$LOFT_HOME/relay.key" 0600
	[ "$DRY" = 1 ] || chown "$LOFT_USER":"$LOFT_USER" "$LOFT_HOME/relay.key"
	ok "wrote $LOFT_HOME/relay.key"
fi

if [ -f "$LOFT_YAML" ] || [ -f "/etc/loft/node.yaml" ]; then
	ok "$LOFT_YAML already present — leaving it"
else
	DECOY_BLOCK=""
	MASK_LINE="mask disable"
	if [ -n "$DECOY" ]; then
		DECOY_BLOCK=$(printf 'decoy %s\n' "$DECOY")
		if [ "$MASK" = 1 ]; then MASK_LINE="mask enable"; fi
	fi
	if [ "$ROLE" = "relay" ]; then
		write_file "$LOFT_YAML" 0640 <<EOF
role relay
id $RELAY_ID
listen $LISTEN
advertise $ADVERTISE
controller $CONTROLLER
controller_pub $CTL_PUB
cert $LOFT_TLS/fullchain.pem
key $LOFT_TLS/privkey.pem
${DECOY_BLOCK}${MASK_LINE}
fronts get
EOF
	else
		write_file "$LOFT_YAML" 0640 <<EOF
role controller
listen $LISTEN
cert $LOFT_TLS/fullchain.pem
key $LOFT_TLS/privkey.pem
fronts get
get_path /static/v3/build
ws_path /api/v1/sync
h2_path /api/v1/tunnel
h3_path /api/v1/tunnel
advertise $ADVERTISE
roster_path /static/v3/manifest.json
lists_path /static/v3/lists.json
auth shared disable
${DECOY_BLOCK}${MASK_LINE}
canary fat_hello enable
canary split_hello disable
canary pad 0
EOF
	fi
	if [ "$DRY" = 0 ]; then
		chown root:"$LOFT_USER" "$LOFT_YAML"
	fi
	ok "wrote $LOFT_YAML"
fi

# ── 11. sysctl ───────────────────────────────────────────────────────────
if [ "$NO_SYSCTL" = 0 ]; then
	step "Sysctl"
	write_file "$SYSCTL_D" 0644 <<EOF
# UDP buffers for QUIC / HTTP/3 on :443.
net.core.rmem_max = 7500000
net.core.wmem_max = 7500000
EOF
	if [ "$DRY" = 0 ]; then
		try sysctl -p "$SYSCTL_D" || try sysctl --system
	fi
	ok "$SYSCTL_D"
fi

# ── 12. systemd ──────────────────────────────────────────────────────────
step "systemd"
UNIT_BIN="$LOFT_BIN"
write_file "$LOFT_UNIT" 0644 <<EOF
[Unit]
Description=loft overlay node
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
User=$LOFT_USER
Group=$LOFT_USER
EnvironmentFile=-$LOFT_ENV
ExecStart=$UNIT_BIN -c $LOFT_YAML -dir $LOFT_HOME
ExecReload=/bin/kill -HUP \$MAINPID
Restart=on-failure
RestartSec=3
TimeoutStopSec=20
LogRateLimitIntervalSec=30s
LogRateLimitBurst=200
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
LimitNOFILE=65536
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$LOFT_HOME
ProtectKernelTunables=true
ProtectControlGroups=true
ProtectClock=true
RestrictSUIDSGID=true
LockPersonality=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
EOF
run systemctl daemon-reload
run systemctl enable loft-node
run systemctl restart loft-node
if [ "$DRY" = 0 ]; then
	sleep 1
	if systemctl is-active --quiet loft-node; then
		ok "loft-node is active"
	else
		warn "loft-node did not stay up — journalctl -u loft-node"
	fi
fi

# ── 13. firewall ─────────────────────────────────────────────────────────
if [ "$NO_FIREWALL" = 0 ]; then
	step "Firewall"
	if have ufw && ufw status 2>/dev/null | grep -q 'Status: active'; then
		try ufw allow "$PORT"/tcp
		try ufw allow "$PORT"/udp
		ok "ufw allowed $PORT/tcp and $PORT/udp"
	else
		ok "no active ufw — open $PORT/tcp and $PORT/udp on the path in front of this host"
	fi
fi

# ── 14. client snippet ───────────────────────────────────────────────────
step "Done"
PUB=""
if [ "$DRY" = 0 ] && [ "$ROLE" = "controller" ]; then
	TOKEN_VAL=$(awk -F= '/^LOFT_TOKEN=/{print substr($0,12); exit}' "$LOFT_ENV" 2>/dev/null || true)
	if [ -z "$TOKEN_VAL" ]; then TOKEN_VAL=$TOKEN; fi
	if have runuser; then
		PUB=$(runuser -u "$LOFT_USER" -- "$LOFT_BIN" -dir "$LOFT_HOME" -print-pub 2>/dev/null || true)
	else
		PUB=$(su -s /bin/sh "$LOFT_USER" -c "'$LOFT_BIN' -dir '$LOFT_HOME' -print-pub" 2>/dev/null || true)
	fi
	TOKEN=$TOKEN_VAL
fi
HOST=$(printf '%s' "$ADVERTISE" | sed -e 's|^[a-zA-Z][a-zA-Z0-9+.-]*://||' -e 's|/.*||')
CLIENT="/root/loft-client.yaml"
CLIENT_TOKEN="$TOKEN"
CLIENT_MEMBER=""
if [ -n "$OP_KEY" ]; then
	CLIENT_TOKEN=$OP_KEY
	CLIENT_MEMBER="member_id: operator"
fi
if [ "$DRY" = 0 ] && [ "$ROLE" = "controller" ] && [ ! -f "$CLIENT" ] && [ "$VIA_PANEL" = 0 ]; then
	umask 077
	cat >"$CLIENT" <<EOF
listen:
  socks5: 127.0.0.1:1080
$CLIENT_MEMBER
token: "$CLIENT_TOKEN"
select: auto
bootstrap: $ADVERTISE/static/v3/manifest.json
controller_pub: $PUB
outbounds:
  - name: node-get
    type: get
    url: $ADVERTISE
    path: /static/js
    fingerprint: chrome
    insecure_skip_verify: true
EOF
	chmod 0600 "$CLIENT"
	ok "wrote $CLIENT"
fi

say ""
say "  role        $ROLE"
say "  listen      $LISTEN"
say "  advertise   $ADVERTISE"
if [ -n "$PUB" ]; then
	say "  controller_pub  $PUB"
fi
say "  config      $LOFT_YAML"
say "  data        $LOFT_HOME"
say "  logs        journalctl -u loft-node -f"
if [ "$ROLE" = "controller" ] && [ "$VIA_PANEL" = 0 ]; then
	say "  client      $CLIENT"
	say "  Replace insecure_skip_verify once the certificate is public."
fi
say ""
