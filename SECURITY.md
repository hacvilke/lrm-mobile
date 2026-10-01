# Security Policy

## Reporting a vulnerability

**Please do not open a public issue.**

Use GitHub's private reporting:
<https://github.com/hacvilke/lrm-mobile/security/advisories/new>

Include what you did, what happened, what you expected, and the output of
`lrm platform`. If a proof of concept needs a second device, say so.

You should get an acknowledgement within a few days. Fixes are released as
a new tagged version with the advisory published alongside.

## Scope

This repository is the **mobile platform, build and installation layer**.
In scope:

- `scripts/install.sh` — download, checksum verification, the ELF
  pre-flight check, where it writes
- `internal/platform` — platform detection, path resolution, the Termux
  argv correction, child-process execution
- `internal/scan` — the network scanner
- the release pipeline, and the integrity of published binaries

**The LRM engine is out of scope here** — it lives in
[hacvilke/lrm](https://github.com/hacvilke/lrm) and has its own
[SECURITY.md](https://github.com/hacvilke/lrm/blob/main/SECURITY.md).
Report engine issues (sync, crypto, the dashboard's file browser, the
`.lr` runtime) there.

## Things worth knowing before you report

These are deliberate, documented behaviours, not vulnerabilities:

- **The dashboard binds `127.0.0.1` and is not reachable from the
  network.** Binding it wider requires an explicit `--host` and prints a
  warning.
- **`lrm scan` is an active network scanner.** It is confined to the
  subnets the device is joined to unless `--cidr` is passed, which warns.
  That it can scan at all is the point of the feature.
- **`--no-verify` skips checksum verification.** It exists for offline
  use and says what it is doing.

Genuine issues we would very much like to hear about:

- a way to make the installer write outside `$HOME`, or to install a
  binary whose checksum does not match
- a way to make `lrm scan` reach a network the device is not joined to
  without `--cidr`
- anything that causes the dashboard to bind beyond localhost by default
- a path where the Termux argv correction drops or injects a user argument

## Supported versions

Only the latest release receives fixes.
