// Command lrm is LRM Mobile's build of the LRM CLI.
//
// It is deliberately thin. Every real command — init, daemon, status,
// dashboard, commit, log, sync, the git-compat surface, the .lr language —
// is the upstream LRM implementation, imported unchanged from
// third_party/lrm. This wrapper does three things upstream cannot do for
// itself on a phone:
//
//  1. resolves LRM's home and cache directories through internal/platform,
//     so nothing ever reaches for /usr/local, /etc or /var on Termux;
//  2. adds `lrm platform`, a one-command diagnostic that tells you what the
//     binary thinks it is running on (this is what you paste into a bug
//     report);
//  3. turns the two mobile-specific failure modes — no $HOME, dashboard with
//     no browser to launch — into readable messages instead of a crash.
//
// The binary that ships for Android is built with GOOS=android and
// -buildmode=pie. See docs/E_TYPE.md for why a GOOS=linux binary cannot
// work in Termux.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/lrm-project/lrm/internal/cli"
	"github.com/lrm-project/lrm/mobile/internal/platform"
)

// version is stamped by the release build with -ldflags -X.
var version = "dev"

func main() {
	info := platform.Detect(platform.RealEnv())

	if err := prepare(info); err != nil {
		fmt.Fprintln(os.Stderr, "lrm:", err)
		os.Exit(1)
	}

	// Termux launches binaries through /system/bin/linker64 (Android 10+
	// forbids exec() inside an app's private data directory), and the
	// loader leaves its own argument wedged into argv. Undo that before
	// anything tries to parse a subcommand out of it.
	rawArgs := os.Args
	args := platform.FixArgs(rawArgs)[1:]
	if len(args) > 0 {
		switch args[0] {
		case "platform", "doctor":
			printPlatform(info, rawArgs)
			return
		}
	}

	hintDashboard(info, args)

	os.Exit(cli.Run(args))
}

// prepare makes the environment safe for the upstream core.
//
// Upstream LRM resolves its machine-wide state with os.UserHomeDir(), which
// on Android returns an error when $HOME is unset — and $HOME can be unset
// in a Termux:Widget script or an adb shell. Rather than let that surface
// as a confusing error deep inside `lrm init`, we settle it here, once, by
// exporting LRM_HOME (the override upstream already supports). This is
// platform-aware, not Android-only: the same code path fixes a bare
// systemd unit on desktop Linux.
func prepare(info platform.Info) error {
	if os.Getenv("LRM_HOME") == "" {
		if info.Home == "" {
			return fmt.Errorf("cannot determine a home directory: set $HOME, or set $LRM_HOME to a writable directory")
		}
		if err := os.Setenv("LRM_HOME", info.ConfigDir()); err != nil {
			return err
		}
	}
	if os.Getenv("TMPDIR") == "" {
		// Android has no /tmp. os.TempDir() would hand back an
		// unwritable path and every atomic rename would fail.
		if d := info.TmpDir(); d != "" {
			_ = os.Setenv("TMPDIR", d)
		}
	}
	return nil
}

// hintDashboard prints the URL-only guidance before handing off. Termux has
// no desktop browser and no xdg-open by default; upstream already binds
// 127.0.0.1 and prints the URL rather than launching anything, so the only
// thing missing on mobile is telling the user how to open it.
func hintDashboard(info platform.Info, args []string) {
	if !info.IsMobile() || len(args) == 0 {
		return
	}
	if args[0] != "dashboard" && args[0] != "dash" {
		return
	}
	fmt.Println("mobile: the dashboard is bound to localhost only and is not reachable from the network.")
	fmt.Println("mobile: no browser is launched — copy the http://127.0.0.1:PORT URL below into your phone's browser.")
}

func printPlatform(info platform.Info, rawArgs []string) {
	fmt.Printf("lrm (LRM Mobile) %s\n", version)
	fmt.Printf("  built for     %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  executable    %s\n", platform.Executable(rawArgs))
	if platform.LaunchedViaLinker() {
		fmt.Printf("  launched via  /system/bin/linker64 (argv corrected)\n")
	}
	fmt.Printf("  detected      %s\n", info.Describe())
	fmt.Printf("  termux        %t\n", info.Termux)
	if info.Prefix != "" {
		fmt.Printf("  PREFIX        %s\n", info.Prefix)
	}
	if info.Release != "" {
		fmt.Printf("  android       %s\n", info.Release)
	}
	fmt.Printf("  HOME          %s\n", info.Home)
	fmt.Printf("  release asset lrm_%s\n", info.Target)
	fmt.Printf("  bin dir       %s\n", info.BinDir())
	fmt.Printf("  config dir    %s\n", info.ConfigDir())
	fmt.Printf("  cache dir     %s\n", info.CacheDir())
	fmt.Printf("  tmp dir       %s\n", info.TmpDir())

	bin := info.BinDir()
	if platform.InPath(bin, os.Getenv("PATH")) {
		fmt.Printf("  PATH          ok (%s is on PATH)\n", bin)
	} else {
		fmt.Printf("  PATH          MISSING — %s is not on PATH\n", bin)
		fmt.Printf("                echo 'export PATH=\"%s:$PATH\"' >> %s\n", bin, info.ShellProfile())
	}
}
