#!/bin/sh
# verify-elf.sh — fail the build if an Android binary would be rejected by
# Android's loader.
#
# This is the regression test for the original bug. Reading the ELF header
# is cheap and it catches the failure on the build machine instead of on
# someone's phone:
#
#   e_type must be 3 (ET_DYN / PIE). 2 (ET_EXEC) is what produced
#     error: "..." has unexpected e_type: 2
#
#   the ELF interpreter must be /system/bin/linker64, not
#     /lib/ld-linux-aarch64.so.1 — the latter does not exist in Termux.
#
# Usage: scripts/verify-elf.sh [dist-dir]

set -eu
DIR="${1:-dist}"
FAIL=0

have() { command -v "$1" >/dev/null 2>&1; }

# e_type lives at byte offset 16 of the ELF header, little-endian uint16.
elf_type() {
  od -An -tu2 -j16 -N2 "$1" 2>/dev/null | tr -d ' \n'
}

for f in "$DIR"/lrm_android_*; do
  [ -f "$f" ] || continue
  t="$(elf_type "$f")"
  if [ "$t" != "3" ]; then
    echo "FAIL $(basename "$f"): e_type=$t, want 3 (ET_DYN/PIE). Build with -buildmode=pie." >&2
    FAIL=1
  else
    echo "  ok $(basename "$f"): ET_DYN (PIE)"
  fi

  if have strings; then
    if strings -a "$f" | grep -q '^/system/bin/linker64$'; then
      echo "  ok $(basename "$f"): interpreter /system/bin/linker64"
    elif strings -a "$f" | grep -q 'ld-linux-aarch64'; then
      echo "FAIL $(basename "$f"): interpreter is glibc's ld-linux, which Termux does not have. Build with GOOS=android." >&2
      FAIL=1
    fi
  fi
done

for f in "$DIR"/lrm_linux_*; do
  [ -f "$f" ] || continue
  echo "  ok $(basename "$f"): e_type=$(elf_type "$f") (desktop Linux accepts both 2 and 3)"
done

exit "$FAIL"
