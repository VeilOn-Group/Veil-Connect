#!/usr/bin/env bash
# Builds the pinned Android build image from toolchain.lock (cached by tag)
# and runs a command in it, unprivileged, with the workspace (veilon-app and
# veil-shared side by side) at /src.
#   scripts/docker.sh scripts/build-aar.sh
#   scripts/docker.sh gradle --no-daemon :app:assembleRelease
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
# The workspace holds the sibling checkouts (veilon-app, veil-shared): the
# Go bridge in corebind replaces the shared module with ../../../veil-shared.
repo="$(cd "$here/../.." && pwd)"
app="$(basename "$(cd "$here/.." && pwd)")"
# shellcheck source=../toolchain.lock
source "$here/toolchain.lock"
"$here/scripts/check-lock.sh"
lockhash=$(sha256sum "$here/toolchain.lock" "$here/Dockerfile" | sha256sum | cut -c1-12)
image="veilon/android-build:${SINGBOX_VERSION}-go${GO_VERSION}-${lockhash}"
if ! docker image inspect "$image" >/dev/null 2>&1; then
  docker build -t "$image" -f "$here/Dockerfile" \
    --build-arg GO_IMAGE_REF="${GO_IMAGE}@${GO_IMAGE_DIGEST}" \
    --build-arg GO_VERSION="$GO_VERSION" --build-arg JDK_PACKAGE="$JDK_PACKAGE" \
    --build-arg ANDROID_CMDLINE_TOOLS_BUILD="$ANDROID_CMDLINE_TOOLS_BUILD" \
    --build-arg ANDROID_CMDLINE_TOOLS_SHA256="$ANDROID_CMDLINE_TOOLS_SHA256" \
    --build-arg ANDROID_NDK_VERSION="$ANDROID_NDK_VERSION" --build-arg ANDROID_PLATFORM="$ANDROID_PLATFORM" \
    --build-arg ANDROID_BUILD_TOOLS="$ANDROID_BUILD_TOOLS" \
    --build-arg GOMOBILE_MODULE="$GOMOBILE_MODULE" --build-arg GOMOBILE_VERSION="$GOMOBILE_VERSION" \
    --build-arg GRADLE_VERSION="$GRADLE_VERSION" --build-arg GRADLE_SHA256="$GRADLE_SHA256" \
    "$here"
fi
exec docker run --rm --user "$(id -u):$(id -g)" \
  -e HOME=/tmp/home -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomod -e GRADLE_USER_HOME=/tmp/gradle \
  -v "$repo:/src" -w "/src/$app/android" "$image" "$@"
