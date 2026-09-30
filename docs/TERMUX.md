# Android / Termux guide

## Install Termux

Install Termux from **F-Droid** or GitHub, not the Play Store — the Play
Store build is an abandoned old version with known breakage.

- F-Droid: <https://f-droid.org/packages/com.termux/>
- GitHub: <https://github.com/termux/termux-app/releases>

Then:

```sh
pkg update && pkg upgrade
pkg install curl
```

## Install LRM Mobile

```sh
curl -fsSL https://raw.githubusercontent.com/hacvilke/lrm-mobile/main/scripts/install.sh | sh
```

The installer detects Android, picks `lrm_android_arm64`, verifies its
SHA-256, installs to `$HOME/.local/bin/lrm`, tells you if that directory is
not on your PATH, and then runs the binary to prove it executes on *your*
device before declaring success.

If PATH needs fixing it prints the exact line. In Termux that is `~/.bashrc`
— Termux does not read `~/.profile` for interactive shells, which is why
generic "add it to your .profile" advice tends to silently do nothing here.

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
. ~/.bashrc
lrm --help
```

## First repository

```sh
mkdir ~/my-project && cd ~/my-project
lrm init --user brandon
echo "hello from my phone" > notes.md
lrm status
lrm commit -m "first commit from Android"
lrm log
```

## The daemon

```sh
lrm daemon
```

Android aggressively kills background processes. Two things help:

```sh
pkg install termux-api
termux-wake-lock          # stops the CPU sleeping; drains battery
```

and, in Android's system settings, exempt Termux from battery
optimisation. Even then, treat the daemon as best-effort on a phone: when
the OS kills it, `lrm daemon` again resumes from where it left off — LRM's
sync is resumable by design.

To keep it running across Termux sessions, `pkg install tmux` and run the
daemon inside a tmux session.

## The dashboard

```sh
lrm dashboard
```

It binds `127.0.0.1` only and is not reachable from your network. No
browser is launched — Termux has none — so copy the printed URL:

```
http://127.0.0.1:8787
```

into Chrome or Firefox on the same phone. A different port:

```sh
lrm dashboard --port 9000
```

Do not bind it to `0.0.0.0` on a phone you carry around. The dashboard
includes a read-only file browser over your workspace; on public Wi-Fi that
is a data leak. LRM prints a warning if you do it anyway.

## Storage and paths

Everything lives under `$HOME`, which in Termux is
`/data/data/com.termux/files/home`:

| What | Where |
|---|---|
| binary | `$HOME/.local/bin/lrm` |
| machine state / device identity | `$HOME/.lrm` |
| per-repository state | `<repo>/.lrm` |
| cache | `$PREFIX/tmp/lrm` |
| temp | `$PREFIX/tmp` |

All of these are overridable: `LRM_INSTALL_DIR`, `LRM_HOME`,
`LRM_CACHE_DIR`, `TMPDIR`. Run `lrm platform` to see the resolved values.

### Working on shared storage

```sh
termux-setup-storage      # grants access, creates ~/storage
cd ~/storage/shared/Documents
```

This works, but `/sdcard` is a FUSE filesystem (`sdcardfs`/`esdfs`) with no
POSIX permissions, unreliable `mtime` granularity, and no hard links. LRM
content-addresses everything and does not rely on link counts, so it
functions — but it is slower and it is not where a maintainer would test
first. Keep repositories in `$HOME` when you can.

## Building on the phone

```sh
pkg install golang git
git clone --recurse-submodules https://github.com/hacvilke/lrm-mobile
cd lrm-mobile
go build -o ~/.local/bin/lrm ./cmd/lrm
```

Building *in* Termux needs no special flags — the native toolchain already
targets Android correctly. The PIE and `GOOS=android` gymnastics in
`scripts/build.sh` only matter when cross-compiling from a desktop.

Expect a few minutes and a warm phone.
