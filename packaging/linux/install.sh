#!/usr/bin/env bash
# SHELFCODE installer for Linux servers (systemd).
#
#   curl -fsSL https://github.com/ABelly99/Shelf-Code/releases/latest/download/install.sh | sudo bash
#
# Options (after "bash -s --" when piping, e.g. "| sudo bash -s -- --port 9000 --pin 1234"):
#   --port N          web port (default 8765, or the current one when upgrading)
#   --pin PIN         require this PIN on every device
#   --version vX.Y.Z  install a specific release instead of the latest
#   --open-firewall   open the port in ufw / firewalld if one is active
#   --uninstall       remove SHELFCODE (keeps your data and settings)
#   --purge           remove SHELFCODE and delete its data, settings and user
#
# Running it again upgrades SHELFCODE and keeps your data and settings.

set -euo pipefail

REPO="ABelly99/Shelf-Code"
BIN=/usr/local/bin/shelfcode
UNIT=/etc/systemd/system/shelfcode.service
CONF_DIR=/etc/shelfcode
CONF=$CONF_DIR/shelfcode.env
DATA=/var/lib/shelfcode
SVC_USER=shelfcode

PORT=""
PIN=""
VERSION="latest"
OPEN_FW=no
ACTION=install

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
die()  { printf '\033[31mError:\033[0m %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT="${2:-}"; shift 2 ;;
    --port=*) PORT="${1#*=}"; shift ;;
    --pin) PIN="${2:-}"; shift 2 ;;
    --pin=*) PIN="${1#*=}"; shift ;;
    --version) VERSION="${2:-}"; shift 2 ;;
    --version=*) VERSION="${1#*=}"; shift ;;
    --open-firewall) OPEN_FW=yes; shift ;;
    --uninstall) ACTION=uninstall; shift ;;
    --purge) ACTION=purge; shift ;;
    -h|--help) sed -n '2,17p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "please run as root, e.g.:  curl -fsSL …/install.sh | sudo bash"
[ -d /run/systemd/system ] || die "this installer needs systemd. Without systemd, use the Docker image or run 'shelfcode --headless' yourself (see the README)."

if command -v dpkg-query >/dev/null 2>&1 && dpkg-query -W -f='${Status}' shelfcode 2>/dev/null | grep -q "install ok"; then
  die "SHELFCODE is installed as a .deb package. Upgrade with:  sudo apt install ./shelfcode_<version>_<arch>.deb"
fi
if command -v rpm >/dev/null 2>&1 && rpm -q shelfcode >/dev/null 2>&1; then
  die "SHELFCODE is installed as an .rpm package. Upgrade with:  sudo dnf install --nogpgcheck ./shelfcode-<version>.<arch>.rpm"
fi

# ---------------------------------------------------------------- uninstall
if [ "$ACTION" != install ]; then
  bold "Removing SHELFCODE"
  systemctl disable --now shelfcode.service >/dev/null 2>&1 || true
  rm -f "$UNIT" "$BIN"
  systemctl daemon-reload
  ok "service and program removed"
  if [ "$ACTION" = purge ]; then
    rm -rf "$DATA" "$CONF_DIR"
    userdel "$SVC_USER" >/dev/null 2>&1 || true
    groupdel "$SVC_USER" >/dev/null 2>&1 || true
    ok "data ($DATA), settings ($CONF_DIR) and user '$SVC_USER' deleted"
  else
    ok "kept your data in $DATA and settings in $CONF_DIR (use --purge to delete them)"
  fi
  exit 0
fi

# ---------------------------------------------------------------- install / upgrade
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  armv7l|armv7*|armhf) ARCH=armv7 ;;
  *) die "unsupported CPU: $(uname -m). Supported: x86_64, aarch64, armv7." ;;
esac

if [ -f "$CONF" ] && [ -z "$PORT" ]; then
  PORT=$(sed -n 's/^SHELFCODE_PORT=//p' "$CONF" | tail -n1)
fi
PORT=${PORT:-8765}
case "$PORT" in ''|*[!0-9]*) die "--port must be a number" ;; esac

UPGRADE=no
[ -x "$BIN" ] && UPGRADE=yes
if [ "$UPGRADE" = yes ]; then bold "Upgrading SHELFCODE ($("$BIN" version 2>/dev/null || echo unknown))"; else bold "Installing SHELFCODE"; fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
HERE=""
# Only when run as a file (not "curl … | bash"): look for the program next to this script.
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fi

fetch() { # url dest
  if command -v curl >/dev/null 2>&1; then curl -fsSL --retry 3 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then wget -q -O "$2" "$1"
  else die "please install curl or wget"; fi
}

if [ -n "$HERE" ] && [ -x "$HERE/shelfcode" ] && [ "$VERSION" = latest ]; then
  # Running from an extracted release archive: use the files next to this script.
  SRC="$HERE"
  ok "using the files in $HERE"
else
  if [ "$VERSION" = latest ]; then BASE="https://github.com/$REPO/releases/latest/download"
  else BASE="https://github.com/$REPO/releases/download/$VERSION"; fi
  NAME="shelfcode-linux-$ARCH.tar.gz"
  fetch "$BASE/$NAME" "$TMP/$NAME" || die "download failed: $BASE/$NAME"
  if fetch "$BASE/checksums.txt" "$TMP/checksums.txt" 2>/dev/null && command -v sha256sum >/dev/null 2>&1; then
    (cd "$TMP" && grep " $NAME\$" checksums.txt | sha256sum -c --status) || die "checksum mismatch for $NAME"
    ok "downloaded and verified $NAME"
  else
    ok "downloaded $NAME"
  fi
  tar -xzf "$TMP/$NAME" -C "$TMP"
  SRC="$TMP/shelfcode-linux-$ARCH"
fi
[ -x "$SRC/shelfcode" ] || die "shelfcode program not found in $SRC"

# user
if ! getent group "$SVC_USER" >/dev/null; then groupadd --system "$SVC_USER"; fi
if ! getent passwd "$SVC_USER" >/dev/null; then
  useradd --system --gid "$SVC_USER" --home-dir "$DATA" --no-create-home --shell /usr/sbin/nologin --comment SHELFCODE "$SVC_USER"
  ok "created system user '$SVC_USER'"
fi

# program (stop first so the file can be replaced)
systemctl stop shelfcode.service >/dev/null 2>&1 || true
install -m 0755 "$SRC/shelfcode" "$BIN"
ok "program: $BIN ($("$BIN" version))"

# data
install -d -m 0750 -o "$SVC_USER" -g "$SVC_USER" "$DATA"
ok "data folder: $DATA"

# settings (never overwritten on upgrade, except the options given now)
install -d -m 0755 "$CONF_DIR"
if [ ! -f "$CONF" ]; then
  if [ -f "$SRC/shelfcode.env" ]; then install -m 0640 "$SRC/shelfcode.env" "$CONF"
  else printf 'SHELFCODE_PORT=8765\nSHELFCODE_HOST=0.0.0.0\n' > "$CONF"; chmod 0640 "$CONF"; fi
  chgrp "$SVC_USER" "$CONF"
fi
set_conf() { # key value
  if grep -qE "^#?$1=" "$CONF"; then sed -i -E "s|^#?$1=.*|$1=$2|" "$CONF"; else echo "$1=$2" >> "$CONF"; fi
}
set_conf SHELFCODE_PORT "$PORT"
[ -n "$PIN" ] && set_conf SHELFCODE_PIN "$PIN"
ok "settings: $CONF"

# service
if [ -f "$SRC/shelfcode.service" ]; then
  sed -e "s|^ExecStart=.*|ExecStart=$BIN|" -e "s|^ExecStartPre=+-/usr/bin/shelfcode|ExecStartPre=+-$BIN|" "$SRC/shelfcode.service" > "$UNIT"
else
  die "shelfcode.service missing from the release archive"
fi
systemctl daemon-reload
systemctl enable --now shelfcode.service >/dev/null 2>&1
ok "service: shelfcode (starts automatically at boot)"

# wait until it answers
up=no
for _ in $(seq 1 30); do
  if command -v curl >/dev/null 2>&1; then curl -fs "http://127.0.0.1:$PORT/api/pin-required" >/dev/null 2>&1 && { up=yes; break; }
  else "$BIN" healthcheck >/dev/null 2>&1 && { up=yes; break; }; fi
  sleep 0.5
done
if [ "$up" = yes ]; then ok "SHELFCODE is running"; else
  warn "SHELFCODE did not answer yet. Check:  journalctl -u shelfcode -n 50"
fi

# firewall
fw_hint=""
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  if [ "$OPEN_FW" = yes ]; then ufw allow "$PORT/tcp" >/dev/null && ok "firewall (ufw): port $PORT opened"
  else fw_hint="sudo ufw allow $PORT/tcp"; fi
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  if [ "$OPEN_FW" = yes ]; then firewall-cmd --permanent --add-port="$PORT/tcp" >/dev/null && firewall-cmd --reload >/dev/null && ok "firewall (firewalld): port $PORT opened"
  else fw_hint="sudo firewall-cmd --permanent --add-port=$PORT/tcp && sudo firewall-cmd --reload"; fi
fi

IPS=$(hostname -I 2>/dev/null || true)
echo ""
bold "SHELFCODE is ready. Open it in a browser:"
for ip in $IPS; do
  case "$ip" in *:*) ;; *) echo "    http://$ip:$PORT" ;; esac
done
[ -z "$IPS" ] && echo "    http://<this-server>:$PORT"
echo ""
if ! grep -qE '^SHELFCODE_PIN=.+' "$CONF"; then
  warn "No PIN is set: anyone who can reach port $PORT can use SHELFCODE."
  echo "    Set one in the app (Settings → Network) or re-run with:  --pin 1234"
  echo ""
fi
[ -n "$fw_hint" ] && { warn "A firewall is active. To allow other devices:  $fw_hint"; echo ""; }
echo "  Status:     systemctl status shelfcode"
echo "  Logs:       journalctl -u shelfcode -f"
echo "  Settings:   $CONF   (then: sudo systemctl restart shelfcode)"
echo "  Data:       $DATA   (daily backups in $DATA/backups)"
echo "  Upgrade:    run this installer again"
echo "  Uninstall:  curl -fsSL https://github.com/$REPO/releases/latest/download/install.sh | sudo bash -s -- --uninstall"
