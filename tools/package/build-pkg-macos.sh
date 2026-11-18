#!/usr/bin/env bash
# Builds VeilOn-<version>.pkg on macOS (pkgbuild/productbuild are macOS
# tools) from universal binaries (ADR-0069). Signing and notarisation need a Mac with the
# Developer ID Installer certificate (not available in CI).
#   UI_APP (VeilOn.app), SINGBOX_BIN, XRAY_BIN (universal or per-arch lipo),
#   VERSION, API_BASE_URL, SPKI_PIN_CURRENT, SPKI_PIN_BACKUP, ROOT_PUBLIC_KEY_B64,
#   SIGN_IDENTITY (optional "Developer ID Installer: ..."), OUT
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
VERSION=${VERSION:-0.1.0}
OUT=$(mkdir -p "${OUT:-$ROOT/dist}" && cd "${OUT:-$ROOT/dist}" && pwd)
r=$(mktemp -d)
trap 'rm -rf "$r"' EXIT
D="$r/Library/Application Support/VeilOn"
mkdir -p "$D" "$r/Library/LaunchDaemons" "$r/Applications" "$r/usr/local/bin"
ld="-s -w -buildid= -X main.Version=$VERSION"
for a in amd64 arm64; do
  (cd "$ROOT/veilond" && CGO_ENABLED=0 GOOS=darwin GOARCH=$a GOWORK=off go build -trimpath -ldflags "$ld" -o "$r/$a/" ./cmd/veilond ./cmd/veilon-updater ./cmd/veilonctl)
done
for b in veilond veilon-updater; do lipo -create -output "$D/$b" "$r/amd64/$b" "$r/arm64/$b"; done
lipo -create -output "$r/usr/local/bin/veilonctl" "$r/amd64/veilonctl" "$r/arm64/veilonctl"
rm -rf "$r/amd64" "$r/arm64"
install -m 0755 "${SINGBOX_BIN:?}" "$D/sing-box"
install -m 0755 "${XRAY_BIN:?}" "$D/xray"
cp -R "${UI_APP:?}" "$r/Applications/VeilOn.app"
sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
sed -e "s|<SINGBOX_SHA256>|$(sha "$D/sing-box")|" -e "s|<XRAY_SHA256>|$(sha "$D/xray")|" \
    -e "s|<API_BASE_URL>|${API_BASE_URL:?}|" -e "s|<SPKI_PIN_CURRENT>|${SPKI_PIN_CURRENT:?}|" \
    -e "s|<SPKI_PIN_BACKUP>|${SPKI_PIN_BACKUP:?}|" -e "s|<ROOT_PUBLIC_KEY_B64>|${ROOT_PUBLIC_KEY_B64:?}|" \
    "$ROOT/packaging/macos/veilond.yaml" > "$D/veilond.yaml"
cp "$ROOT/packaging/macos/net.veilon.veilond.plist" "$ROOT/packaging/macos/net.veilon.updater.plist" "$r/Library/LaunchDaemons/"
args=(--root "$r" --identifier net.veilon.client --version "$VERSION" --scripts "$ROOT/packaging/macos/scripts" --install-location /)
[[ -n ${SIGN_IDENTITY:-} ]] && args+=(--sign "$SIGN_IDENTITY")
pkgbuild "${args[@]}" "$OUT/VeilOn-$VERSION.pkg"
echo "built $OUT/VeilOn-$VERSION.pkg"
