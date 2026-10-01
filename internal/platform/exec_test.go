package platform

import (
	"path/filepath"
	"reflect"
	"testing"
)

const termuxBin = "/data/data/com.termux/files/home/.local/bin/lrm"

// TestLinkerArgvKeepsArgv0 is the regression test for the supervisor bug
// seen on an Android 16 device:
//
//	daemon exit status 2 after 0s — restarting in 32s (restart #6)
//	unknown command "/data/data/com.termux/files/home/.local/bin/lrm"
//
// `lrm daemon --supervise` re-executed itself through the loader but put
// the loader in argv[0]. FixArgs compares the basenames of argv[0] and
// argv[1] to decide whether argv[1] is the injected executable path; with
// "linker64" in argv[0] they no longer matched, the path survived into
// cli.Run, and the daemon failed instantly in a restart loop.
func TestLinkerArgvKeepsArgv0(t *testing.T) {
	got := LinkerArgv("lrm", termuxBin, []string{"daemon", "--port", "8787"})
	want := []string{"lrm", termuxBin, "daemon", "--port", "8787"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q\nwant  %q", got, want)
	}
	if filepath.Base(got[0]) == "linker64" {
		t.Error("the loader must not appear in argv[0]; that is the bug")
	}
}

// The whole point: what LinkerArgv produces must be something FixArgs can
// undo. These two functions are each other's inverse, and the Android
// failure was them disagreeing.
func TestLinkerArgvRoundTripsThroughFixArgs(t *testing.T) {
	cases := [][]string{
		{"daemon"},
		{"daemon", "--port", "8787"},
		{"platform"},
		{},
		{"commit", "-m", "a message with spaces"},
	}
	for _, userArgs := range cases {
		argv := LinkerArgv("lrm", termuxBin, userArgs)

		// The child sees argv as os.Args. Undo the injection.
		fixed := fixArgs(argv, readlinkTo("/system/bin/linker64"))
		got := fixed[1:]

		if len(got) != len(userArgs) {
			t.Errorf("round trip of %q gave %q", userArgs, got)
			continue
		}
		for i := range got {
			if got[i] != userArgs[i] {
				t.Errorf("round trip of %q gave %q", userArgs, got)
				break
			}
		}
	}
}

// A supervisor restart must survive the round trip with its subcommand
// intact — this is exactly what broke.
func TestSupervisorRestartArgvSurvives(t *testing.T) {
	argv := LinkerArgv("lrm", termuxBin, []string{"daemon", "--port", "9000"})
	user := fixArgs(argv, readlinkTo("/system/bin/linker64"))[1:]
	if len(user) == 0 || user[0] != "daemon" {
		t.Fatalf("first user argument = %q, want \"daemon\"; the CLI would "+
			"reject anything else as an unknown command", user)
	}
}

func TestNeedsLinkerExecOnlyForDataDirBinaries(t *testing.T) {
	// Without a loader launch nothing needs special treatment.
	if NeedsLinkerExec("/usr/bin/ls") {
		t.Error("a system binary on a normal host must not need the loader")
	}
}

func TestLinkerArgvDoesNotAliasCallerSlice(t *testing.T) {
	args := []string{"daemon"}
	out := LinkerArgv("lrm", termuxBin, args)
	out[2] = "mutated"
	if args[0] != "daemon" {
		t.Error("LinkerArgv mutated the caller's slice")
	}
}
