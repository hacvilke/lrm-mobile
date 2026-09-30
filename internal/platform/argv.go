package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// Android 10 and later forbid exec() of a file inside an app's private data
// directory. Termux's home is exactly that:
//
//	/data/data/com.termux/files/home/.local/bin/lrm
//
// so Termux cannot exec an installed binary directly. Instead it launches it
// through Android's dynamic loader:
//
//	/system/bin/linker64 /data/data/com.termux/files/home/.local/bin/lrm <args...>
//
// The loader does not consume that path. It is left in place, so the program
// starts with an extra element wedged in at index 1:
//
//	os.Args = ["lrm", "/data/.../lrm", "platform"]
//	                   ^^^^^^^^^^^^^^ injected
//
// Every Go program that parses os.Args[1:] then reads the loader's argument
// as the user's first argument. For LRM that means:
//
//	unknown command "/data/data/com.termux/files/home/.local/bin/lrm"
//
// This file undoes it. Two things make the correction safe rather than a
// guess:
//
//  1. LaunchedViaLinker reads /proc/self/exe. When the loader started us,
//     that symlink names the *loader*, not our binary — an unambiguous fact
//     about how this process was created, not a heuristic.
//  2. Even then, we only drop args[1] if it actually looks like our own
//     executable path. A user argument is never removed by accident.
//
// Off Android, and on an Android that exec'd us directly, both checks fail
// and os.Args is passed through exactly as the kernel delivered it.

// linkerNames are the loaders bionic may start a program through.
var linkerNames = map[string]bool{
	"linker":          true,
	"linker64":        true,
	"linker_asan":     true,
	"linker_asan64":   true,
	"linker_hwasan64": true,
}

// ProcSelfExe is the symlink read to discover how this process was started.
// It is a variable so tests can point it somewhere else.
var ProcSelfExe = "/proc/self/exe"

// LaunchedViaLinker reports whether this process was started by Android's
// dynamic loader rather than exec'd directly — the situation in which the
// loader has inserted an extra argv element.
func LaunchedViaLinker() bool {
	return launchedViaLinker(os.Readlink)
}

func launchedViaLinker(readlink func(string) (string, error)) bool {
	p, err := readlink(ProcSelfExe)
	if err != nil {
		return false
	}
	return linkerNames[filepath.Base(p)]
}

// FixArgs returns argv with the loader's injected path removed, if it is
// there. Pass it the raw os.Args; it returns a slice of the same shape, so
// callers keep using args[0] as the program name and args[1:] as the user's
// arguments.
func FixArgs(args []string) []string {
	return fixArgs(args, os.Readlink)
}

func fixArgs(args []string, readlink func(string) (string, error)) []string {
	if len(args) < 2 {
		return args
	}
	if !launchedViaLinker(readlink) {
		// Not started by the loader: nothing was injected, so touching
		// argv here could only ever corrupt it.
		return args
	}
	if !looksLikeOurExe(args[1], args[0]) {
		return args
	}
	out := make([]string, 0, len(args)-1)
	out = append(out, args[0])
	out = append(out, args[2:]...)
	return out
}

// looksLikeOurExe reports whether candidate is plausibly the absolute path
// the loader was handed for this program, given the name the user invoked.
//
// The loader is always given an absolute path, and that path always ends in
// the same filename the user typed (or the absolute path they typed). Both
// must hold before we drop anything.
func looksLikeOurExe(candidate, argv0 string) bool {
	if !filepath.IsAbs(candidate) {
		return false
	}
	if candidate == argv0 {
		return true
	}
	if filepath.Base(candidate) != filepath.Base(argv0) {
		return false
	}
	// A path inside an app's private data directory is the case Android
	// refuses to exec, and therefore the case the loader is used for.
	// Accept a plain user-owned bin directory too, since Termux can be
	// configured with a different home.
	return strings.HasPrefix(candidate, "/data/") ||
		strings.Contains(candidate, "/files/home/") ||
		filepath.IsAbs(candidate)
}

// Executable returns the path of this program.
//
// os.Executable reads /proc/self/exe, which under the loader names
// /system/bin/linker64 rather than us — so a program that re-executes
// itself, or prints its own location, would be wrong on Termux. When we were
// started by the loader, the path it was handed is the honest answer.
//
// rawArgs must be the *unmodified* os.Args.
func Executable(rawArgs []string) string {
	if LaunchedViaLinker() && len(rawArgs) >= 2 && filepath.IsAbs(rawArgs[1]) {
		return rawArgs[1]
	}
	if p, err := os.Executable(); err == nil {
		return p
	}
	if len(rawArgs) > 0 {
		return rawArgs[0]
	}
	return ""
}
