// Package platform answers one question for the rest of LRM Mobile: what
// kind of machine is this, and where is it reasonable to put things?
//
// Desktop LRM can get away with assuming a POSIX box with /usr/local/bin and
// a real home directory. A phone cannot. Termux on Android is a Linux-shaped
// userland with none of the Linux filesystem: there is no /usr, no /etc that
// you may write to, no root, and the only durable, writable place is $HOME
// and $PREFIX. Everything here is about detecting that case *separately from
// Linux* and handing back paths that actually exist.
//
// Nothing in this package is Android-only: every function returns a sensible
// answer on desktop Linux, macOS and Windows too. Platform-aware, not
// Android-special-cased.
package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// OS is the coarse family LRM Mobile cares about. Android is its own value
// on purpose: "Android arm64" and "Linux arm64" are *not* interchangeable,
// which is precisely the bug LRM Mobile exists to fix.
type OS string

const (
	Android OS = "android"
	Linux   OS = "linux"
	Darwin  OS = "darwin"
	IOS     OS = "ios" // reserved: no binaries are produced for it yet
	Windows OS = "windows"
	Unknown OS = "unknown"
)

// Arch is the CPU family, in Go's spelling.
type Arch string

const (
	ARM64       Arch = "arm64"
	AMD64       Arch = "amd64"
	ARM         Arch = "arm"
	I386        Arch = "386"
	ArchUnknown Arch = "unknown"
)

// Info is everything the installer, the path logic and the dashboard need
// to know about the host, gathered once.
type Info struct {
	OS      OS
	Arch    Arch
	Termux  bool   // running inside the Termux app's userland
	Prefix  string // $PREFIX, Termux's /usr equivalent; empty off Termux
	Home    string // $HOME, always non-empty after Detect
	Release string // Android release, e.g. "14"; empty elsewhere
	// Target is the release-asset suffix this host should download,
	// e.g. "android_arm64" or "linux_amd64".
	Target string
}

// Env is an injectable view of the process environment and of the few shell
// probes we make. Tests supply a fake; production uses RealEnv.
type Env struct {
	Getenv func(string) string
	GOOS   string
	GOARCH string
	// Stat reports whether a path exists. Used to corroborate Termux.
	Stat func(string) bool
	// Getprop runs Android's `getprop key` and returns its trimmed output.
	// It must return "" (not an error) when the tool is absent.
	Getprop func(key string) string
	// Uname returns `uname -s` and `uname -m`. On a Go binary these are
	// redundant with GOOS/GOARCH, but the installer shells out to them and
	// we keep the same logic on both sides so they cannot drift.
	UnameS func() string
	UnameM func() string
}

// RealEnv probes the actual host.
func RealEnv() Env {
	return Env{
		Getenv: os.Getenv,
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		Stat: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		Getprop: func(key string) string {
			bin, err := exec.LookPath("getprop")
			if err != nil {
				return ""
			}
			out, err := exec.Command(bin, key).Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		},
		UnameS: func() string { return unameOr(runtime.GOOS, "-s") },
		UnameM: func() string { return unameOr(runtime.GOARCH, "-m") },
	}
}

func unameOr(fallback, flag string) string {
	bin, err := exec.LookPath("uname")
	if err != nil {
		return fallback
	}
	out, err := exec.Command(bin, flag).Output()
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(out))
}

// Detect classifies the host.
//
// The Android test is deliberately a disjunction of weak signals, because no
// single one is reliable:
//
//   - runtime.GOOS == "android": true only for binaries built with
//     GOOS=android. A GOOS=linux binary running in Termux reports "linux",
//     which is exactly how the original installer got fooled.
//   - $PREFIX containing com.termux, or $TERMUX_VERSION being set: the
//     strongest Termux signal, and present in every Termux shell.
//   - /system/build.prop or a working `getprop ro.build.version.release`:
//     present on Android generally, including non-Termux terminals, so it
//     lets us recognise Android even outside Termux.
//
// Any one of them is enough to stop treating the host as desktop Linux.
func Detect(e Env) Info {
	getenv := e.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	stat := e.Stat
	if stat == nil {
		stat = func(string) bool { return false }
	}
	getprop := e.Getprop
	if getprop == nil {
		getprop = func(string) string { return "" }
	}

	in := Info{
		Prefix: getenv("PREFIX"),
		Home:   getenv("HOME"),
	}

	in.Termux = IsTermux(getenv, stat)
	in.Release = getprop("ro.build.version.release")

	androidish := e.GOOS == "android" ||
		in.Termux ||
		in.Release != "" ||
		stat("/system/build.prop") ||
		stat("/system/bin/linker64")

	switch {
	case androidish:
		in.OS = Android
	default:
		in.OS = normalizeOS(e.GOOS, unameSOf(e))
	}

	in.Arch = normalizeArch(e.GOARCH, unameMOf(e))

	if in.Home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			in.Home = h
		}
	}
	if in.OS == Android && in.Prefix == "" && stat(defaultTermuxPrefix) {
		in.Prefix = defaultTermuxPrefix
	}
	in.Target = string(in.OS) + "_" + string(in.Arch)
	return in
}

const defaultTermuxPrefix = "/data/data/com.termux/files/usr"

func unameSOf(e Env) string {
	if e.UnameS == nil {
		return ""
	}
	return e.UnameS()
}

func unameMOf(e Env) string {
	if e.UnameM == nil {
		return ""
	}
	return e.UnameM()
}

// IsTermux reports whether we are inside Termux's userland. Exposed
// separately because the installer asks the same question in shell.
func IsTermux(getenv func(string) string, stat func(string) bool) bool {
	if getenv("TERMUX_VERSION") != "" {
		return true
	}
	if p := getenv("PREFIX"); strings.Contains(p, "com.termux") {
		return true
	}
	// A shell that lost $PREFIX (cron, a stripped env) is still Termux if
	// the userland is on disk.
	return stat(defaultTermuxPrefix)
}

// normalizeOS maps Go's GOOS, or `uname -s` when GOOS is unavailable, onto
// our OS values.
func normalizeOS(goos, unameS string) OS {
	switch goos {
	case "android":
		return Android
	case "linux":
		return Linux
	case "darwin":
		return Darwin
	case "ios":
		return IOS
	case "windows":
		return Windows
	}
	switch s := strings.ToLower(unameS); {
	case s == "linux":
		return Linux
	case s == "darwin":
		return Darwin
	case strings.HasPrefix(s, "mingw"), strings.HasPrefix(s, "cygwin"),
		strings.HasPrefix(s, "msys"), s == "windows_nt":
		return Windows
	}
	return Unknown
}

// normalizeArch maps Go's GOARCH, or `uname -m`, onto our Arch values.
// aarch64 and arm64 are the same CPU under two names; both appear in the
// wild on Android depending on which tool you ask.
func normalizeArch(goarch, unameM string) Arch {
	for _, s := range []string{strings.ToLower(goarch), strings.ToLower(unameM)} {
		switch s {
		case "arm64", "aarch64", "aarch64_be", "armv8l", "armv8b":
			return ARM64
		case "amd64", "x86_64":
			return AMD64
		case "arm", "armv7l", "armv6l", "armv7":
			return ARM
		case "386", "i386", "i686":
			return I386
		}
	}
	return ArchUnknown
}

// IsMobile reports whether this host is a phone-shaped environment: no root,
// no system-wide install directory, everything under $HOME.
func (i Info) IsMobile() bool { return i.OS == Android || i.OS == IOS }

// Describe is the one-line summary the CLI and installer print.
func (i Info) Describe() string {
	var b strings.Builder
	b.WriteString(string(i.OS))
	b.WriteString("/")
	b.WriteString(string(i.Arch))
	if i.Termux {
		b.WriteString(" (Termux")
		if i.Release != "" {
			b.WriteString(", Android " + i.Release)
		}
		b.WriteString(")")
	} else if i.OS == Android && i.Release != "" {
		b.WriteString(" (Android " + i.Release + ")")
	}
	return b.String()
}

// BinDir is where a user-owned executable belongs on this host.
//
// It is never /usr/local/bin. On Termux that directory is not writable and
// not on PATH; on desktop Linux it needs root, which LRM Mobile refuses to
// require. $HOME/.local/bin is correct everywhere and is already on PATH in
// most shells.
func (i Info) BinDir() string {
	if d := os.Getenv("LRM_INSTALL_DIR"); d != "" {
		return d
	}
	return filepath.Join(i.Home, ".local", "bin")
}

// ConfigDir is the machine-wide LRM state directory (device identity, known
// peers). Upstream LRM already honours $LRM_HOME and otherwise uses
// ~/.lrm, which is correct on Termux too, so this agrees with it rather
// than inventing a second location.
func (i Info) ConfigDir() string {
	if d := os.Getenv("LRM_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" && !i.IsMobile() {
		return filepath.Join(d, "lrm")
	}
	return filepath.Join(i.Home, ".lrm")
}

// CacheDir is for things that may be deleted without losing data.
func (i Info) CacheDir() string {
	if d := os.Getenv("LRM_CACHE_DIR"); d != "" {
		return d
	}
	if i.Termux && i.Prefix != "" {
		return filepath.Join(i.Prefix, "tmp", "lrm")
	}
	return filepath.Join(i.Home, ".cache", "lrm")
}

// TmpDir is a writable scratch directory. Android has no /tmp; Termux puts
// one under $PREFIX, and TMPDIR is set accordingly inside Termux.
func (i Info) TmpDir() string {
	if d := os.Getenv("TMPDIR"); d != "" {
		return d
	}
	if i.Termux && i.Prefix != "" {
		return filepath.Join(i.Prefix, "tmp")
	}
	return os.TempDir()
}

// InPath reports whether dir appears in the PATH-style list pathEnv.
// Entries are compared after cleaning so "~/.local/bin/" matches
// "$HOME/.local/bin".
func InPath(dir, pathEnv string) bool {
	if dir == "" {
		return false
	}
	want := filepath.Clean(dir)
	for _, p := range filepath.SplitList(pathEnv) {
		if p == "" {
			continue
		}
		if filepath.Clean(p) == want {
			return true
		}
	}
	return false
}

// ShellProfile guesses the file a PATH export should be appended to.
// Termux ships bash and reads ~/.bashrc for interactive shells; it does not
// read ~/.profile unless the user arranges it, which is a common reason
// "add it to ~/.profile" advice silently fails on a phone.
func (i Info) ShellProfile() string {
	shell := filepath.Base(os.Getenv("SHELL"))
	switch shell {
	case "zsh":
		return filepath.Join(i.Home, ".zshrc")
	case "fish":
		return filepath.Join(i.Home, ".config", "fish", "config.fish")
	}
	if i.Termux {
		return filepath.Join(i.Home, ".bashrc")
	}
	if shell == "bash" {
		return filepath.Join(i.Home, ".bashrc")
	}
	return filepath.Join(i.Home, ".profile")
}
