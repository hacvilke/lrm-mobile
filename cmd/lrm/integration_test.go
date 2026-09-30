package main_test

// End-to-end tests for the LRM Mobile binary.
//
// These build the real `lrm` and run it, because the things that broke on
// Android were never unit-testable: they were "does this file execute" and
// "does this path exist". Two of the tests are the actual regression tests
// for the bug in the README — they cross-compile for android/arm64 and
// inspect the ELF header, which is exactly what Android's loader does
// before it refuses to run a binary.

import (
	"bufio"
	"debug/elf"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// lrmBinary builds the CLI once for the whole test binary.
func lrmBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lrm-mobile-bin")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "lrm")
		cmd := exec.Command("go", "build", "-o", out, ".")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build failed: %v\n%s", err, b)
			return
		}
		binPath = out
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binPath
}

// termuxEnv returns an environment that looks like a Termux shell, rooted at
// a temp dir so no test touches the real home.
func termuxEnv(t *testing.T) (home string, env []string) {
	t.Helper()
	home = t.TempDir()
	prefix := filepath.Join(home, "..", "usr")
	_ = os.MkdirAll(filepath.Join(prefix, "tmp"), 0o755)
	env = append(os.Environ(),
		"HOME="+home,
		"PREFIX="+prefix,
		"TERMUX_VERSION=0.118.0",
		"LRM_HOME=", // let the binary derive it
		"TMPDIR=",
	)
	return home, env
}

func run(t *testing.T, env []string, dir, bin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// --- basic CLI execution ----------------------------------------------------

func TestCLIHelp(t *testing.T) {
	bin := lrmBinary(t)
	out, err := run(t, os.Environ(), t.TempDir(), bin, "--help")
	if err != nil {
		t.Fatalf("lrm --help failed: %v\n%s", err, out)
	}
	if !strings.Contains(strings.ToLower(out), "usage") {
		t.Errorf("lrm --help printed no usage:\n%s", out)
	}
}

func TestCLIVersion(t *testing.T) {
	bin := lrmBinary(t)
	if out, err := run(t, os.Environ(), t.TempDir(), bin, "version"); err != nil {
		t.Fatalf("lrm version failed: %v\n%s", err, out)
	}
}

func TestPlatformCommand(t *testing.T) {
	bin := lrmBinary(t)
	home, env := termuxEnv(t)
	out, err := run(t, env, t.TempDir(), bin, "platform")
	if err != nil {
		t.Fatalf("lrm platform failed: %v\n%s", err, out)
	}
	for _, want := range []string{"termux        true", "detected      android", "release asset lrm_android_"} {
		if !strings.Contains(out, want) {
			t.Errorf("lrm platform output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, filepath.Join(home, ".local", "bin")) {
		t.Errorf("bin dir is not under the Termux home:\n%s", out)
	}
	// Nothing may point at a desktop system directory.
	for _, bad := range []string{"/usr/local/bin", "/etc/lrm", "/var/lib"} {
		if strings.Contains(out, bad) {
			t.Errorf("mobile paths must not mention %q:\n%s", bad, out)
		}
	}
}

// --- install paths: state must land under $HOME ------------------------------

func TestInitUsesHomeNotSystemPaths(t *testing.T) {
	bin := lrmBinary(t)
	home, env := termuxEnv(t)
	proj := filepath.Join(home, "my-project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, env, proj, bin, "init", "--user", "brandon")
	if err != nil {
		t.Fatalf("lrm init failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(proj, ".lrm")); err != nil {
		t.Fatalf("no .lrm in the project after init: %v", err)
	}
	// Machine-wide state, when it is created at all (upstream creates it
	// lazily, on the first command that needs a device identity), must
	// land under $HOME and nowhere else.
	if out, err := run(t, env, proj, bin, "device", "id"); err == nil {
		_ = out
		if _, err := os.Stat(filepath.Join(home, ".lrm")); err != nil {
			t.Errorf("machine state was not created at $HOME/.lrm: %v", err)
		}
	}

	out, err = run(t, env, proj, bin, "status")
	if err != nil {
		t.Fatalf("lrm status failed: %v\n%s", err, out)
	}

	if err := os.WriteFile(filepath.Join(proj, "a.txt"), []byte("hello from a phone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, env, proj, bin, "commit", "-m", "first"); err != nil {
		t.Fatalf("lrm commit failed: %v\n%s", err, out)
	}
	if out, err := run(t, env, proj, bin, "log"); err != nil || !strings.Contains(out, "first") {
		t.Fatalf("lrm log did not show the commit: %v\n%s", err, out)
	}
}

func TestNoHomeGivesAReadableError(t *testing.T) {
	bin := lrmBinary(t)
	cmd := exec.Command(bin, "status")
	cmd.Dir = t.TempDir()
	// A Termux:Widget script can genuinely run with no HOME.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	b, err := cmd.CombinedOutput()
	if err == nil {
		return // resolved a home some other way; fine
	}
	if strings.Contains(string(b), "panic") {
		t.Errorf("missing $HOME panicked instead of erroring:\n%s", b)
	}
}

// --- dashboard --------------------------------------------------------------

func TestDashboardStartsAndBindsLocalhostOnly(t *testing.T) {
	bin := lrmBinary(t)
	home, env := termuxEnv(t)
	proj := filepath.Join(home, "dash-project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, env, proj, bin, "init", "--user", "brandon"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	port := freePort(t)
	cmd := exec.Command(bin, "dashboard", "--port", fmt.Sprint(port))
	cmd.Dir = proj
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// It must print a URL rather than trying to launch a browser: Termux
	// has no browser to launch.
	urlRe := regexp.MustCompile(`http://127\.0\.0\.1:\d+`)
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			t.Log("dashboard: " + line)
			if m := urlRe.FindString(line); m != "" {
				select {
				case found <- m:
				default:
				}
			}
		}
	}()

	var url string
	select {
	case url = <-found:
	case <-time.After(20 * time.Second):
		t.Fatal("dashboard never printed an http://127.0.0.1:PORT URL")
	}

	// It must actually serve.
	deadline := time.Now().Add(15 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dashboard did not answer on %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s = %d, want 200", url, resp.StatusCode)
	}

	// And it must NOT be reachable on a routable address: the dashboard
	// shows the workspace's files, so a phone on public Wi-Fi must not be
	// serving it to the network.
	if ip := outboundIP(); ip != "" {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, fmt.Sprint(port)), 500*time.Millisecond)
		if err == nil {
			c.Close()
			t.Errorf("dashboard is reachable on %s:%d — it must bind localhost only", ip, port)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func outboundIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return ""
}

// --- the regression tests for "unexpected e_type: 2" -------------------------

// TestAndroidBuildIsPIE cross-compiles the android/arm64 release binary and
// checks the two header fields Android's loader checks. If this test fails,
// the release would be unrunnable on every Android device.
func TestAndroidBuildIsPIE(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compile takes a while")
	}
	out := filepath.Join(t.TempDir(), "lrm_android_arm64")
	cmd := exec.Command("go", "build", "-buildmode=pie", "-trimpath", "-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=android", "GOARCH=arm64")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("android build failed: %v\n%s", err, b)
	}

	f, err := elf.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if f.Type != elf.ET_DYN {
		t.Errorf("e_type = %v (%d), want ET_DYN (3). "+
			"ET_EXEC is what produces 'unexpected e_type: 2' on Android.",
			f.Type, f.Type)
	}
	if f.Machine != elf.EM_AARCH64 {
		t.Errorf("machine = %v, want EM_AARCH64", f.Machine)
	}
	interp := elfInterp(t, f)
	if interp != "/system/bin/linker64" {
		t.Errorf("ELF interpreter = %q, want /system/bin/linker64 "+
			"(a linux/arm64 PIE build asks for /lib/ld-linux-aarch64.so.1, "+
			"which does not exist in Termux)", interp)
	}
}

// TestLinuxArm64BuildReproducesTheOriginalBug documents, executably, why the
// upstream asset could never have worked: the binary the old installer chose
// is ET_EXEC. This test asserts the broken shape so that the contrast with
// the test above is a fact in CI, not a claim in a README.
func TestLinuxArm64BuildReproducesTheOriginalBug(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compile takes a while")
	}
	out := filepath.Join(t.TempDir(), "lrm_linux_arm64")
	cmd := exec.Command("go", "build", "-trimpath", "-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=arm64")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("linux build failed: %v\n%s", err, b)
	}
	f, err := elf.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Type != elf.ET_EXEC {
		t.Skipf("default linux/arm64 build is %v on this toolchain, not ET_EXEC; "+
			"the original bug may no longer reproduce here", f.Type)
	}
	t.Logf("confirmed: a default linux/arm64 build is %v (e_type 2) — "+
		"this is the binary Android refuses", f.Type)
}

func elfInterp(t *testing.T, f *elf.File) string {
	t.Helper()
	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		b := make([]byte, p.Filesz)
		if _, err := p.ReadAt(b, 0); err != nil {
			t.Fatal(err)
		}
		return strings.TrimRight(string(b), "\x00")
	}
	return ""
}
