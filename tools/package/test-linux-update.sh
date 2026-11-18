#!/usr/bin/env bash
# Silent update round trip on a disposable Debian/Ubuntu host or VM, as root
# (ADR-0069): installs veilon 0.6.0, then lets veilon-updater install a
# signed 0.6.1 from the inbox, checks that a downgrade, a tampered package
# and a foreign signature are refused, and removes the package.
# All keys are generated here for the test and deleted afterwards.
# Env: SINGBOX_BIN, XRAY_BIN (pinned cores).
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
[[ $(id -u) == 0 ]] || { echo "run as root on a disposable host" >&2; exit 2; }
t=$(mktemp -d)
trap 'rm -rf "$t"' EXIT
(cd "${API_DIR:-$ROOT/../veil-api}/server" && go build -o "$t/keygen" ./cmd/veilon-keygen)
cd "$t"
pub() { "$t/keygen" root -out "$1" | sed 's/.*: //'; }
ROOTPUB=$(pub root.seed); RELPUB=$(pub rel.seed); EVILPUB=$(pub evil.seed)
"$t/keygen" keyset -root root.seed -version 1 -key prof-1="$RELPUB" -release-key rel-1="$RELPUB" -out keyset.json >/dev/null
export API_BASE_URL=https://api.veilon.example ROOT_PUBLIC_KEY_B64=$ROOTPUB
export SPKI_PIN_CURRENT=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= SPKI_PIN_BACKUP=BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
for v in 0.6.0 0.6.1; do VERSION=$v OUT=$t/out FORMATS=deb "$ROOT/tools/package/build-linux.sh" >/dev/null; done
deb() { echo "$t/out/veilon_$1_amd64.deb"; }
dpkg -i "$(deb 0.6.0)" >/dev/null
installed() { dpkg-query -W -f='${Version}' veilon; }
[[ $(installed) == 0.6.0 ]]
id veilon >/dev/null && getent group veilon-users >/dev/null
inbox=/var/lib/veilon-update/inbox
[[ $(stat -c '%U:%G %a' $inbox) == "root:veilon 770" ]]
[[ $(stat -c '%U %a' /var/lib/veilon-update/work) == "root 700" ]]

sign() { # $1 seed  $2 version  $3 file -> rel.json
  REL_SEED=$(cat "$1") "$t/keygen" release-sign -seed-env REL_SEED -key-id rel-1 -platform linux-amd64-deb \
    -version "$2" -url "https://updates.veilon.example/$(basename "$3")" -file "$3" -out rel.json >/dev/null
}
submit() { # as the unprivileged veilond account would
  install -o veilon -g veilon -m 0640 keyset.json $inbox/keyset.json
  install -o veilon -g veilon -m 0640 rel.json $inbox/release.json
  install -o veilon -g veilon -m 0640 "$1" $inbox/artifact
  install -o veilon -g veilon -m 0640 /dev/null $inbox/request
}
expect_refused() {
  if /usr/lib/veilon/veilon-updater apply 2>"$t/err"; then echo "FAIL: $1 accepted" >&2; exit 1; fi
  [[ -z $(ls -A $inbox) ]] || { echo "FAIL: inbox not emptied after $1" >&2; exit 1; }
  echo "ok: $1 refused ($(head -c 120 "$t/err"))"
}

# 1. foreign release key
sign evil.seed 0.6.1 "$(deb 0.6.1)"; submit "$(deb 0.6.1)"; expect_refused "foreign signature"
# 2. tampered package (same size)
sign rel.seed 0.6.1 "$(deb 0.6.1)"; cp "$(deb 0.6.1)" bad.deb; printf 'X' | dd of=bad.deb bs=1 seek=4096 conv=notrunc 2>/dev/null
submit bad.deb; expect_refused "tampered package"
# 3. the real update
sign rel.seed 0.6.1 "$(deb 0.6.1)"; submit "$(deb 0.6.1)"
/usr/lib/veilon/veilon-updater apply
[[ $(installed) == 0.6.1 ]] && echo "ok: 0.6.1 installed silently"
[[ $(/usr/lib/veilon/veilon-updater version) == "veilon-updater 0.6.1 linux-amd64-deb" ]]
# 4. downgrade back to 0.6.0
sign rel.seed 0.6.0 "$(deb 0.6.0)"; submit "$(deb 0.6.0)"; expect_refused "downgrade"
[[ $(installed) == 0.6.1 ]]
dpkg -P veilon >/dev/null
echo "silent update round trip: OK"
