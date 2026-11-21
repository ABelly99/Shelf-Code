#!/bin/sh
# Runs after the .deb / .rpm package is installed or upgraded.
set -e
if ! getent group shelfcode >/dev/null 2>&1; then
  groupadd --system shelfcode
fi
if ! getent passwd shelfcode >/dev/null 2>&1; then
  useradd --system --gid shelfcode --home-dir /var/lib/shelfcode --no-create-home \
    --shell /usr/sbin/nologin --comment "SHELFCODE" shelfcode
fi
mkdir -p /var/lib/shelfcode
chown shelfcode:shelfcode /var/lib/shelfcode
chmod 0750 /var/lib/shelfcode

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
  # deb: "configure <old-version>"   rpm: "1" = install, "2" = upgrade
  upgrade=no
  if [ "$1" = "configure" ] && [ -n "$2" ]; then upgrade=yes; fi
  if [ "$1" = "2" ]; then upgrade=yes; fi
  if [ "$upgrade" = yes ]; then
    systemctl try-restart shelfcode.service >/dev/null 2>&1 || true
  else
    systemctl enable --now shelfcode.service >/dev/null 2>&1 || true
  fi
fi

port=$(sed -n 's/^SHELFCODE_PORT=//p' /etc/shelfcode/shelfcode.env 2>/dev/null | tail -n1)
port=${port:-8765}
ip=$(hostname -I 2>/dev/null | awk '{print $1}')
echo ""
echo "SHELFCODE is running:  http://${ip:-this-server}:${port}"
echo "  Settings:  /etc/shelfcode/shelfcode.env   (then: sudo systemctl restart shelfcode)"
echo "  Logs:      journalctl -u shelfcode -f"
echo "  Data:      /var/lib/shelfcode"
echo ""
