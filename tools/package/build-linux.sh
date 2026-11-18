#!/usr/bin/env bash
# Builds the Linux client packages (ADR-0069): veilon_<v>_<arch>.deb and
# veilon-<v>-1.<arch>.rpm with veilond, veilon-updater, veilonctl, the
# pinned cores (sing-box, xray) and, when present, the desktop UI.
#
# Env:
#   VERSION            x.y.z (default 0.1.0)
#   SINGBOX_BIN        official sing-box client binary (pinned, build/singbox.lock)
#   XRAY_BIN           official Xray binary (pinned, build/xray.lock)
#   UI_BIN             optional: desktop UI binary (make desktop)
#   API_BASE_URL, SPKI_PIN_CURRENT, SPKI_PIN_BACKUP, ROOT_PUBLIC_KEY_B64
#                      public values baked into /etc/veilon/veilond.yaml
#   OUT                output dir (default ./dist)
#   FORMATS            "deb rpm" (default)
# No secrets are involved: the root key is a public key.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
VERSION=${VERSION:-0.1.0}
OUT=$(mkdir -p "${OUT:-$ROOT/dist}" && cd "${OUT:-$ROOT/dist}" && pwd)
FORMATS=${FORMATS:-deb rpm}
SINGBOX_BIN=${SINGBOX_BIN:?set SINGBOX_BIN}
XRAY_BIN=${XRAY_BIN:?set XRAY_BIN}
GOARCH=${GOARCH:-amd64}
case $GOARCH in amd64) DEBARCH=amd64 RPMARCH=x86_64 ;; arm64) DEBARCH=arm64 RPMARCH=aarch64 ;; *) echo "arch $GOARCH" >&2; exit 2 ;; esac
PKG=$ROOT/packaging/linux
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

sha() { sha256sum "$1" | cut -d' ' -f1; }

stage() { # $1 = format (deb|rpm) -> $work/$1/root
  local fmt=$1 r=$work/$fmt/root
  mkdir -p "$r"/usr/lib/veilon "$r"/usr/bin "$r"/etc/veilon "$r"/usr/lib/systemd/system \
    "$r"/usr/lib/sysusers.d "$r"/usr/lib/tmpfiles.d "$r"/usr/share/doc/veilon
  local ld="-s -w -buildid= -X main.Version=$VERSION -X github.com/veilon/veilon-app/veilond/internal/core.PackageFormat=$fmt"
  (cd "$ROOT/veilond" && CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH GOWORK=off go build -trimpath -ldflags "$ld" \
    -o "$r/usr/lib/veilon/" ./cmd/veilond ./cmd/veilon-updater)
  (cd "$ROOT/veilond" && CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH GOWORK=off go build -trimpath -ldflags "-s -w -buildid= -X main.Version=$VERSION" \
    -o "$r/usr/bin/" ./cmd/veilonctl)
  install -m 0755 "$SINGBOX_BIN" "$r/usr/lib/veilon/sing-box"
  install -m 0755 "$XRAY_BIN" "$r/usr/lib/veilon/xray"
  if [[ -n ${UI_BIN:-} ]]; then
    mkdir -p "$r"/usr/share/applications "$r"/usr/share/icons/hicolor/256x256/apps
    install -m 0755 "$UI_BIN" "$r/usr/bin/veilon"
    install -m 0644 "$ROOT/desktop/build/appicon.png" "$r/usr/share/icons/hicolor/256x256/apps/veilon.png"
    cat > "$r/usr/share/applications/net.veilon.Veilon.desktop" <<DESK
[Desktop Entry]
Type=Application
Name=VeilOn
Comment=VeilOn VPN
Exec=/usr/bin/veilon
Icon=veilon
Categories=Network;
DESK
  fi
  sed -e "s|<SINGBOX_SHA256>|$(sha "$SINGBOX_BIN")|" -e "s|<XRAY_SHA256>|$(sha "$XRAY_BIN")|" \
      -e "s|<API_BASE_URL>|${API_BASE_URL:-https://api.veilon.example}|" \
      -e "s|<SPKI_PIN_CURRENT>|${SPKI_PIN_CURRENT:-REPLACE_PIN_CURRENT}|" -e "s|<SPKI_PIN_BACKUP>|${SPKI_PIN_BACKUP:-REPLACE_PIN_BACKUP}|" \
      -e "s|<ROOT_PUBLIC_KEY_B64>|${ROOT_PUBLIC_KEY_B64:-REPLACE_ROOT_KEY}|" "$PKG/veilond.yaml" > "$r/etc/veilon/veilond.yaml"
  chmod 0644 "$r/etc/veilon/veilond.yaml"
  install -m 0644 "$PKG/veilond.service" "$PKG/veilon-update.service" "$PKG/veilon-update.path" "$r/usr/lib/systemd/system/"
  install -m 0644 "$PKG/veilon.sysusers.conf" "$r/usr/lib/sysusers.d/veilon.conf"
  install -m 0644 "$PKG/veilon.tmpfiles.conf" "$r/usr/lib/tmpfiles.d/veilon.conf"
  install -m 0644 "$PKG/veilond-app-rules.conf" "$r/usr/share/doc/veilon/veilond-app-rules.conf"
  install -m 0644 "$ROOT/THIRD-PARTY-LICENSES.md" "$r/usr/share/doc/veilon/LICENSES.md"
}

POSTINST='set -e
systemd-sysusers /usr/lib/sysusers.d/veilon.conf >/dev/null 2>&1 || true
systemd-tmpfiles --create /usr/lib/tmpfiles.d/veilon.conf >/dev/null 2>&1 || true
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
  systemctl enable veilond.service veilon-update.path >/dev/null 2>&1 || true
  systemctl restart veilond.service >/dev/null 2>&1 || true
  systemctl start veilon-update.path >/dev/null 2>&1 || true
fi'
PRERM='set -e
if [ -d /run/systemd/system ] && [ "${1:-}" = remove -o "${1:-}" = 0 ]; then
  systemctl disable --now veilon-update.path veilond.service >/dev/null 2>&1 || true
fi'

for fmt in $FORMATS; do
  stage "$fmt"
  r=$work/$fmt/root
  size=$(du -sk "$r" | cut -f1)
  case $fmt in
  deb)
    mkdir -p "$r/DEBIAN"
    cat > "$r/DEBIAN/control" <<CTRL
Package: veilon
Version: $VERSION
Architecture: $DEBARCH
Maintainer: VeilOn <support@veilon.example>
Installed-Size: $size
Depends: systemd, nftables
Recommends: libwebkit2gtk-4.1-0
Section: net
Priority: optional
Homepage: https://veilon.example
Description: VeilOn VPN client
 Client core (veilond) with sing-box and Xray-core, the silent update
 helper and the command line tool. The desktop UI runs as the user;
 members of the veilon-users group may control the VPN.
CTRL
    echo /etc/veilon/veilond.yaml > "$r/DEBIAN/conffiles"
    printf '#!/bin/sh\n%s\n' "$POSTINST" > "$r/DEBIAN/postinst"
    printf '#!/bin/sh\n%s\n' "$PRERM" > "$r/DEBIAN/prerm"
    chmod 0755 "$r/DEBIAN/postinst" "$r/DEBIAN/prerm"
    dpkg-deb --root-owner-group -Zxz --build "$r" "$OUT/veilon_${VERSION}_${DEBARCH}.deb" >/dev/null
    echo "built $OUT/veilon_${VERSION}_${DEBARCH}.deb"
    ;;
  rpm)
    top=$work/rpm/top
    mkdir -p "$top"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
    files=$(cd "$r" && find . -type f | sed 's|^\.||' | sort | grep -v '^/etc/veilon/veilond.yaml$')
    cat > "$top/SPECS/veilon.spec" <<SPEC
Name: veilon
Version: $VERSION
Release: 1
Summary: VeilOn VPN client
License: Proprietary (third-party components: see LICENSES.md)
URL: https://veilon.example
BuildArch: $RPMARCH
Requires: systemd, nftables
AutoReqProv: no
%global debug_package %{nil}
%global __strip /bin/true
%description
Client core (veilond) with sing-box and Xray-core, the silent update
helper and the command line tool.
%install
cp -a $r/. %{buildroot}/
%post
$POSTINST
%preun
$PRERM
%files
%defattr(-,root,root,-)
%config(noreplace) /etc/veilon/veilond.yaml
$files
SPEC
    rpmbuild --quiet --define "_topdir $top" --define "_build_id_links none" -bb "$top/SPECS/veilon.spec"
    cp "$top/RPMS/$RPMARCH/veilon-$VERSION-1.$RPMARCH.rpm" "$OUT/"
    echo "built $OUT/veilon-$VERSION-1.$RPMARCH.rpm"
    ;;
  esac
done
(cd "$OUT" && sha256sum veilon* > SHA256SUMS.linux)
