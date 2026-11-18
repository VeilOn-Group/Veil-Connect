#!/usr/bin/env bash
# Builds app/libs/veilon-core.aar: sing-box libbox (GPL-3.0, pinned in
# corebind/go.mod) and veil-shared/mobile bound together by gomobile into
# ONE AAR with native library libveilcore.so.
#
#   scripts/build-aar.sh            # inside the pinned image (or a host that
#                                   # matches toolchain.lock exactly)
#   scripts/build-aar.sh --docker   # build the image from toolchain.lock and
#                                   # run this script inside it
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=../toolchain.lock
source "$here/toolchain.lock"

if [[ ${1:-} == --docker ]]; then
  exec "$here/scripts/docker.sh" scripts/build-aar.sh
fi

# Fail fast if the host toolchain is not the locked one.
[[ $(go env GOVERSION) == "go$GO_VERSION" ]] || { echo "need go$GO_VERSION, have $(go env GOVERSION) (use --docker)"; exit 1; }
java --version 2>/dev/null | grep -q '^openjdk 17' || { echo "need OpenJDK 17 (use --docker)"; exit 1; }
[[ -f ${ANDROID_NDK_HOME:-}/source.properties ]] && grep -q "Pkg.Revision = ${ANDROID_NDK_VERSION}" "$ANDROID_NDK_HOME/source.properties" \
  || { echo "need NDK $ANDROID_NDK_VERSION in ANDROID_NDK_HOME (use --docker)"; exit 1; }
command -v gomobile >/dev/null || { echo "gomobile $GOMOBILE_VERSION missing (use --docker)"; exit 1; }
# corebind is deliberately outside the repo go.work.
export GOTOOLCHAIN=local GOFLAGS=-trimpath GOWORK=off

cd "$here/corebind"
if [[ ! -f go.sum ]]; then
  # First networked build: hashes come from sum.golang.org (transparency
  # log), not from trust in the download. Commit the resulting go.sum.
  echo "corebind/go.sum missing: resolving via the checksum database; commit it afterwards" >&2
  go mod tidy
fi
go mod verify
grep -q "^github.com/sagernet/sing-box ${SINGBOX_VERSION} " go.sum || { echo "sing-box ${SINGBOX_VERSION} not in go.sum"; exit 1; }

out="$here/app/libs"
mkdir -p "$out"
rm -f "$out"/libbox.aar "$out"/veilcore.aar  # older layout
gomobile bind -v \
  -target="$ANDROID_TARGETS" -androidapi "$ANDROID_MIN_API" \
  -javapkg=net.veilon.core -libname=veilcore \
  -trimpath -ldflags "-X github.com/sagernet/sing-box/constant.Version=${SINGBOX_VERSION#v} -s -w -buildid= -checklinkname=0" \
  -tags "$LIBBOX_TAGS" \
  -o "$out/veilon-core.aar" \
  github.com/sagernet/sing-box/experimental/libbox \
  github.com/veilon/veil-shared/mobile
sha256sum "$out/veilon-core.aar" | tee "$out/veilon-core.aar.sha256"
