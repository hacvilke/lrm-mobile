# LRM Mobile

**LRM, running on your phone.** A mobile platform, build and installation
layer around [LRM](https://github.com/hacvilke/lrm) — the peer-to-peer,
server-less version control system — so that it installs and runs correctly
on Android under [Termux](https://termux.dev).

It is not a fork. There is no copied LRM source in this repository: the
upstream project is a pinned git submodule, and everything here is the
thousand or so lines of detection, paths, build recipe and installer that
upstream was missing for mobile.

---

## Install (Android / Termux)

```sh
curl -fsSL https://raw.githubusercontent.com/YOUR_GITHUB_USER/lrm-mobile/main/scripts/install.sh | sh
```

> Replace `YOUR_GITHUB_USER` with this repository's owner, or set
> `LRM_MOBILE_REPO=owner/lrm-mobile`. The placeholder is deliberate — no
> owner is hard-coded anywhere in this repo.

Then:

```sh
lrm --help

mkdir ~/my-project
cd ~/my-project
lrm init --user brandon
lrm daemon
```

The installer prints the exact PATH line to add if `$HOME/.local/bin` is not
already on your PATH, and it runs the binary before declaring success, so
you find out on the spot whether it works on *your* device.

Full guide: **[docs/TERMUX.md](docs/TERMUX.md)**.

---

## Why this exists

Installing upstream LRM in Termux fails:

```
$ lrm --help
error: "/data/data/com.termux/files/home/.local/bin/lrm" has unexpected e_type: 2
```

### The error, explained

`e_type` is a field in the ELF header saying what kind of binary this is.
`2` is `ET_EXEC` — a fixed-address executable. Android's loader requires
every executable to be **position-independent** (`ET_DYN`, `e_type` 3), so
that address-space randomisation works. It refuses an `ET_EXEC` file
outright.

The upstream installer detected the platform with `uname`. In Termux,
`uname -s` says `Linux` and `uname -m` says `aarch64` — both true, and both
useless here, because Android's C library is bionic, its loader is
`/system/bin/linker64`, and its PIE rules are stricter. So the installer
resolved `linux/arm64`, downloaded `lrm_linux_arm64`, and that asset was a
default Go build:

```
$ readelf -h lrm_linux_arm64 | grep Type
  Type:  EXEC (Executable file)        # e_type 2. Dead on arrival.
```

### How LRM Mobile avoids it

Two changes to the build, both required, neither sufficient alone:

```sh
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -buildmode=pie ...
```

| flag | why |
|---|---|
| `-buildmode=pie` | makes the binary `ET_DYN` (e_type 3). Fixes the error above. |
| `GOOS=android` | sets the ELF interpreter to `/system/bin/linker64`. **A `GOOS=linux` PIE build asks for `/lib/ld-linux-aarch64.so.1`, which does not exist on Android — so PIE alone is not enough.** |
| `CGO_ENABLED=0` | keeps it pure Go, so no NDK is needed to build and the binary is dependency-free. |

Result:

```
$ file lrm_android_arm64
ELF 64-bit LSB pie executable, ARM aarch64, ... interpreter /system/bin/linker64
```

…plus an installer that detects Android *separately from Linux*, and a
build that refuses to publish an `lrm_android_*` asset whose `e_type` is not
3.

The full write-up, including why `CGO_ENABLED=0` is safe here, is in
**[docs/E_TYPE.md](docs/E_TYPE.md)**.

---

## Relationship to the original LRM project

| | |
|---|---|
| Upstream | <https://github.com/hacvilke/lrm> |
| Included as | git submodule at `third_party/lrm`, pinned to a commit |
| LRM source copied into this repo | **none** |
| LRM core modified | **no** |

This module is named `github.com/lrm-project/lrm/mobile` so that Go's
internal-package rule lets it import upstream's `internal/...` packages
directly through a `replace` directive. One line of `go.mod` instead of a
30,000-line fork; upgrading LRM is a submodule bump with no merge
conflicts, ever. The reasoning is in
**[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)**.

Reading upstream first paid off, because most of what the mobile brief asks
for was **already true** of LRM's core and needed no change at all:

- state already resolves through `$LRM_HOME` → `os.UserHomeDir()` → `~/.lrm`
  (`internal/node`), never `/etc` or `/var`;
- `lrm dashboard` already binds `127.0.0.1`, already prints a URL instead of
  launching a browser, already warns if you bind wider (`internal/cli/dash.go`);
- nothing needs root; nothing imports C.

LRM's *core* was never the problem. The build and install layer was. So
this repository is a layer, and the core is untouched.

---

## Usage

```sh
lrm platform                # NEW: what this binary thinks it is running on
lrm init --user brandon     # start a repository
lrm status
lrm commit -m "from my phone"
lrm log --graph
lrm daemon                  # peer-to-peer sync
lrm dashboard               # local read-only web view
lrm share / join / sync / clone / send
lrm branch / merge / stash / rebase / blame / bisect / grep / notes ...
```

Every command except `lrm platform` is upstream LRM's, unmodified — the
whole git-compat surface and the `.lr` / `.lrq` languages included. Upstream's
own CLI sweep (`scripts/check-all.sh`, 90 checks) passes against a binary
built from this repository.

`lrm platform` is the diagnostic to paste into bug reports:

```
$ lrm platform
lrm (LRM Mobile) v0.1.0
  built for     android/arm64
  detected      android/arm64 (Termux, Android 14)
  termux        true
  PREFIX        /data/data/com.termux/files/usr
  HOME          /data/data/com.termux/files/home
  release asset lrm_android_arm64
  bin dir       /data/data/com.termux/files/home/.local/bin
  config dir    /data/data/com.termux/files/home/.lrm
  cache dir     /data/data/com.termux/files/usr/tmp/lrm
  tmp dir       /data/data/com.termux/files/usr/tmp
  PATH          ok
```

### The dashboard on mobile

```sh
lrm dashboard
```

Binds `127.0.0.1` only. Never exposed to the network, never auto-published.
No browser is launched — Termux has none — so it prints
`http://127.0.0.1:8787`, which you paste into your phone's browser.
`--port` changes it. The dashboard includes a read-only file browser over
your workspace, which is exactly why it must not be bound to `0.0.0.0` on a
device you carry around.

---

## Supported platforms

| Target | Asset | Status |
|---|---|---|
| Android arm64 (Termux) | `lrm_android_arm64` | **Built and header-verified in CI.** PIE, `GOOS=android`, pure Go. On-device execution: see the honesty note below. |
| Linux arm64 | `lrm_linux_arm64` | Built and tested |
| Linux amd64 | `lrm_linux_amd64` | Built and tested |
| Android x86_64 (emulator) | — | **Not shipped.** Go cannot build an android/amd64 PIE without cgo + NDK. Build from source in the emulator. |
| Android armv7 (32-bit) | — | Not built. Open an issue if you need it. |
| iOS | — | **Not supported.** No binary exists. See [docs/ROADMAP-IOS.md](docs/ROADMAP-IOS.md). |
| macOS / Windows | — | Use [upstream LRM](https://github.com/hacvilke/lrm). |

### An honesty note about "tested"

CI builds the Android binary and verifies its ELF header — `ET_DYN`,
aarch64, `/system/bin/linker64` — which proves the reported bug cannot
recur. CI **cannot execute** it: GitHub runners are x86_64, and
`qemu-aarch64` cannot provide Android's loader. The CLI and dashboard tests
run the real binary in a *simulated* Termux environment on Linux.

So: the incompatible-binary problem is fixed and machine-verified. Full
on-device behaviour — particularly the daemon surviving Android's
background-process killer — is **not** yet certified on hardware. The
checklist is in [docs/TESTING.md](docs/TESTING.md), and it will be ticked
off in release notes with the device and Android version named, not before.

---

## Building from source

```sh
git clone --recurse-submodules https://github.com/YOUR_GITHUB_USER/lrm-mobile
cd lrm-mobile
make build          # native binary -> ./lrm
make release        # all targets -> ./dist, with checksums, header-verified
make test           # go tests + installer shell tests
```

If you already cloned without `--recurse-submodules`:

```sh
git submodule update --init --recursive
```

One target only:

```sh
sh scripts/build.sh android_arm64
```

> `go install .../lrm-mobile/cmd/lrm@latest` does **not** work: `replace`
> directives are ignored for dependency modules. Clone with submodules.
> That is the price of not forking, and it is paid by contributors, not by
> users installing a release binary.

Building *inside* Termux needs none of the cross-compilation flags — the
native toolchain already targets Android:

```sh
pkg install golang git
go build -o ~/.local/bin/lrm ./cmd/lrm
```

---

## Mobile filesystem layout

No `/usr/local`, no `/etc`, no `/var`, no root — on any platform.

| What | Termux | Desktop Linux | Override |
|---|---|---|---|
| binary | `$HOME/.local/bin/lrm` | `$HOME/.local/bin/lrm` | `LRM_INSTALL_DIR` |
| machine state | `$HOME/.lrm` | `$XDG_CONFIG_HOME/lrm` or `$HOME/.lrm` | `LRM_HOME` |
| per-repo state | `<repo>/.lrm` | `<repo>/.lrm` | — |
| cache | `$PREFIX/tmp/lrm` | `$HOME/.cache/lrm` | `LRM_CACHE_DIR` |
| temp | `$PREFIX/tmp` | `/tmp` | `TMPDIR` |

`lrm platform` prints the resolved values. Android has no writable `/tmp`,
which is why `TMPDIR` is derived from `$PREFIX` there — a detail that
otherwise breaks every atomic rename.

---

## Troubleshooting

**`has unexpected e_type: 2`**
You are running a `linux_*` asset, not `android_arm64`. Reinstall:
`sh install.sh --force`, then check `lrm platform` says `detected android`.
The current installer refuses to install an `ET_EXEC` binary on Android, so
this should only happen with a manually placed binary.

**`lrm: command not found` right after a successful install**
`$HOME/.local/bin` is not on your PATH. Termux reads `~/.bashrc`, not
`~/.profile`:
`echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc && . ~/.bashrc`

**`No such file or directory` when the file is clearly there**
The binary's ELF interpreter does not exist — a `GOOS=linux` PIE build
asking for `/lib/ld-linux-aarch64.so.1`. Install the `android_arm64` asset.

**The daemon dies when the screen turns off**
Android killed it. `pkg install termux-api && termux-wake-lock`, and exempt
Termux from battery optimisation in system settings. LRM's sync resumes, so
restarting the daemon is safe.

**The dashboard will not open**
It is localhost-only by design. Copy the printed `http://127.0.0.1:PORT`
into the browser *on the same phone*.

**`no release for ...` from the installer**
No release has been published yet, or `LRM_MOBILE_REPO` is still the
placeholder. Use `sh install.sh --source`.

**Repositories on `/sdcard` behave oddly**
`/sdcard` is a FUSE filesystem with no POSIX permissions and coarse
timestamps. LRM works there but is slower. Prefer `$HOME`.

**Anything else** — open an issue and include the output of `lrm platform`.

---

## Development

```
lrm-mobile/
├── cmd/lrm/                 entry point (~120 lines) + end-to-end tests
├── internal/platform/       OS/arch/Android/Termux detection, paths, PATH
├── scripts/
│   ├── install.sh           the mobile-aware installer
│   ├── build.sh             the PIE / GOOS=android build recipe
│   ├── verify-elf.sh        blocks a bad Android asset from shipping
│   └── test-install.sh      installer tests against simulated hosts
├── .github/workflows/       ci.yml, release.yml
├── docs/                    E_TYPE.md, TERMUX.md, ARCHITECTURE.md,
│                            TESTING.md, ROADMAP-IOS.md
├── third_party/lrm/         upstream LRM (git submodule, unmodified)
├── go.mod
├── Makefile
├── LICENSE
└── README.md
```

Guidelines:

1. **Do not copy code from upstream.** If you need a change in LRM's core,
   send it upstream and bump the submodule.
2. **Platform-aware, never Android-only.** Every branch in
   `internal/platform` returns a correct answer on desktop too.
3. **Do not claim a platform works until it has been built, header-checked
   and, ideally, run on the real thing.** Update `docs/TESTING.md`
   truthfully.

Upgrading upstream LRM:

```sh
git -C third_party/lrm fetch --tags
git -C third_party/lrm checkout v0.4.0
go test ./... && make release
git commit -am "bump LRM to v0.4.0"
```

Releasing: tag `vX.Y.Z` and push. `release.yml` builds every target, runs
`verify-elf.sh` as a blocking gate, and publishes the binaries with
`SHA256SUMS.txt`.

---

## License and attribution

LRM Mobile is MIT licensed — see [LICENSE](LICENSE).

**LRM itself is not part of this license.** It is the work of the LRM
authors at <https://github.com/hacvilke/lrm>, included here as an unmodified
git submodule under its own license and copyright. All of the version
control, syncing, mesh networking, git compatibility and language work is
theirs; this repository contributes only the mobile platform, build and
install layer around it.

If LRM Mobile is useful to you, credit the upstream project first.
