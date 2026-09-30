# iOS: what would be involved

**Status: not supported. No binary is produced. Nothing has been tested.**

This file exists so that the design decision is recorded and so that adding
iOS later is a small change rather than a restructuring.

## Why it is not just another GOOS

Android and iOS are not symmetric. Termux is a real POSIX userland with a
shell, a package manager, and `execve`. iOS has none of that for
third-party code:

- Apps are sandboxed and cannot `fork`/`exec` arbitrary binaries.
- Code signing and the lack of W^X exceptions mean a downloaded executable
  cannot be run at all on a stock device.
- There is no user-visible filesystem shared between apps; `$HOME` is the
  app container.

So "install a CLI with curl | sh" is not achievable on iOS in the way it is
on Android, and pretending otherwise would be dishonest.

## The three realistic routes

1. **a-Shell / iSH** — sandboxed terminal apps. a-Shell links commands in
   as libraries rather than executing separate binaries, so LRM would need
   to be built as a static library with a registered entry point. iSH runs
   an x86 emulator over Alpine; a `linux/386` build *may* run there, slowly
   and with unreliable networking. Cheapest experiment: try
   `GOOS=linux GOARCH=386` in iSH and report what happens.
2. **A wrapper app** — build LRM as an `xcframework` with `gomobile bind`
   and ship a SwiftUI shell around it. This gives a real, App Store-able
   product, but the CLI surface becomes an internal API and the daemon has
   to live inside iOS background-task budgets, which are far stricter than
   Android's.
3. **Jailbreak / sideload only** — a `GOOS=ios GOARCH=arm64` build. Not
   worth shipping.

Route 2 is the only one that leads to something a normal person can use.

## What is already in place

- `platform.IOS` exists and `Info.IsMobile()` returns true for it, so all
  the `$HOME`-relative path logic and the dashboard's URL-instead-of-browser
  behaviour already apply.
- `scripts/build.sh` has an `ios` branch in `build_one` which currently
  skips with a message. Adding a target means adding `ios_arm64` to the
  target list and a signing step.
- The release matrix in `.github/workflows/release.yml` is a plain list.

Nothing about the architecture needs to change. That was the point.
