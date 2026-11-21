#!/usr/bin/env bash
# Builds every release file into dist/:
#   SHELFCODE.exe, SHELFCODE-arm64.exe            Windows (one file: put it in a folder and run it)
#   shelfcode-linux-{amd64,arm64,armv7}.tar.gz    Linux servers (program + installer + service)
#   shelfcode_<ver>_<arch>.deb / .rpm             Debian/Ubuntu and Fedora/RHEL packages
#   install.sh                                    one-line Linux installer
#   shelfcode-zimaos.yml, shelfcode-docker-compose.yml
#   shelfcode-zimaos-appstore.zip                 ZimaOS / CasaOS app source
#   checksums.txt
#
# Usage: scripts/release.sh 1.0.0
# Needs: Go. Uses go-winres and nfpm (fetched with "go run").
set -euo pipefail

VERSION="${1:-dev}"
VERSION="${VERSION#v}"
cd "$(dirname "$0")/.."
ROOT=$(pwd)
DIST="$ROOT/dist"
BUILD="$ROOT/build"
rm -rf "$DIST" "$BUILD"
mkdir -p "$DIST" "$BUILD"

LDFLAGS="-s -w -X main.appVersion=$VERSION"
export CGO_ENABLED=0
GOWINRES="github.com/tc-hib/go-winres@v0.3.3"
NFPM="github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.49.0"

echo "== Windows"
# Version info + icon inside the .exe (shown in Explorer → Properties and Task Manager).
WINVER="$VERSION"; case "$WINVER" in *[!0-9.]*|'') WINVER="0.0.0" ;; esac
go run "$GOWINRES" simply --arch amd64,arm64 --manifest cli \
  --icon packaging/winres/icon.png \
  --product-name SHELFCODE --file-description "SHELFCODE barcode inventory" \
  --product-version "$WINVER" --file-version "$WINVER" \
  --original-filename SHELFCODE.exe --copyright "CC0 1.0 – public domain" --out rsrc
trap 'rm -f "$ROOT"/rsrc_windows_*.syso' EXIT
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/SHELFCODE.exe" .
GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/SHELFCODE-arm64.exe" .
rm -f "$ROOT"/rsrc_windows_*.syso

echo "== Linux"
for arch in amd64 arm64 armv7; do
  goarch=$arch; goarm=""
  [ "$arch" = armv7 ] && { goarch=arm; goarm=7; }
  name="shelfcode-linux-$arch"
  stage="$BUILD/$name"
  mkdir -p "$stage"
  GOOS=linux GOARCH=$goarch GOARM=$goarm go build -trimpath -tags timetzdata -ldflags "$LDFLAGS" -o "$stage/shelfcode" .
  cp packaging/linux/install.sh packaging/linux/shelfcode.service packaging/linux/shelfcode.env LICENSE "$stage/"
  cp docs/linux.md "$stage/README.md"
  chmod 0755 "$stage/shelfcode" "$stage/install.sh"
  tar -C "$BUILD" --owner=0 --group=0 -czf "$DIST/$name.tar.gz" "$name"

  # .deb and .rpm
  nfpm_arch=$arch; [ "$arch" = armv7 ] && nfpm_arch=arm7
  sed -e "s|\${NFPM_ARCH}|$nfpm_arch|" -e "s|\${VERSION}|$VERSION|" -e "s|\${BINARY}|$stage/shelfcode|" \
    packaging/nfpm.yaml > "$BUILD/nfpm-$arch.yaml"
  for pkg in deb rpm; do
    go run "$NFPM" package --config "$BUILD/nfpm-$arch.yaml" --packager "$pkg" --target "$DIST/" >/dev/null
  done
done
cp packaging/linux/install.sh "$DIST/install.sh"

echo "== Docker / ZimaOS"
cp deploy/docker-compose.yml "$DIST/shelfcode-docker-compose.yml"
cp deploy/zimaos/shelfcode.yml "$DIST/shelfcode-zimaos.yml"
# App source for ZimaOS/CasaOS: same layout as a CasaOS app store.
store="$BUILD/shelfcode-appstore"
mkdir -p "$store/Apps/ShelfCode"
cp deploy/zimaos/shelfcode.yml "$store/Apps/ShelfCode/docker-compose.yml"
cp docs/icon.png "$store/Apps/ShelfCode/icon.png"
cp docs/items.png "$store/Apps/ShelfCode/screenshot-1.png"
echo '[{"name":"Utilities","font":"wrench","description":"Utilities"}]' > "$store/category-list.json"
echo '[{"appid":"shelfcode"}]' > "$store/recommend-list.json"
(cd "$BUILD" && rm -f "$DIST/shelfcode-zimaos-appstore.zip" && zip -qr "$DIST/shelfcode-zimaos-appstore.zip" shelfcode-appstore)

echo "== Checksums"
(cd "$DIST" && sha256sum -- * > checksums.txt)

sed "s/{{VERSION}}/$VERSION/g" packaging/release-notes.md > "$BUILD/release-notes.md"
ls -la "$DIST"
