package platform

import (
	"errors"
	"reflect"
	"testing"
)

// readlinkTo fakes /proc/self/exe pointing at target.
func readlinkTo(target string) func(string) (string, error) {
	return func(string) (string, error) { return target, nil }
}

func readlinkFails() func(string) (string, error) {
	return func(string) (string, error) { return "", errors.New("no such file") }
}

const termuxLrm = "/data/data/com.termux/files/home/.local/bin/lrm"

func TestLaunchedViaLinker(t *testing.T) {
	cases := map[string]bool{
		"/system/bin/linker64":        true,
		"/system/bin/linker":          true,
		"/system/bin/linker_hwasan64": true,
		termuxLrm:                     false,
		"/usr/local/bin/lrm":          false,
	}
	for target, want := range cases {
		if got := launchedViaLinker(readlinkTo(target)); got != want {
			t.Errorf("/proc/self/exe -> %q: got %v, want %v", target, got, want)
		}
	}
	if launchedViaLinker(readlinkFails()) {
		t.Error("unreadable /proc/self/exe must not be treated as a linker launch")
	}
}

// TestFixArgsRemovesTheLinkerArgument is the regression test for the exact
// failure seen on an Android 16 device:
//
//	$ lrm platform
//	unknown command "/data/data/com.termux/files/home/.local/bin/lrm"
func TestFixArgsRemovesTheLinkerArgument(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			"lrm platform",
			[]string{"lrm", termuxLrm, "platform"},
			[]string{"lrm", "platform"},
		},
		{
			"lrm --help",
			[]string{"lrm", termuxLrm, "--help"},
			[]string{"lrm", "--help"},
		},
		{
			"no user args at all",
			[]string{"lrm", termuxLrm},
			[]string{"lrm"},
		},
		{
			"several args preserved in order",
			[]string{"lrm", termuxLrm, "commit", "-m", "hello world"},
			[]string{"lrm", "commit", "-m", "hello world"},
		},
		{
			"invoked by absolute path",
			[]string{termuxLrm, termuxLrm, "status"},
			[]string{termuxLrm, "status"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fixArgs(c.in, readlinkTo("/system/bin/linker64"))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("fixArgs(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// The correction must never fire when the loader was not involved, or argv
// would be corrupted on every other platform.
func TestFixArgsLeavesNormalLaunchesAlone(t *testing.T) {
	cases := []struct {
		name    string
		procExe string
		in      []string
	}{
		{"desktop linux", "/home/t/.local/bin/lrm", []string{"lrm", "platform"}},
		{"direct exec on android", termuxLrm, []string{"lrm", "platform"}},
		{"no /proc at all", "", []string{"lrm", "status", "--json"}},
		{"argv0 only", "/system/bin/linker64", []string{"lrm"}},
		{"empty argv", "/system/bin/linker64", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rl := readlinkTo(c.procExe)
			if c.procExe == "" {
				rl = readlinkFails()
			}
			got := fixArgs(c.in, rl)
			if !reflect.DeepEqual(got, c.in) {
				t.Errorf("argv was modified: got %q, want %q unchanged", got, c.in)
			}
		})
	}
}

// Under the loader, a *user* argument must still survive: we only drop
// something that genuinely looks like our own executable path.
func TestFixArgsDoesNotEatUserArguments(t *testing.T) {
	cases := [][]string{
		// A relative first argument is never what the loader injects.
		{"lrm", "notes.md", "commit"},
		// An absolute path with a different basename is a user's file.
		{"lrm", "/sdcard/Documents/report.txt"},
		// A subcommand is not a path.
		{"lrm", "dashboard", "--port", "9000"},
	}
	for _, in := range cases {
		got := fixArgs(in, readlinkTo("/system/bin/linker64"))
		if !reflect.DeepEqual(got, in) {
			t.Errorf("user argument was eaten: fixArgs(%q) = %q", in, got)
		}
	}
}

// The end-to-end shape callers actually use.
func TestFixArgsProducesUsableUserArgs(t *testing.T) {
	raw := []string{"lrm", termuxLrm, "init", "--user", "brandon"}
	user := fixArgs(raw, readlinkTo("/system/bin/linker64"))[1:]
	want := []string{"init", "--user", "brandon"}
	if !reflect.DeepEqual(user, want) {
		t.Fatalf("user args = %q, want %q", user, want)
	}
	if user[0] != "init" {
		t.Errorf("first user arg = %q; the CLI would reject this as an unknown command", user[0])
	}
}
