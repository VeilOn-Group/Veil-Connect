#!/usr/bin/env bash
# One-time (and on every version bump): fill the TOFU fields of
# toolchain.lock. Run on a trusted, networked machine; review the diff
# (ideally compare with a second machine/network) and commit it.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
lock="$here/toolchain.lock"
# shellcheck source=../toolchain.lock
source "$lock"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
set_field() { sed -i "s|^$1=.*|$1=$2|" "$lock"; echo "$1=$2"; }

docker pull "$GO_IMAGE" >/dev/null
set_field GO_IMAGE_DIGEST "$(docker image inspect --format '{{index .RepoDigests 0}}' "$GO_IMAGE" | cut -d@ -f2)"

curl -fsSLo "$tmp/clt.zip" "https://dl.google.com/android/repository/commandlinetools-linux-${ANDROID_CMDLINE_TOOLS_BUILD}_latest.zip"
set_field ANDROID_CMDLINE_TOOLS_SHA256 "$(sha256sum "$tmp/clt.zip" | cut -d' ' -f1)"

curl -fsSLo "$tmp/gradle.zip" "https://services.gradle.org/distributions/gradle-${GRADLE_VERSION}-bin.zip"
got=$(sha256sum "$tmp/gradle.zip" | cut -d' ' -f1)
# Gradle publishes the checksum separately; both must agree.
pub=$(curl -fsSL "https://services.gradle.org/distributions/gradle-${GRADLE_VERSION}-bin.zip.sha256")
[[ $got == "$pub" ]] || { echo "gradle checksum mismatch: $got vs $pub"; exit 1; }
set_field GRADLE_SHA256 "$got"
