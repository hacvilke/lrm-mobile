#!/bin/sh
# build.sh — produce LRM Mobile release binaries.
#
# The only interesting line in this file is the Android one. Everything else
# is a conventional Go cross-build.
#
#   Android/Termux needs:  GOOS=android GOARCH=arm64 CGO_ENABLED=0 -buildmode=pie
#
# Why each part is required (see docs/E_TYPE.md for the full story):
#
#   -buildmode=pie   Android's loader refuses ET_EXEC (e_type 2) binaries.
#                    A default Go build is ET_EXEC. PIE makes it ET_DYN.
#   GOOS=android     sets the ELF interpreter to /system/bin/linker64, which
#                    is the loader that actually exists on the device.
#                    A linux/arm64 PIE build asks for
#                    /lib/ld-linux-aarch64.so.1, which Termux does not have,
#                    so it fails a second time even after PIE is fixed.
#   CGO_ENABLED=0    keeps the build pure Go. GOOS=android normally implies
#                    cgo and an NDK toolchain; LRM needs neither, so we turn
#                    cgo off and cross-build from any machine with plain Go.
#                    The Go runtime still emits the correct android ELF
#                    headers; only the C-dependent stdlib paths (cgo DNS
#                    resolver, cgo os/user) fall back to their pure-Go
#                    implementations, which is what we want anyway.
#
# Usage:  scripts/build.sh [--version TAG] [--out DIR] [target ...]
#         scripts/build.sh                  # all default targets
#         scripts/build.sh android_arm64    # just one
#
# Targets are named exactly as the release assets: lrm_<target>.

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/dist"
VERSION="${LRM_MOBILE_VERSION:-dev}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?--version needs a value}"; shift 2 ;;
    --version=*) VERSION="${1#*=}"; shift ;;
    --out) OUT="${2:?--out needs a value}"; shift 2 ;;
    --out=*) OUT="${1#*=}"; shift ;;
    -h|--help) sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) echo "unknown option $1" >&2; exit 2 ;;
    *) break ;;
  esac
done

# Default set. Keeping desktop Linux here is deliberate: LRM Mobile is a
# superset of upstream's Linux support, not a replacement for it.
#
# android_amd64 is NOT in the default set and is not shipped: Go cannot
# produce a PIE binary for android/amd64 without cgo and an NDK toolchain
# ("android/amd64 requires external (cgo) linking"), and a non-PIE one is
# rejected by the loader for the same e_type reason. x86_64 Android
# emulators are therefore unsupported today; build from source inside the
# emulator, or use an arm64 image.
DEFAULT_TARGETS="android_arm64 linux_arm64 linux_amd64"
TARGETS="${*:-$DEFAULT_TARGETS}"

if [ ! -f "$ROOT/third_party/lrm/go.mod" ]; then
  echo "error: third_party/lrm is empty — run: git submodule update --init --recursive" >&2
  exit 1
fi

command -v go >/dev/null 2>&1 || { echo "error: Go is not installed (https://go.dev/dl/)" >&2; exit 1; }

mkdir -p "$OUT"
cd "$ROOT"

LDFLAGS="-s -w -X main.version=$VERSION"

build_one() {
  target="$1"
  goos="${target%%_*}"
  goarch="${target#*_}"
  out="$OUT/lrm_${target}"
  mode=""
  case "$goos" in
    android)
      # Mandatory. Without it the binary is ET_EXEC and Android's loader
      # rejects it with "unexpected e_type: 2".
      mode="-buildmode=pie"
      ;;
    ios)
      echo "  skip lrm_${target}: iOS is not supported yet (see docs/ROADMAP-IOS.md)" >&2
      return 0
      ;;
    windows) out="${out}.exe" ;;
  esac

  echo "  build lrm_${target}"
  # shellcheck disable=SC2086
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build $mode -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/lrm
}

echo "LRM Mobile build ($VERSION) -> $OUT"
for t in $TARGETS; do
  build_one "$t"
done

# Verify what we just produced. A release that silently ships an ET_EXEC
# Android binary is the exact bug this repository exists to prevent, so the
# check is part of the build, not a separate optional step.
if [ -x "$ROOT/scripts/verify-elf.sh" ]; then
  "$ROOT/scripts/verify-elf.sh" "$OUT"
fi

( cd "$OUT" && (sha256sum lrm_* 2>/dev/null || shasum -a 256 lrm_*) > SHA256SUMS.txt )
echo "checksums -> $OUT/SHA256SUMS.txt"
ls -l "$OUT"
