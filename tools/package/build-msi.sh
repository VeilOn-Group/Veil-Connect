#!/usr/bin/env bash
# Builds VeilOn-<version>-x64.msi with wixl (msitools) from cross-compiled
# Windows binaries. The UI (Wails) and sing-box.exe are inputs:
#   UI_EXE        VeilOn.exe built on Windows (make desktop)
#   SINGBOX_EXE   pinned sing-box.exe
#   XRAY_EXE      pinned xray.exe (build/xray.lock, Xray-windows-64.zip)
#   VERSION, API_BASE_URL, SPKI_PIN_CURRENT, SPKI_PIN_BACKUP, ROOT_PUBLIC_KEY_B64, OUT
# Authenticode signing of the MSI happens afterwards on the signing host.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
VERSION=${VERSION:-0.1.0}
OUT=$(mkdir -p "${OUT:-$ROOT/dist}" && cd "${OUT:-$ROOT/dist}" && pwd)
: "${UI_EXE:?set UI_EXE}" "${SINGBOX_EXE:?set SINGBOX_EXE}" "${XRAY_EXE:?set XRAY_EXE}"
src=$(mktemp -d)
trap 'rm -rf "$src"' EXIT
ld="-s -w -buildid= -X main.Version=$VERSION"
(cd "$ROOT/veilond" && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOWORK=off go build -trimpath -ldflags "$ld" -o "$src/" ./cmd/veilond ./cmd/veilon-updater ./cmd/veilonctl)
cp "$UI_EXE" "$src/VeilOn.exe"
cp "$SINGBOX_EXE" "$src/sing-box.exe"
cp "$XRAY_EXE" "$src/xray.exe"
cp "$ROOT/THIRD-PARTY-LICENSES.md" "$src/LICENSES.md"
cp "$ROOT/desktop/build/windows/icon.ico" "$src/icon.ico"
sha() { sha256sum "$1" | cut -d' ' -f1; }
sed -e "s|<SINGBOX_SHA256>|$(sha "$src/sing-box.exe")|" -e "s|<XRAY_SHA256>|$(sha "$src/xray.exe")|" \
    -e "s|<API_BASE_URL>|${API_BASE_URL:-https://api.veilon.example}|" \
    -e "s|<SPKI_PIN_CURRENT>|${SPKI_PIN_CURRENT:-REPLACE_PIN_CURRENT}|" -e "s|<SPKI_PIN_BACKUP>|${SPKI_PIN_BACKUP:-REPLACE_PIN_BACKUP}|" \
    -e "s|<ROOT_PUBLIC_KEY_B64>|${ROOT_PUBLIC_KEY_B64:-REPLACE_ROOT_KEY}|" \
    "$ROOT/packaging/windows/veilond.yaml" > "$src/veilond.yaml"
out="$OUT/VeilOn-$VERSION-x64.msi"
wixl -a x64 -D Version="$VERSION" -D Src="$src" -o "$out" "$ROOT/packaging/windows/veilon.wxs"
echo "built $out"
(cd "$OUT" && sha256sum "$(basename "$out")" > "$(basename "$out").sha256")
