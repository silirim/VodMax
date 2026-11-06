#!/bin/sh
# Builds the release packages into dist/<version>/:
#   vodmax-server_<v>_linux_{amd64,arm64,armv7}.tar.gz  server + systemd unit
#   VodMax-<v>.apk                                      Android (with the built-in server)
#   app.vodmax.client_<v>_all.ipk                        LG webOS
#   SHA256SUMS
# Needs Go; for the APK the Android SDK and NDK (clients/android/local.properties)
# and a JDK 17 (JAVA_HOME); for the IPK Node.js (npx @webos-tools/cli).
#   scripts/release.sh 0.2.0
# It stops when the apps' versions do not match, when the APK or the IPK
# cannot be built, or when the APK is not signed with the release key.
# DEBUG=1 turns these into warnings, for a local test build.
set -e
v=${1:?versione, es. 0.2.0}
cd "$(dirname "$0")/.."
out=dist/$v
fail() { echo "release.sh: $*" >&2; [ -n "${DEBUG:-}" ] || exit 1; }

# Android refuses an update whose versionCode is not higher than the
# installed one; webOS takes the numeric X.Y.Z only.
gradle=clients/android/app/build.gradle
grep -q "versionName \"$v\"" $gradle || fail "versionName in $gradle non è $v"
grep -q "\"version\": *\"${v%%-*}\"" clients/webos/appinfo.json || fail "version in clients/webos/appinfo.json non è ${v%%-*}"
code() { sed -n 's/^ *versionCode *\([0-9][0-9]*\).*/\1/p'; }
prev=$(git describe --tags --abbrev=0 --exclude "v$v" 2>/dev/null || true)
if [ -n "$prev" ]; then
  now=$(code < $gradle) before=$(git show "$prev:$gradle" | code)
  [ "${now:-0}" -gt "${before:-0}" ] || fail "versionCode $now in $gradle non è maggiore di $before ($prev)"
fi
rm -rf "$out" && mkdir -p "$out"

for t in amd64: arm64: arm:7; do
  arch=${t%%:*}; arm=${t#*:}; name=$arch; [ "$arch" = arm ] && name=armv7
  dir=$(mktemp -d)/vodmax-server_${v}_linux_$name
  mkdir -p "$dir"
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch GOARM=$arm go build -trimpath -ldflags="-s -w -X main.version=$v" -o "$dir/vodmax-server" ./cmd/vodmax-server
  cp packaging/vodmax.service packaging/vodmax-update README.md docs/install.md docs/installazione.md "$dir/"
  tar -C "$(dirname "$dir")" -czf "$out/$(basename "$dir").tar.gz" "$(basename "$dir")"
  rm -rf "$(dirname "$dir")"
done

if [ -n "${JAVA_HOME:-}" ]; then
  VERSION=$v clients/android/build-server.sh
  # Signed with the release key when there is one (see app/build.gradle),
  # else with the debug key, for local tests only.
  if [ -f "${VODMAX_SIGNING:-$HOME/.config/vodmax-release/keystore.properties}" ]; then
    (cd clients/android && ./gradlew --no-daemon -q assembleRelease)
    cp clients/android/app/build/outputs/apk/release/app-release.apk "$out/VodMax-$v.apk"
  else
    fail "nessuna chiave di rilascio: l'APK sarebbe firmato con la chiave di debug"
    (cd clients/android && ./gradlew --no-daemon -q assembleDebug)
    cp clients/android/app/build/outputs/apk/debug/app-debug.apk "$out/VodMax-$v.apk"
  fi
  # Installed apps take an update only with the key they were signed with.
  sdk=${ANDROID_HOME:-$(sed -n 's/^sdk.dir=//p' clients/android/local.properties)}
  signer=$(ls -d "$sdk"/build-tools/*/apksigner 2>/dev/null | sort -V | tail -n 1)
  certs=$("${signer:-apksigner}" verify --print-certs "$out/VodMax-$v.apk") || fail "firma dell'APK non verificata"
  case $certs in *"CN=Android Debug"*) fail "APK firmato con la chiave di debug" ;; esac
else
  fail "JAVA_HOME non impostato: niente APK"
fi

if command -v npx >/dev/null; then
  npx --yes -p @webos-tools/cli@3.2.6 ares-package clients/webos -o "$out"
else
  fail "npx non trovato: niente pacchetto per le TV LG"
fi

(cd "$out" && sha256sum -- * > SHA256SUMS)
ls -l "$out"
