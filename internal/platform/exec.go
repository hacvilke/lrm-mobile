package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// Starting a child process on Termux is not as simple as exec.Command.
//
// Android 10 and later refuse to exec() a file inside an app's private data
// directory. Termux works around this for its own shell by going through
// the dynamic loader, and by LD_PRELOAD'ing termux-exec, which rewrites
// execve for programs that use libc. A pure-Go binary calls the execve
// syscall directly, so termux-exec never sees it — which means a Go program
// on Termux gets "permission denied" when it tries to run anything in
// $PREFIX/bin or $HOME.
//
// Command does what termux-exec would have done: when we ourselves were
// started through the loader, and the program we want to run lives in the
// app's data directory, launch it as
//
//	/system/bin/linker64 /path/to/program args...
//
// Off Android, and for programs outside the data directory (/system/bin/...),
// it is exactly exec.Command.

// linkerPaths are the loaders we may invoke a data-directory binary through,
// most specific first.
var linkerPaths = []string{"/system/bin/linker64", "/system/bin/linker"}

// NeedsLinkerExec reports whether path must be launched through Android's
// loader rather than exec'd directly.
func NeedsLinkerExec(path string) bool {
	if !LaunchedViaLinker() {
		// If we were exec'd directly, this device permits direct exec.
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// Only files in an app's private data directory are affected.
	return len(abs) >= 6 && abs[:6] == "/data/"
}

// Linker returns the loader to use, or "" if none is present.
func Linker() string {
	for _, p := range linkerPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// Command builds an *exec.Cmd that will actually start on this platform.
// Use it instead of exec.Command everywhere in this repository.
func Command(name string, args ...string) *exec.Cmd {
	resolved := name
	if filepath.Base(name) == name {
		// A bare name: resolve it on PATH so we can reason about where
		// it lives.
		if p, err := exec.LookPath(name); err == nil {
			resolved = p
		}
	}
	if NeedsLinkerExec(resolved) {
		if ld := Linker(); ld != "" {
			c := exec.Command(ld)
			c.Args = LinkerArgv(filepath.Base(resolved), resolved, args)
			return c
		}
	}
	return exec.Command(resolved, args...)
}

// LinkerArgv builds the argument vector for a loader-mediated exec.
//
// This must match what termux-exec does, and the detail is easy to get
// wrong: the loader is the *program* (argv is passed to execve alongside
// the loader's path), but argv[0] stays the name the program was invoked
// as, and the binary's path is inserted at argv[1].
//
//	execve("/system/bin/linker64", ["lrm", "/data/.../lrm", "daemon"])
//
// Getting this wrong by putting the loader in argv[0] produces
//
//	execve("/system/bin/linker64", ["/system/bin/linker64", "/data/.../lrm", "daemon"])
//
// and then FixArgs cannot recognise argv[1] as our own executable --
// the basenames no longer match -- so the path is left in place and the
// CLI reports:
//
//	unknown command "/data/data/com.termux/files/home/.local/bin/lrm"
//
// Observed on an Android 16 device when `lrm daemon --supervise`
// re-executed itself, which then span on a 1s..60s backoff.
func LinkerArgv(argv0, binary string, args []string) []string {
	out := make([]string, 0, len(args)+2)
	out = append(out, argv0, binary)
	return append(out, args...)
}

// CommandContext is Command with a context, so a child that never answers
// cannot hang the caller. termux-api helpers do exactly that when the
// Termux:API app is missing.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := Command(name, args...)
	// Rebuild through CommandContext for the kill-on-cancel behaviour,
	// then restore Args verbatim: under the loader they are not simply
	// Path plus arguments, and recomputing them would reintroduce the
	// argv[0] bug above.
	cc := exec.CommandContext(ctx, c.Path)
	cc.Args = c.Args
	cc.Env = c.Env
	cc.Dir = c.Dir
	return cc
}

// SelfCommand builds a command that re-runs this very binary. Used by the
// daemon supervisor, where os.Executable() would name the loader.
func SelfCommand(rawArgs []string, args ...string) *exec.Cmd {
	self := Executable(rawArgs)
	return Command(self, args...)
}

// HasTermuxAPI reports whether the termux-api package is installed, which
// is what provides termux-wifi-scaninfo, termux-wake-lock and friends.
func HasTermuxAPI() bool {
	_, err := exec.LookPath("termux-wake-lock")
	return err == nil
}
