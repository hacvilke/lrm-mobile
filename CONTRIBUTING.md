# Contributing to LRM Mobile

Thanks for considering it. This repository is small on purpose, and the
rules below exist to keep it that way.

## The one rule that matters

**Do not copy code from LRM into this repository.**

LRM Mobile is a platform, build and installation layer. The engine —
version control, syncing, the mesh, the `.lr` language — lives in
[hacvilke/lrm](https://github.com/hacvilke/lrm) and is consumed here as a
pinned git submodule at `third_party/lrm`, compiled unmodified.

If you need a change in the engine, **send it upstream** and then bump the
submodule here. A patch that duplicates upstream code into this repository
will be declined no matter how good it is, because the moment the two
copies diverge this project stops being maintainable.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for how the module path
trick lets us import upstream's `internal/` packages without forking.

## Getting set up

```sh
git clone --recurse-submodules https://github.com/hacvilke/lrm-mobile
cd lrm-mobile
make test          # go tests + installer shell tests
make release       # cross-build every target into ./dist
make verify        # check the Android binaries are PIE
```

If you cloned without `--recurse-submodules`:

```sh
git submodule update --init --recursive
```

Note that `go install .../cmd/lrm@latest` does **not** work: `replace`
directives are ignored for dependency modules. Clone with submodules. That
is the price of not forking, and it is paid by contributors rather than by
users installing a release binary.

## How to propose a change

`main` is protected. **Nobody pushes to it directly — including the
maintainers.** Everything goes through a pull request with passing CI.

```sh
git checkout -b fix/some-thing
# ... work ...
make test
git commit
git push -u origin fix/some-thing
gh pr create          # or open the PR in the web UI
```

### Commit messages

Explain *why*, not *what* — the diff already says what. The commit that
fixed the Android argv bug is a reasonable model: what was observed, what
the platform actually does, why the chosen fix is correct, and what it
deliberately does not do.

Use a `type(scope): summary` first line where it helps
(`fix(android):`, `feat(mobile):`, `docs:`, `chore:`).

### Releasing

Branch protection applies to `main`, not to tags, so the release flow is
unchanged: merge the PR, then push a tag and let the workflow publish.

```sh
git tag -a v0.3.0 -m "v0.3.0 — summary"
git push origin v0.3.0
```

`release.yml` builds every target, runs `verify-elf.sh` as a blocking
gate, and uploads the binaries with `SHA256SUMS.txt`. Never upload release
assets by hand: a tag push triggers the workflow, which rebuilds and
replaces anything already attached, and the window between the two leaves
users with a checksum that does not match what they just downloaded.

## Standards

- **`gofmt` clean, `go vet` clean, tests pass.** CI enforces all three.
- **Platform-aware, never Android-only.** Every branch in
  `internal/platform` must return a correct answer on desktop Linux,
  macOS and Windows too. An `if android { ... }` sprinkled through the
  code is a maintenance liability and will drift.
- **No new dependencies.** LRM has zero; so does this. If you think you
  need one, open an issue first and make the case.
- **Tests for platform behaviour go in `internal/platform`; anything that
  touches a real binary goes in `cmd/lrm`.** The ELF-header tests are not
  optional decoration — they are the regression tests for the bug this
  project exists to fix.

## Do not claim a platform works until it does

This is the rule this project was founded on. A binary that compiles is
not a binary that runs; a checksum proves you downloaded the right bytes,
not that the kernel will load them.

If you add or change platform support:

1. Build it.
2. Check the artefact (`scripts/verify-elf.sh`, or the equivalent).
3. Run it on the real thing if you possibly can.
4. Update [docs/TESTING.md](docs/TESTING.md) **honestly** — there is a
   section for "verified by construction, not by execution" and a
   checklist of things nobody has confirmed on hardware. Putting your
   change in the right section is part of the contribution.

A PR that moves an item into "confirmed on real hardware" should say which
device and which OS version.

## The network scanner

`lrm scan` is a diagnostic for networks you administer. Contributions that
widen its reach — scanning ranges the device is not joined to without an
explicit flag, removing the subnet-size guard, removing the warning on
`--cidr` — will be declined. Making it *faster* or *more accurate* within
those limits is very welcome.

## Reporting bugs

Open an issue using the bug template and include the output of:

```sh
lrm platform
```

That one command tells us the build, the detected platform, whether the
Termux loader workaround is active, and every resolved path. Without it,
most mobile bug reports are unactionable.

## Licensing of contributions

This project is licensed under the [Apache License 2.0](LICENSE). Under
section 5 of that licence, any contribution you intentionally submit for
inclusion is licensed under the same terms, with no separate paperwork —
there is no CLA to sign.

Only submit work you have the right to license that way.
