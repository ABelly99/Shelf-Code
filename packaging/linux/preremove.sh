#!/bin/sh
# Runs before the package is removed (not on upgrade).
set -e
remove=no
if [ "$1" = "remove" ] || [ "$1" = "purge" ]; then remove=yes; fi   # deb
if [ "$1" = "0" ]; then remove=yes; fi                               # rpm
if [ "$remove" = yes ] && [ -d /run/systemd/system ]; then
  systemctl disable --now shelfcode.service >/dev/null 2>&1 || true
fi
