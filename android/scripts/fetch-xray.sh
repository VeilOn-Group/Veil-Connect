#!/usr/bin/env bash
# Puts the pinned official Xray-core Android binaries into the APK as
# lib/<abi>/libxray.so. Android only allows executing files from
# the app's native library directory, so the binary ships as a "library"
# and the app runs it as a child process (VLESS: XHTTP and Vision).
#
# Archives and digests: veil-shared/build/xray.lock (official XTLS/Xray-core release).
# armeabi-v7a has no official Android build: those devices use Hysteria 2
# and naive only.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
lock="${SHARED_DIR:-$here/../../veil-shared}/build/xray.lock"
get() { grep "^$1=" "$lock" | cut -d= -f2-; }
base=$(get XRAY_BASE_URL)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for pair in ARM64:arm64-v8a AMD64:x86_64; do
  key=${pair%%:*} abi=${pair##*:}
  zip=$(get "XRAY_ANDROID_${key}_ZIP") sum=$(get "XRAY_ANDROID_${key}_ZIP_SHA256")
  curl -fsSL -o "$tmp/$zip" "$base/$zip"
  echo "$sum  $tmp/$zip" | sha256sum -c -
  mkdir -p "$here/app/src/main/jniLibs/$abi"
  unzip -p "$tmp/$zip" xray > "$here/app/src/main/jniLibs/$abi/libxray.so"
  chmod 0755 "$here/app/src/main/jniLibs/$abi/libxray.so"
  echo "libxray.so for $abi: $(sha256sum < "$here/app/src/main/jniLibs/$abi/libxray.so" | cut -d' ' -f1)"
done
