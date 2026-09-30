package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeEnv builds an Env from a map, with no filesystem and no getprop
// unless the test asks for them.
func fakeEnv(goos, goarch string, vars map[string]string, files ...string) Env {
	set := map[string]bool{}
	for _, f := range files {
		set[f] = true
	}
	return Env{
		Getenv: func(k string) string { return vars[k] },
		GOOS:   goos,
		GOARCH: goarch,
		Stat:   func(p string) bool { return set[p] },
		Getprop: func(k string) string {
			return vars["getprop:"+k]
		},
		UnameS: func() string { return vars["uname -s"] },
		UnameM: func() string { return vars["uname -m"] },
	}
}

// --- Android detection ------------------------------------------------------

func TestDetectAndroidFromTermuxPrefix(t *testing.T) {
	// The exact shape of the bug: a GOOS=linux binary running in Termux.
	// uname says Linux, GOOS says linux, and the old installer therefore
	// downloaded lrm_linux_arm64. $PREFIX is what gives the game away.
	e := fakeEnv("linux", "arm64", map[string]string{
		"PREFIX":   "/data/data/com.termux/files/usr",
		"HOME":     "/data/data/com.termux/files/home",
		"uname -s": "Linux",
		"uname -m": "aarch64",
	})
	got := Detect(e)
	if got.OS != Android {
		t.Fatalf("OS = %q, want android (a Termux shell must never be classified as plain linux)", got.OS)
	}
	if !got.Termux {
		t.Error("Termux = false, want true")
	}
	if got.Target != "android_arm64" {
		t.Errorf("Target = %q, want android_arm64", got.Target)
	}
}

func TestDetectAndroidFromGOOS(t *testing.T) {
	e := fakeEnv("android", "arm64", map[string]string{"HOME": "/data/data/com.termux/files/home"})
	if got := Detect(e); got.OS != Android {
		t.Fatalf("OS = %q, want android", got.OS)
	}
}

func TestDetectAndroidFromTermuxVersion(t *testing.T) {
	// A Termux shell that somehow lost $PREFIX but kept $TERMUX_VERSION.
	e := fakeEnv("linux", "arm64", map[string]string{
		"TERMUX_VERSION": "0.118.0",
		"HOME":           "/data/data/com.termux/files/home",
	})
	got := Detect(e)
	if !got.Termux || got.OS != Android {
		t.Fatalf("got OS=%q termux=%v, want android/true", got.OS, got.Termux)
	}
}

func TestDetectAndroidFromGetprop(t *testing.T) {
	// A non-Termux Android terminal: no $PREFIX, but getprop answers.
	e := fakeEnv("linux", "arm64", map[string]string{
		"HOME":                             "/data/local/tmp",
		"getprop:ro.build.version.release": "14",
	})
	got := Detect(e)
	if got.OS != Android {
		t.Fatalf("OS = %q, want android", got.OS)
	}
	if got.Termux {
		t.Error("Termux = true, want false (Android is not always Termux)")
	}
	if got.Release != "14" {
		t.Errorf("Release = %q, want 14", got.Release)
	}
}

func TestDetectAndroidFromSystemFiles(t *testing.T) {
	e := fakeEnv("linux", "arm64", map[string]string{"HOME": "/data/local/tmp"},
		"/system/build.prop")
	if got := Detect(e); got.OS != Android {
		t.Fatalf("OS = %q, want android", got.OS)
	}
}

func TestDetectRecoversPrefixWhenUnset(t *testing.T) {
	e := fakeEnv("android", "arm64", map[string]string{"HOME": "/data/data/com.termux/files/home"},
		defaultTermuxPrefix)
	got := Detect(e)
	if got.Prefix != defaultTermuxPrefix {
		t.Errorf("Prefix = %q, want %q", got.Prefix, defaultTermuxPrefix)
	}
	if !got.Termux {
		t.Error("Termux = false, want true (userland present on disk)")
	}
}

// --- Termux detection standalone -------------------------------------------

func TestIsTermux(t *testing.T) {
	no := func(string) bool { return false }
	cases := []struct {
		name string
		vars map[string]string
		stat func(string) bool
		want bool
	}{
		{"termux prefix", map[string]string{"PREFIX": "/data/data/com.termux/files/usr"}, no, true},
		{"termux version", map[string]string{"TERMUX_VERSION": "0.118.0"}, no, true},
		{"desktop linux", map[string]string{"PREFIX": "/usr/local"}, no, false},
		{"empty env", map[string]string{}, no, false},
		{"userland on disk", map[string]string{}, func(p string) bool { return p == defaultTermuxPrefix }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsTermux(func(k string) string { return c.vars[k] }, c.stat)
			if got != c.want {
				t.Errorf("IsTermux = %v, want %v", got, c.want)
			}
		})
	}
}

// --- plain Linux must stay plain Linux --------------------------------------

func TestDetectLinuxIsNotAndroid(t *testing.T) {
	e := fakeEnv("linux", "amd64", map[string]string{
		"HOME":     "/home/torvalds",
		"PREFIX":   "", // a desktop shell has no PREFIX
		"uname -s": "Linux",
		"uname -m": "x86_64",
	})
	got := Detect(e)
	if got.OS != Linux {
		t.Fatalf("OS = %q, want linux", got.OS)
	}
	if got.Termux {
		t.Error("Termux = true on a desktop box")
	}
	if got.Target != "linux_amd64" {
		t.Errorf("Target = %q, want linux_amd64", got.Target)
	}
	if got.IsMobile() {
		t.Error("IsMobile = true on desktop linux")
	}
}

func TestDetectLinuxARM64(t *testing.T) {
	e := fakeEnv("linux", "arm64", map[string]string{"HOME": "/home/pi", "uname -m": "aarch64"})
	got := Detect(e)
	if got.OS != Linux || got.Arch != ARM64 {
		t.Fatalf("got %s/%s, want linux/arm64", got.OS, got.Arch)
	}
	if got.Target != "linux_arm64" {
		t.Errorf("Target = %q, want linux_arm64", got.Target)
	}
}

func TestDetectDarwinAndWindows(t *testing.T) {
	if got := Detect(fakeEnv("darwin", "arm64", map[string]string{"HOME": "/Users/x"})); got.OS != Darwin {
		t.Errorf("OS = %q, want darwin", got.OS)
	}
	if got := Detect(fakeEnv("windows", "amd64", map[string]string{"HOME": `C:\Users\x`})); got.OS != Windows {
		t.Errorf("OS = %q, want windows", got.OS)
	}
}

// --- architecture normalisation ---------------------------------------------

func TestNormalizeArch(t *testing.T) {
	cases := map[string]Arch{
		"arm64": ARM64, "aarch64": ARM64, "ARM64": ARM64, "armv8l": ARM64,
		"amd64": AMD64, "x86_64": AMD64,
		"armv7l": ARM, "armv6l": ARM, "arm": ARM,
		"i686": I386, "386": I386,
		"sparc64": ArchUnknown,
	}
	for in, want := range cases {
		if got := normalizeArch(in, ""); got != want {
			t.Errorf("normalizeArch(%q) = %q, want %q", in, got, want)
		}
		// uname fallback path must agree with the GOARCH path.
		if got := normalizeArch("", in); got != want {
			t.Errorf("normalizeArch(uname %q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeOSFallsBackToUname(t *testing.T) {
	if got := normalizeOS("", "Linux"); got != Linux {
		t.Errorf("got %q, want linux", got)
	}
	if got := normalizeOS("", "MINGW64_NT-10.0"); got != Windows {
		t.Errorf("got %q, want windows", got)
	}
	if got := normalizeOS("", "Plan9"); got != Unknown {
		t.Errorf("got %q, want unknown", got)
	}
}

// --- installation and configuration paths -----------------------------------

func TestTermuxPathsStayUnderHome(t *testing.T) {
	home := "/data/data/com.termux/files/home"
	prefix := "/data/data/com.termux/files/usr"
	t.Setenv("LRM_INSTALL_DIR", "")
	t.Setenv("LRM_HOME", "")
	t.Setenv("LRM_CACHE_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/should/be/ignored/on/mobile")
	t.Setenv("TMPDIR", "")

	in := Detect(fakeEnv("android", "arm64", map[string]string{"HOME": home, "PREFIX": prefix}))

	if got, want := in.BinDir(), filepath.Join(home, ".local", "bin"); got != want {
		t.Errorf("BinDir = %q, want %q", got, want)
	}
	if got, want := in.ConfigDir(), filepath.Join(home, ".lrm"); got != want {
		t.Errorf("ConfigDir = %q, want %q (XDG_CONFIG_HOME must be ignored on mobile)", got, want)
	}
	if got, want := in.CacheDir(), filepath.Join(prefix, "tmp", "lrm"); got != want {
		t.Errorf("CacheDir = %q, want %q", got, want)
	}
	if got, want := in.TmpDir(), filepath.Join(prefix, "tmp"); got != want {
		t.Errorf("TmpDir = %q, want %q", got, want)
	}
	// The whole point: nothing may point at a desktop system directory.
	for _, p := range []string{in.BinDir(), in.ConfigDir(), in.CacheDir(), in.TmpDir()} {
		for _, bad := range []string{"/usr/local", "/etc", "/var/lib", "/opt"} {
			if strings.HasPrefix(p, bad+"/") || p == bad {
				t.Errorf("path %q is under desktop-only %q", p, bad)
			}
		}
	}
}

func TestPathOverridesAreHonoured(t *testing.T) {
	in := Detect(fakeEnv("android", "arm64", map[string]string{"HOME": "/h", "PREFIX": "/p"}))
	t.Setenv("LRM_INSTALL_DIR", "/custom/bin")
	t.Setenv("LRM_HOME", "/custom/state")
	t.Setenv("LRM_CACHE_DIR", "/custom/cache")
	t.Setenv("TMPDIR", "/custom/tmp")
	if got := in.BinDir(); got != "/custom/bin" {
		t.Errorf("BinDir = %q", got)
	}
	if got := in.ConfigDir(); got != "/custom/state" {
		t.Errorf("ConfigDir = %q", got)
	}
	if got := in.CacheDir(); got != "/custom/cache" {
		t.Errorf("CacheDir = %q", got)
	}
	if got := in.TmpDir(); got != "/custom/tmp" {
		t.Errorf("TmpDir = %q", got)
	}
}

func TestDesktopConfigDirUsesXDG(t *testing.T) {
	t.Setenv("LRM_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "/home/t/.config")
	in := Detect(fakeEnv("linux", "amd64", map[string]string{"HOME": "/home/t"}))
	if got, want := in.ConfigDir(), filepath.Join("/home/t/.config", "lrm"); got != want {
		t.Errorf("ConfigDir = %q, want %q", got, want)
	}
}

// --- PATH detection ----------------------------------------------------------

func TestInPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	home := "/data/data/com.termux/files/home"
	bin := home + "/.local/bin"
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"present", strings.Join([]string{"/usr/bin", bin, "/bin"}, sep), true},
		{"absent", strings.Join([]string{"/usr/bin", "/bin"}, sep), false},
		{"trailing slash", bin + "/" + sep + "/bin", true},
		{"unclean", home + "/.local/./bin" + sep + "/bin", true},
		{"empty path", "", false},
		{"empty entries", sep + sep + bin, true},
		{"prefix only, not a match", home + "/.local" + sep + "/bin", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InPath(bin, c.path); got != c.want {
				t.Errorf("InPath(%q, %q) = %v, want %v", bin, c.path, got, c.want)
			}
		})
	}
	if InPath("", "/usr/bin") {
		t.Error("empty dir must never be reported as on PATH")
	}
}

func TestShellProfile(t *testing.T) {
	in := Info{Home: "/h", Termux: true}
	t.Setenv("SHELL", "/data/data/com.termux/files/usr/bin/bash")
	if got := in.ShellProfile(); got != "/h/.bashrc" {
		t.Errorf("got %q, want /h/.bashrc", got)
	}
	t.Setenv("SHELL", "/usr/bin/zsh")
	if got := in.ShellProfile(); got != "/h/.zshrc" {
		t.Errorf("got %q, want /h/.zshrc", got)
	}
	t.Setenv("SHELL", "")
	desktop := Info{Home: "/h"}
	if got := desktop.ShellProfile(); got != "/h/.profile" {
		t.Errorf("got %q, want /h/.profile", got)
	}
}

func TestDescribe(t *testing.T) {
	in := Info{OS: Android, Arch: ARM64, Termux: true, Release: "14"}
	if got, want := in.Describe(), "android/arm64 (Termux, Android 14)"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
	if got, want := (Info{OS: Linux, Arch: AMD64}).Describe(), "linux/amd64"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}

// --- the real host ------------------------------------------------------------

func TestDetectRealHostIsSelfConsistent(t *testing.T) {
	in := Detect(RealEnv())
	if in.Arch == ArchUnknown {
		t.Errorf("could not classify this machine's CPU (GOARCH=%s)", runtime.GOARCH)
	}
	if in.Home == "" {
		t.Error("Home is empty on the real host")
	}
	if in.Target == "" || strings.Contains(in.Target, "unknown") {
		t.Errorf("Target = %q on the real host", in.Target)
	}
	// A CI runner is not a phone.
	if runtime.GOOS == "linux" && os.Getenv("PREFIX") == "" && in.OS == Android {
		t.Errorf("desktop linux misdetected as android: %+v", in)
	}
}

// --- nil-safety ---------------------------------------------------------------

func TestDetectWithEmptyEnvDoesNotPanic(t *testing.T) {
	got := Detect(Env{})
	if got.OS != Unknown && got.OS != Android {
		// With no GOOS and no uname the honest answer is "unknown".
		t.Logf("OS = %q", got.OS)
	}
}
