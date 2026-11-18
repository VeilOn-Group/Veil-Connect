#!/usr/bin/env bash
# Refuses an incomplete toolchain.lock (empty TOFU fields).
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
missing=$(grep -E '^[A-Z0-9_]+=$' "$here/toolchain.lock" | cut -d= -f1 || true)
if [[ -n $missing ]]; then
  echo "toolchain.lock incomplete: $(echo $missing | tr '\n' ' ')" >&2
  echo "run client/android/scripts/lock-toolchain.sh on a networked machine and commit the result" >&2
  exit 1
fi
