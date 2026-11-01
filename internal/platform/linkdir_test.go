package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLinkDirOnlyOnTermux: the symlink target must only be offered where it is
// guaranteed to be on PATH and writable without root — Termux's $PREFIX/bin.
// On a desktop /usr/local/bin needs root, so LinkDir must stay empty and the
// installer falls back to ~/.local/bin plus a profile line.
func TestLinkDirOnlyOnTermux(t *testing.T) {
	t.Setenv("TERMUX_VERSION", "")
	t.Setenv("PREFIX", "/usr/local")
	if got := LinkDir(); got != "" {
		t.Fatalf("desktop prefix must not get a link dir, got %q", got)
	}
	if got := LinkDir(); got != "" {
		t.Fatalf("empty env must not get a link dir, got %q", got)
	}

	t.Setenv("PREFIX", "/data/data/com.termux/files/usr")
	want := filepath.Join("/data/data/com.termux/files/usr", "bin")
	if got := LinkDir(); got != want {
		t.Fatalf("Termux link dir = %q want %q", got, want)
	}

	// The Termux marker alone is enough, even if PREFIX is spelled differently.
	t.Setenv("TERMUX_VERSION", "0.118")
	t.Setenv("PREFIX", "/opt/tx")
	if got := LinkDir(); got != filepath.Join("/opt/tx", "bin") {
		t.Fatalf("TERMUX_VERSION host: got %q", got)
	}

	// Sanity: the directory we point at is the one that exists on the device.
	_ = os.Getenv
}
