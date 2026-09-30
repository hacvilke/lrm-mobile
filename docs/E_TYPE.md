# `unexpected e_type: 2`

This is the error that made LRM Mobile necessary. It is worth understanding
properly, because the obvious fix is only half of it.

```
$ lrm --help
error: "/data/data/com.termux/files/home/.local/bin/lrm" has unexpected e_type: 2
```

## What the message means

`e_type` is a field at byte offset 16 of every ELF file's header. It says
what kind of ELF object this is:

| value | name | meaning |
|---|---|---|
| 1 | `ET_REL` | relocatable object (`.o`) |
| **2** | **`ET_EXEC`** | **a fixed-address executable** |
| 3 | `ET_DYN` | shared object, or a position-independent executable (PIE) |

Android's loader, `bionic`, requires every executable to be **PIE** — that
is, `ET_DYN`. This has been enforced since Android 5.0 for the system and
has been a hard error for ordinary executables since Android 10. The reason
is ASLR: an `ET_EXEC` binary insists on being mapped at the fixed addresses
baked into it at link time, which defeats address-space randomisation. So
when Termux is handed an `ET_EXEC` file, the loader does not try and fail —
it refuses up front, with exactly the message above.

The message is not about LRM. It is about the *file format* of the binary
the old installer downloaded.

## Why the old installer downloaded the wrong thing

Upstream LRM's installer detected the platform like this:

```sh
case "$(uname -s)" in Linux) echo linux ;; esac
case "$(uname -m)" in arm64|aarch64) echo arm64 ;; esac
```

Inside Termux, `uname -s` prints `Linux` and `uname -m` prints `aarch64`.
That is not a bug in Termux — Android *is* a Linux kernel, and Termux is a
genuine Linux userland running on it. But the C library is bionic, not
glibc, the loader is `/system/bin/linker64`, not `/lib/ld-linux-*.so`, and
the rules about PIE are stricter. `uname` cannot tell you any of that.

So the installer resolved `linux/arm64`, fetched `lrm_linux_arm64`, and
that asset was produced by:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" ...
```

A default Go build is **`ET_EXEC`**. Confirmed on this toolchain:

```
$ readelf -h lrm_linux_arm64 | grep Type
  Type:  EXEC (Executable file)
```

`EXEC` is `e_type` 2. Everything after that follows.

## The fix, part one: PIE

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildmode=pie ...
```

```
$ readelf -h lrm_pie | grep Type
  Type:  DYN (Position-Independent Executable file)
```

`e_type` is now 3. The first error is gone — and if you stop here you will
hit a second one, which is why "just add `-buildmode=pie`" is not the whole
answer.

## The fix, part two: the loader path

A PIE is dynamically loaded, so its `PT_INTERP` program header names the
loader that must map it. Look at what the `GOOS=linux` PIE asks for:

```
$ file lrm_pie
ELF 64-bit LSB pie executable, ARM aarch64, ... interpreter /lib/ld-linux-aarch64.so.1
```

`/lib/ld-linux-aarch64.so.1` is glibc's dynamic loader. **It does not exist
on Android.** There is no `/lib` you can rely on, and bionic's loader lives
at `/system/bin/linker64`. Running that file in Termux fails again — this
time with a "No such file or directory" style error that misleadingly points
at the binary you can plainly see is there.

Building with `GOOS=android` fixes the header:

```sh
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -buildmode=pie ...
```

```
$ file lrm_android_arm64
ELF 64-bit LSB pie executable, ARM aarch64, ... interpreter /system/bin/linker64
```

`ET_DYN`, aarch64, and the loader that actually exists on the device. That
is the asset LRM Mobile ships as `lrm_android_arm64`.

## Why `CGO_ENABLED=0`, and why that is not a compromise

`GOOS=android` normally implies cgo, because Go's android port expects to
link against bionic through the NDK. That would mean requiring an Android
NDK on every build machine and in CI. LRM does not need it: nothing in the
codebase imports C, and the two standard-library features that *would*
prefer cgo have pure-Go fallbacks that are better for us anyway:

- **DNS resolution** — with cgo off, Go uses its own resolver rather than
  bionic's `getaddrinfo`. LRM is a peer-to-peer system that does mDNS,
  STUN, NAT-PMP and hole punching itself; the pure-Go resolver is the
  predictable choice.
- **`os/user`** — with cgo off, Go reads `/etc/passwd` instead of calling
  NSS. On Android there is no `/etc/passwd`, so neither works, and LRM does
  not look up users; it identifies devices by keypair.

So `CGO_ENABLED=0` gives a statically linked, dependency-free, pure-Go
binary that cross-compiles from any machine with plain Go installed — and
the Go linker still emits the correct Android ELF headers. Documented here
because it is the one non-obvious flag in the build.

## The one target this does not work for

`GOOS=android GOARCH=amd64 -buildmode=pie` fails:

```
android/amd64 requires external (cgo) linking, but cgo is not enabled
```

Go cannot produce an amd64 Android PIE without an NDK toolchain, and a
non-PIE one would be rejected for the `e_type` reason above. LRM Mobile
therefore ships **no** x86_64 Android binary. If you are on an x86_64
emulator image, `pkg install golang` and use `install.sh --source`. This is
a documented limitation, not an oversight.

## How this cannot silently regress

Three layers, all blocking:

1. `scripts/verify-elf.sh` reads `e_type` straight out of the header and
   fails the build if an `lrm_android_*` asset is not `3`. It runs as part
   of `scripts/build.sh`, so it cannot be skipped by accident.
2. `TestAndroidBuildIsPIE` in `cmd/lrm/integration_test.go` cross-compiles
   for android/arm64 and asserts `ET_DYN` *and* the `/system/bin/linker64`
   interpreter. `TestLinuxArm64BuildReproducesTheOriginalBug` asserts the
   old asset really is `ET_EXEC`, so the contrast is a CI fact rather than
   a README claim.
3. `scripts/install.sh` checks `e_type` of what it downloaded **before**
   installing it, and refuses with an explanation rather than leaving a
   broken file on your phone.
