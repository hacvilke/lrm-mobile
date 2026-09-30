#!/bin/sh
# LRM Mobile installer.
#
#   curl -fsSL https://raw.githubusercontent.com/hacvilke/lrm-mobile/main/scripts/install.sh | sh
#
# The owner is a default, not a hard-coded constant: override it with
# LRM_MOBILE_REPO=owner/lrm-mobile, or point LRM_BASE_URL at a mirror.
#
# What it does, in order:
#   1. detect the operating system
#   2. detect the CPU architecture
#   3. detect Android and Termux *separately from Linux*
#   4. choose the matching release asset
#   5. download it
#   6. verify its SHA-256 against the release's SHA256SUMS.txt
#   7. install it to $HOME/.local/bin/lrm  (no root, ever)
#   8. check whether that directory is on PATH
#   9. print the exact line to add if it is not
#  10. run the binary to prove it actually executes here
#
# Options:
#   --dir DIR        install here (default $LRM_INSTALL_DIR, else ~/.local/bin)
#   --version TAG    install a specific release (default: latest)
#   --from FILE      install a local binary instead of downloading
#   --source         build from source with Go instead of downloading
#   --force          overwrite an existing lrm
#   --no-verify      skip checksum verification (not recommended)
#   --dry-run        print the plan and change nothing
#   --print-target   print the detected target triple and exit
#
# Environment:
#   LRM_MOBILE_REPO   owner/repo to download from
#   LRM_BASE_URL      release download base URL (mirror)
#   LRM_INSTALL_DIR   install directory
#   LRM_VERSION       release tag
#
# POSIX sh. No bashisms, no root, no package manager, no dependency beyond
# curl-or-wget and a sha256 tool.

set -eu

REPO="${LRM_MOBILE_REPO:-hacvilke/lrm-mobile}"  # override with $LRM_MOBILE_REPO
VERSION="${LRM_VERSION:-}"
BASE_URL="${LRM_BASE_URL:-}"
INSTALL_DIR="${LRM_INSTALL_DIR:-}"
FROM_FILE=""
USE_SOURCE=0
FORCE=0
DRY_RUN=0
NO_VERIFY=0
PRINT_TARGET=0
BIN_NAME="lrm"

say()  { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --dir)          INSTALL_DIR="${2:?--dir needs a value}"; shift 2 ;;
    --dir=*)        INSTALL_DIR="${1#*=}"; shift ;;
    --version)      VERSION="${2:?--version needs a value}"; shift 2 ;;
    --version=*)    VERSION="${1#*=}"; shift ;;
    --from)         FROM_FILE="${2:?--from needs a value}"; shift 2 ;;
    --from=*)       FROM_FILE="${1#*=}"; shift ;;
    --source)       USE_SOURCE=1; shift ;;
    --force)        FORCE=1; shift ;;
    --no-verify)    NO_VERIFY=1; shift ;;
    --dry-run)      DRY_RUN=1; shift ;;
    --print-target) PRINT_TARGET=1; shift ;;
    -h|--help)      sed -n '2,42p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)              die "unknown option $1 (try --help)" ;;
  esac
done

# --------------------------------------------------------------- detection ---
# These four functions are the heart of the installer and the reason this
# repository exists. The upstream LRM installer asked only `uname -s` and
# `uname -m`; inside Termux those answer "Linux" and "aarch64", so it
# installed lrm_linux_arm64 — a binary Android's loader will not run.

# 3a. Termux: the userland shipped by the Termux app.
detect_termux() {
  # $TERMUX_VERSION is exported by every Termux shell.
  [ -n "${TERMUX_VERSION:-}" ] && { echo 1; return; }
  # $PREFIX points inside the app's private data directory.
  case "${PREFIX:-}" in
    *com.termux*) echo 1; return ;;
  esac
  # Last resort: the userland is on disk even if the env was stripped.
  [ -d /data/data/com.termux/files/usr ] && { echo 1; return; }
  echo 0
}

# 3b. Android in general — Termux is one way to be on Android, not the only
#     one. getprop is the authoritative probe; the /system paths corroborate
#     it on devices where getprop is unavailable to an unprivileged shell.
detect_android_release() {
  if command -v getprop >/dev/null 2>&1; then
    getprop ro.build.version.release 2>/dev/null | tr -d '\r\n'
  fi
}
is_android() {
  [ "$TERMUX" = 1 ] && return 0
  [ -n "$ANDROID_RELEASE" ] && return 0
  [ -f /system/build.prop ] && return 0
  [ -f /system/bin/linker64 ] && return 0
  # Set by the Android framework for app processes.
  [ -n "${ANDROID_ROOT:-}" ] && [ -n "${ANDROID_DATA:-}" ] && return 0
  return 1
}

# 1. operating system
detect_os() {
  if is_android; then echo android; return; fi
  case "$(uname -s)" in
    Linux)  echo linux ;;
    Darwin) echo darwin ;;
    CYGWIN*|MINGW*|MSYS*|Windows_NT) echo windows ;;
    *) die "unsupported OS: $(uname -s) — build from source with --source" ;;
  esac
}

# 2. architecture
detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)        echo amd64 ;;
    arm64|aarch64|armv8l) echo arm64 ;;
    armv7l|armv6l|arm)   echo arm ;;
    i386|i686)           echo 386 ;;
    *) die "unsupported CPU: $(uname -m) — build from source with --source" ;;
  esac
}

TERMUX="$(detect_termux)"
ANDROID_RELEASE="$(detect_android_release || true)"
OS="$(detect_os)"
ARCH="$(detect_arch)"

# 4. pick the asset.
TARGET="${OS}_${ARCH}"
case "$TARGET" in
  android_arm64|linux_arm64|linux_amd64) ;;
  android_amd64)
    die "Android on x86_64 (an emulator image) has no prebuilt binary: Go cannot
       produce a PIE binary for android/amd64 without cgo and an NDK.
       Use an arm64 system image, or install Go in the emulator and rerun
       this script with --source." ;;
  android_arm)
    die "32-bit Android (armv7) is not built today. Rerun with --source if you
       have Go available, and please open an issue saying you need it." ;;
  darwin_*|windows_*)
    warn "$OS is supported by upstream LRM, not by LRM Mobile's release matrix."
    die "install upstream LRM instead: https://github.com/hacvilke/lrm" ;;
  *) die "no release build for ${TARGET} — try --source" ;;
esac
ASSET="lrm_${TARGET}"

# ------------------------------------------------------------------ paths ---
# 7. Never /usr/local/bin, never /etc, never sudo. On Termux those are not
#    writable and not on PATH; on desktop Linux they need root.
pick_dir() {
  if [ -n "$INSTALL_DIR" ]; then printf '%s\n' "$INSTALL_DIR"; return; fi
  printf '%s\n' "${HOME:?\$HOME is not set — pass --dir}/.local/bin"
}
DEST_DIR="$(pick_dir)"
DEST="$DEST_DIR/$BIN_NAME"

if [ "$PRINT_TARGET" = 1 ]; then
  printf '%s\n' "$TARGET"
  exit 0
fi

# ---------------------------------------------------------------- helpers ---
fetch() {
  url="$1"; out="${2:--}"
  if command -v curl >/dev/null 2>&1; then
    if [ "$out" = "-" ]; then curl -fsSL "$url"; else curl -fsSL -o "$out" "$url"; fi
  elif command -v wget >/dev/null 2>&1; then
    if [ "$out" = "-" ]; then wget -qO- "$url"; else wget -qO "$out" "$url"; fi
  else
    die "neither curl nor wget is available (Termux: pkg install curl)"
  fi
}

sha256_of() {
  f="$1"
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$f" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$f" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then openssl dgst -sha256 "$f" | awk '{print $NF}'
  else die "no sha256 tool found (Termux: pkg install coreutils)"
  fi
}

latest_version() {
  tag="$(fetch "https://api.github.com/repos/${REPO}/releases/latest" \
        | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$tag" ] || die "no published release for ${REPO}
       (set LRM_MOBILE_REPO=owner/repo if you are installing from a fork)
       build from source instead:  sh install.sh --source"
  printf '%s\n' "$tag"
}

# Guard against the exact original failure, before we install rather than
# after: byte 16 of an ELF header is e_type, and Android requires 3.
elf_type_of() { od -An -tu2 -j16 -N2 "$1" 2>/dev/null | tr -d ' \n'; }
check_android_elf() {
  [ "$OS" = android ] || return 0
  command -v od >/dev/null 2>&1 || return 0
  t="$(elf_type_of "$1")"
  [ -z "$t" ] && return 0
  if [ "$t" = "2" ]; then
    die "the downloaded binary is ET_EXEC (e_type 2); Android's loader will
       refuse it with 'unexpected e_type: 2'. This asset was not built with
       GOOS=android -buildmode=pie. Refusing to install it.
       Please report this against ${REPO}."
  fi
}

# -------------------------------------------------------------------- plan ---
say "LRM Mobile installer"
say "  platform : ${OS}/${ARCH}${ANDROID_RELEASE:+  (Android ${ANDROID_RELEASE})}"
say "  termux   : $([ "$TERMUX" = 1 ] && echo yes || echo no)${PREFIX:+  PREFIX=$PREFIX}"
say "  asset    : ${ASSET}"
say "  install  : ${DEST}"
say ""

if [ "$DRY_RUN" = 1 ]; then
  say "dry run: would install ${ASSET} to ${DEST}"
  exit 0
fi

[ -e "$DEST" ] && [ "$FORCE" = 0 ] && die "$DEST already exists (use --force to replace it)"
mkdir -p "$DEST_DIR" || die "cannot create ${DEST_DIR}"

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t lrm-mobile)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

SRC=""
if [ -n "$FROM_FILE" ]; then
  [ -f "$FROM_FILE" ] || die "no such file: $FROM_FILE"
  SRC="$FROM_FILE"
  say "using local binary: ${FROM_FILE}"

elif [ "$USE_SOURCE" = 1 ]; then
  command -v go >/dev/null 2>&1 || die "Go is not installed (Termux: pkg install golang)"
  command -v git >/dev/null 2>&1 || die "git is not installed (Termux: pkg install git)"
  say "cloning ${REPO} (with the upstream LRM submodule) ..."
  git clone --depth 1 --recurse-submodules "https://github.com/${REPO}.git" "$TMP/src" >/dev/null 2>&1 \
    || die "clone failed"
  say "building (a few minutes on a phone) ..."
  ( cd "$TMP/src" && sh scripts/build.sh --out "$TMP/dist" "$TARGET" >/dev/null ) || die "build failed"
  SRC="$TMP/dist/$ASSET"

else
  # 5. download
  [ -n "$VERSION" ] || VERSION="$(latest_version)"
  [ "$VERSION" = latest ] && VERSION="$(latest_version)"
  if [ -n "$BASE_URL" ]; then
    URL="${BASE_URL%/}/${ASSET}"
    SUMS_URL="${BASE_URL%/}/SHA256SUMS.txt"
  else
    URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
    SUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS.txt"
  fi
  say "downloading ${ASSET} (${VERSION}) ..."
  fetch "$URL" "$TMP/$ASSET" || die "download failed: ${URL}"
  SRC="$TMP/$ASSET"

  # 6. verify
  if [ "$NO_VERIFY" = 1 ]; then
    warn "checksum verification skipped (--no-verify)"
  else
    say "verifying SHA-256 ..."
    SUMS="$(fetch "$SUMS_URL" - 2>/dev/null || true)"
    [ -n "$SUMS" ] || die "SHA256SUMS.txt could not be downloaded; refusing to install
       an unverified binary (override with --no-verify if you accept the risk)"
    want="$(printf '%s\n' "$SUMS" | awk -v a="$ASSET" '$2==a || $2=="*"a {print $1}' | head -1)"
    [ -n "$want" ] || die "no checksum listed for ${ASSET} in the release"
    got="$(sha256_of "$SRC")"
    [ "$want" = "$got" ] || die "checksum mismatch for ${ASSET}
       expected ${want}
       got      ${got}"
    say "  ok ${got}"
  fi
fi

check_android_elf "$SRC"

# 7. install
install -m 0755 "$SRC" "$DEST" 2>/dev/null || {
  cp "$SRC" "$DEST" || die "could not write ${DEST}"
  chmod 0755 "$DEST" || die "could not chmod ${DEST}"
}
say "installed: ${DEST}"

# 10. prove it runs *here*. This is the check the old installer never did,
#     and the reason the e_type failure only showed up later.
say ""
say "checking that it executes on this device ..."
if out="$("$DEST" --help 2>&1)"; then
  say "  ok  lrm --help works"
elif out="$("$DEST" help 2>&1)"; then
  say "  ok  lrm help works"
else
  case "$out" in
    *e_type*) die "the installed binary will not run on this device:
       $out
       This is the ET_EXEC/PIE problem — the wrong asset was installed." ;;
    *) die "the installed binary did not run: $out" ;;
  esac
fi
if pout="$("$DEST" platform 2>&1)"; then
  printf '%s\n' "$pout" | sed 's/^/  /'
fi

# 8 + 9. PATH
case ":${PATH}:" in
  *":${DEST_DIR}:"*)
    say ""
    say "PATH: ok — ${DEST_DIR} is already on your PATH."
    ;;
  *)
    profile="$HOME/.profile"
    [ "$TERMUX" = 1 ] && profile="$HOME/.bashrc"
    case "${SHELL:-}" in
      */zsh)  profile="$HOME/.zshrc" ;;
      */bash) profile="$HOME/.bashrc" ;;
    esac
    say ""
    say "PATH: ${DEST_DIR} is NOT on your PATH. Add it:"
    say ""
    say "  echo 'export PATH=\"${DEST_DIR}:\$PATH\"' >> ${profile}"
    say "  . ${profile}"
    say ""
    say "(Termux reads ~/.bashrc, not ~/.profile, for interactive shells.)"
    ;;
esac

say ""
say "Next:"
say "  lrm --help"
say "  mkdir ~/my-project && cd ~/my-project"
say "  lrm init --user your-name"
say "  lrm daemon              # start syncing with your other devices"
say "  lrm dashboard           # then open http://127.0.0.1:8787 in your browser"
