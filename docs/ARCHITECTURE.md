# How LRM Mobile relates to LRM

## The rule this repository follows

> Do not fork and duplicate the LRM codebase.

It does not. There is **zero** copied LRM source in this repository. Run
`git ls-files` and you will find about a thousand lines of Go, all of it
platform detection, path resolution and a CLI entry point.

## The mechanism

Upstream LRM is included as a **git submodule** at `third_party/lrm`,
pinned to a commit. The whole of LRM's implementation — `internal/cli`,
`internal/daemon`, `internal/sync`, `internal/dash`, `internal/merkle`,
`internal/gitcompat`, the `.lr` language, all of it — is compiled from
there, unmodified.

That normally would not work. Everything in LRM lives under `internal/`,
and Go's internal rule says a package under `.../lrm/internal/` may only be
imported by code whose import path begins with `.../lrm/`. A module called
`github.com/someone/lrm-mobile` could not import it, which is exactly the
pressure that pushes people into forking.

So this module is named:

```
module github.com/lrm-project/lrm/mobile
```

with

```
require github.com/lrm-project/lrm v0.0.0
replace github.com/lrm-project/lrm => ./third_party/lrm
```

The path sits *under* the upstream module path, the internal rule is
satisfied by prefix, and `cmd/lrm/main.go` can write:

```go
import "github.com/lrm-project/lrm/internal/cli"
```

One `replace` line and a submodule, instead of a 30,000-line fork.

### Consequences you should know about

- `go install github.com/lrm-project/lrm/mobile/cmd/lrm@latest` does **not**
  work, because `replace` directives are ignored for dependency modules.
  Building from source means cloning with `--recurse-submodules`. This is
  the price of not forking, and it is a price paid by maintainers, not by
  users, who install a prebuilt binary.
- Upgrading to a new LRM release is `git -C third_party/lrm fetch && git -C
  third_party/lrm checkout <tag>` followed by a commit. Nothing to merge,
  no conflicts, ever.

## What LRM Mobile adds

```
cmd/lrm/main.go                — thin entry point (~120 lines)
internal/platform/detect.go    — OS/arch/Android/Termux detection, paths
scripts/install.sh             — mobile-aware installer
scripts/build.sh               — the PIE/GOOS=android build recipe
scripts/verify-elf.sh          — blocks a bad Android asset from shipping
.github/workflows/             — CI and release
```

### `cmd/lrm`

Calls `cli.Run(os.Args[1:])` — upstream's entry point — after doing three
things upstream cannot do for itself:

1. resolves `LRM_HOME` and `TMPDIR` through `internal/platform` before
   handing off, so a shell with no `$HOME` (Termux:Widget, `adb shell`)
   gets a sentence instead of a stack trace;
2. adds `lrm platform`, the diagnostic to paste into bug reports;
3. prints the "no browser will be launched, copy this URL" note before
   `lrm dashboard`.

Every other command is upstream's, byte for byte.

### `internal/platform`

Answers "what is this machine" and "where may I write". It is
platform-*aware*, not Android-special-cased: every function returns a
correct answer on desktop Linux, macOS and Windows too, and the Android
branch is one case among several. That was a deliberate constraint — an
`if android { ... }` sprinkled through the code would be a maintenance
liability and would drift.

## What LRM Mobile did *not* need to change

A pleasant discovery while reading the upstream source: LRM was already
mostly portable. Specifically —

- **Home directory.** `internal/node/node.go` already resolves state via
  `$LRM_HOME`, falling back to `os.UserHomeDir()` and `~/.lrm`. It never
  hard-codes `/etc` or `/var`. On Termux `~/.lrm` is
  `/data/data/com.termux/files/home/.lrm`, which is correct. Nothing to
  patch.
- **Dashboard binding.** `internal/cli/dash.go` already defaults to
  `127.0.0.1`, already refuses to assume a browser, already prints the URL,
  and already warns loudly if you bind beyond localhost. That is exactly
  the mobile-safe behaviour the brief asked for; it was already there.
- **Root.** Nothing in LRM requires privilege.
- **cgo.** Nothing imports C.

So the honest summary is that LRM's *core* was never the problem. The
problem was entirely in the **build and install layer** — one missing
`-buildmode=pie` and one wrong `GOOS`. That is why this repository is a
layer and not a fork.

## Room for iOS

`platform.OS` has an `IOS` constant and `Info.IsMobile()` already returns
true for it; `scripts/build.sh` has an `ios` case in `build_one` that
currently skips with an explanation, and the release matrix is a plain
string list. Adding iOS means adding a target name and a code-signing step
— no restructuring. Nothing is claimed for iOS today; see `TESTING.md`.
