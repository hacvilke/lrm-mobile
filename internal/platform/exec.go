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
			return exec.Command(ld, append([]string{resolved}, args...)...)
		}
	}
	return exec.Command(resolved, args...)
}

// CommandContext is Command with a context, so a child that never answers
// cannot hang the caller. termux-api helpers do exactly that when the
// Termux:API app is missing.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := Command(name, args...)
	cc := exec.CommandContext(ctx, c.Path, c.Args[1:]...)
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
