#!/bin/sh
# test-install.sh — exercise scripts/install.sh against simulated hosts.
#
# The Go tests cover the binary; this covers the shell, which is the part
# that actually got the platform wrong the first time. Each case fakes an
# environment and asserts which release asset the installer would pick.
#
# Usage: scripts/test-install.sh

set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL="$ROOT/scripts/install.sh"
PASS=0; FAIL=0
FAKE="$(mktemp -d)"
trap 'rm -rf "$FAKE"' EXIT INT TERM

# fake_uname makes `uname -s` / `uname -m` answer whatever a case needs, and
# optionally provides a `getprop`, by putting shims first on PATH.
make_shims() {
	s="$1"; m="$2"; release="${3:-}"
	d="$FAKE/bin"; rm -rf "$d"; mkdir -p "$d"
	cat > "$d/uname" <<EOF
#!/bin/sh
case "\$1" in
  -s) echo "$s" ;;
  -m) echo "$m" ;;
  *)  echo "$s" ;;
esac
EOF
	chmod +x "$d/uname"
	if [ -n "$release" ]; then
		cat > "$d/getprop" <<EOF
#!/bin/sh
[ "\$1" = ro.build.version.release ] && echo "$release"
EOF
		chmod +x "$d/getprop"
	fi
	printf '%s\n' "$d"
}

# want NAME EXPECTED_TARGET  -- remaining args are env assignments
want() {
	name="$1"; expect="$2"; shift 2
	got="$(env -i PATH="$SHIM:/usr/bin:/bin" HOME="$FAKE/home" "$@" \
		sh "$INSTALL" --print-target 2>&1 || true)"
	if [ "$got" = "$expect" ]; then
		PASS=$((PASS+1)); echo "  ok   $name -> $got"
	else
		FAIL=$((FAIL+1)); echo "  FAIL $name: got '$got', want '$expect'"
	fi
}

# wantfail NAME SUBSTRING -- installer must refuse, with a useful message
wantfail() {
	name="$1"; sub="$2"; shift 2
	got="$(env -i PATH="$SHIM:/usr/bin:/bin" HOME="$FAKE/home" "$@" \
		sh "$INSTALL" --print-target 2>&1 || true)"
	if printf '%s' "$got" | grep -q "$sub"; then
		PASS=$((PASS+1)); echo "  ok   $name (refused as expected)"
	else
		FAIL=$((FAIL+1)); echo "  FAIL $name: expected refusal mentioning '$sub', got: $got"
	fi
}

echo "== install.sh platform selection =="

# The original bug: Termux answers Linux/aarch64 to uname. It must NOT be
# resolved to linux_arm64.
SHIM="$(make_shims Linux aarch64 14)"
want "termux arm64 (the bug)" "android_arm64" \
	PREFIX=/data/data/com.termux/files/usr TERMUX_VERSION=0.118.0
want "termux arm64, no TERMUX_VERSION" "android_arm64" \
	PREFIX=/data/data/com.termux/files/usr
want "android arm64, not termux (getprop only)" "android_arm64"

# Without any Android signal the same uname output is plain Linux.
SHIM="$(make_shims Linux aarch64)"
want "raspberry pi / linux arm64" "linux_arm64"
want "linux arm64 with a non-termux PREFIX" "linux_arm64" PREFIX=/usr/local

SHIM="$(make_shims Linux x86_64)"
want "desktop linux amd64" "linux_amd64"

SHIM="$(make_shims Linux x86_64 13)"
wantfail "android x86_64 emulator" "no prebuilt binary"

SHIM="$(make_shims Linux armv7l 9)"
wantfail "32-bit android" "32-bit Android"

SHIM="$(make_shims Darwin arm64)"
wantfail "macOS" "upstream LRM"

echo ""
echo "== install.sh end to end (local binary) =="
BIN="$ROOT/dist/lrm_linux_amd64"
if [ -x "$BIN" ] && [ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ]; then
	H="$FAKE/e2e"; mkdir -p "$H"
	out="$(HOME="$H" sh "$INSTALL" --from "$BIN" --force 2>&1)" || {
		FAIL=$((FAIL+1)); echo "  FAIL end-to-end install: $out"; }
	if [ -x "$H/.local/bin/lrm" ]; then
		PASS=$((PASS+1)); echo "  ok   installed to \$HOME/.local/bin/lrm"
	else
		FAIL=$((FAIL+1)); echo "  FAIL binary not at \$HOME/.local/bin/lrm"
	fi
	if printf '%s' "$out" | grep -q "lrm --help works"; then
		PASS=$((PASS+1)); echo "  ok   post-install executable check ran"
	else
		FAIL=$((FAIL+1)); echo "  FAIL no post-install executable check"
	fi
	if printf '%s' "$out" | grep -q "is NOT on your PATH"; then
		PASS=$((PASS+1)); echo "  ok   PATH advice given when missing"
	else
		FAIL=$((FAIL+1)); echo "  FAIL no PATH advice"
	fi
	# And with the dir on PATH it must say so instead.
	out2="$(HOME="$H" PATH="$H/.local/bin:$PATH" sh "$INSTALL" --from "$BIN" --force 2>&1)"
	if printf '%s' "$out2" | grep -q "PATH: ok"; then
		PASS=$((PASS+1)); echo "  ok   PATH detected when present"
	else
		FAIL=$((FAIL+1)); echo "  FAIL PATH not detected when present"
	fi
	# A deliberately corrupt "binary" must be refused, not installed.
	printf 'not an elf' > "$FAKE/junk"
	if HOME="$H" sh "$INSTALL" --from "$FAKE/junk" --force >/dev/null 2>&1; then
		FAIL=$((FAIL+1)); echo "  FAIL a non-executable file was accepted"
	else
		PASS=$((PASS+1)); echo "  ok   refuses a file that does not execute"
	fi
else
	echo "  skip end-to-end (needs dist/lrm_linux_amd64 on linux/amd64; run scripts/build.sh)"
fi

echo ""
echo "$PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
