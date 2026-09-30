# Testing, and what has actually been verified

This project makes a claim about a platform it is hard to run CI on, so the
honest thing is to say precisely which claims are machine-verified, which
are verified by construction, and which still need a human with a phone.

## Machine-verified in CI

| Claim | How |
|---|---|
| Termux is detected as Android, not Linux | `internal/platform` unit tests + `scripts/test-install.sh` shell tests |
| Desktop Linux is still detected as Linux | same |
| `aarch64`/`arm64`/`armv8l` all normalise to arm64 | `TestNormalizeArch` |
| The Android binary is `ET_DYN` (PIE) | `TestAndroidBuildIsPIE`, `scripts/verify-elf.sh`, CI `smoke-android` job |
| The Android binary uses `/system/bin/linker64` | same |
| The old `linux_arm64` asset really is `ET_EXEC` | `TestLinuxArm64BuildReproducesTheOriginalBug` |
| Config/cache/bin paths never touch `/usr/local`, `/etc`, `/var` | `TestTermuxPathsStayUnderHome`, `TestPlatformCommand` |
| `$HOME`-relative paths are used, and overridable | `TestPathOverridesAreHonoured` |
| PATH presence/absence is detected correctly | `TestInPath`, installer tests |
| The installer installs to `$HOME/.local/bin/lrm` without root | `scripts/test-install.sh` end-to-end case |
| The installer runs an executable check after installing | same |
| The installer refuses a file that does not execute | same |
| `lrm --help`, `version`, `init`, `status`, `commit`, `log` run | `cmd/lrm` integration tests |
| `lrm dashboard` starts, serves, and prints a `127.0.0.1` URL | `TestDashboardStartsAndBindsLocalhostOnly` |
| The dashboard is **not** reachable on a LAN address | same test dials the host's routable IP and requires a refusal |

The CLI and dashboard tests run the real binary as a subprocess, in a
simulated Termux environment (`HOME`, `PREFIX`, `TERMUX_VERSION` all set to
a temp tree), on a Linux CI runner.

## Verified by construction, not by execution

The `android/arm64` binary is **built and inspected** in CI, but CI cannot
execute it: GitHub's runners are x86_64 Linux, and `qemu-aarch64` cannot
supply `/system/bin/linker64`, so it cannot load an Android PIE either.

What CI proves is that the file is a valid aarch64 `ET_DYN` object naming
Android's loader — i.e. that the specific failure reported in the original
bug cannot occur. It does not prove the program behaves correctly on a real
device.

## Confirmed on real hardware

**Android 16, arm64, Termux, non-rooted. 2026-09-30.**

- [x] The binary **loads and executes.** No `e_type: 2`. The PIE +
      `GOOS=android` build is correct on a real device, not just in a
      header dump.
- [x] The installer detects the device correctly: `platform : android/arm64
      (Android 16)`, `termux : yes`, `PREFIX=/data/data/com.termux/files/usr`.
- [x] It selects `lrm_android_arm64`, not `lrm_linux_arm64`.
- [x] SHA-256 verification passes against the published release.
- [x] Installs to `$HOME/.local/bin/lrm` with no root.
- [x] The post-install executable check **caught a real bug** (see below)
      instead of reporting a false success.

Re-verified on the same device after v0.1.1:

- [x] `lrm platform` runs and reports `launched via /system/bin/linker64
      (argv corrected)` — the argv injection is detected and undone on
      real hardware.
- [x] `lrm --help` works.
- [x] `$HOME/.local/bin` detected as already on PATH.
- [x] All resolved paths land under `$HOME` / `$PREFIX`:
      config `~/.lrm`, cache `$PREFIX/tmp/lrm`, tmp `$PREFIX/tmp`.
- [x] `platform.Executable()` returns the real binary path, not the
      loader's.

That run also showed `getprop` was not being found from inside the Go
process (Termux's PATH does not always include `/system/bin`), so the
Android version was missing from `lrm platform` though the shell installer
had it. Fixed in v0.1.2 with absolute-path fallbacks.

That run also found the argv defect fixed in v0.1.1: Termux launches
binaries through `/system/bin/linker64`, which injects the executable's
path as `os.Args[1]`, so every subcommand was read as an unknown command.
Covered now by `TestFixArgsRemovesTheLinkerArgument` and friends.

## Not yet verified — needs a real device

Please do not read this repository as claiming a fully device-tested
Android release until a maintainer has ticked these off on hardware and
recorded the device and Android version in the release notes:

- [ ] `lrm init` / `lrm status` / `lrm commit` on-device
- [ ] `lrm daemon` surviving Android's background process limits
      (expect this to need Termux's wake-lock: `termux-wake-lock`)
- [ ] `lrm dashboard` opened in the phone's browser
- [ ] peer sync between a phone and a desktop on the same Wi-Fi
- [ ] behaviour when Android kills the daemon on screen-off

**iOS is not supported and no iOS binary is produced.** The build system
has a slot for it and `internal/platform` has an `IOS` constant, so adding
it later does not mean restructuring anything — but iOS has no general
process-execution model for a CLI like this, and nothing has been built or
tested. Saying otherwise would be a lie.

## `lrm scan` and the daemon helpers

Machine-verified: subnet maths, host enumeration, IP ordering, port-list
parsing, MAC/OUI lookup including the randomised-address and
locally-administered-vendor cases, Wi-Fi band/channel/security decoding,
signal rendering, neighbour-table parsing, TCP probe behaviour (including
"refused counts as alive"), the self/gateway labelling fix, the oversized-
subnet guard, and daemon flag splitting.

Exercised for real on a Linux host: `lrm scan --fast`, `--full`, `--json`
and `--cidr` all run end to end and produce correct output.

**Not verified on a phone:** the Wi-Fi survey (needs Termux:API and
Location on a real device), `/proc/net/arp` behaviour under Android 10+
restrictions, and whether `--supervise` actually survives Android's
background killer over hours. The mechanisms are implemented and tested;
their effectiveness on hardware is unmeasured. Do not read the presence of
these features as a claim that Android will cooperate.

## Running the tests

```sh
git submodule update --init --recursive
make test                 # go tests + installer shell tests
make release && make verify
go test ./... -run Android -v
```

## Manual on-device procedure

In Termux:

```sh
pkg install curl
curl -fsSL https://raw.githubusercontent.com/hacvilke/lrm-mobile/main/scripts/install.sh | sh
lrm platform              # paste this into any bug report
lrm --help
mkdir ~/my-project && cd ~/my-project
lrm init --user brandon
lrm status
lrm daemon &
lrm dashboard             # then open the printed 127.0.0.1 URL
```

If anything fails, `lrm platform` plus the output of
`uname -a; echo $PREFIX; getprop ro.build.version.release` is what a
maintainer needs.
