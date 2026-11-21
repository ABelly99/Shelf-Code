#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
if [ "$1" = "purge" ]; then
  echo "SHELFCODE removed. Your data is still in /var/lib/shelfcode (delete it yourself if you don't need it)."
fi
