//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePrimaryHelper(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestPrimarySelectionUsesAvailableHelpersLikeRust covers real helper
// availability, fallback, stdin delivery, and ownership release (#49112/#53753).
func TestPrimarySelectionUsesAvailableHelpersLikeRust(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	writePrimaryHelper(t, dir, "wl-paste", `printf 'wayland-primary'`)
	writePrimaryHelper(t, dir, "xclip", `if [ "$1" = "-selection" ] && [ "$3" = "-o" ]; then printf 'xclip-primary'; exit 0; fi
if [ "$3" = "-in" ]; then cat > "`+logPath+`"; printf 'xclip-in\n' >> "`+logPath+`"; exit 0; fi
exit 1`)
	writePrimaryHelper(t, dir, "wl-copy", `if [ "$1" = "--clear" ]; then printf 'clear\n' >> "`+logPath+`"; exit 0; fi
cat > "`+logPath+`"; printf 'wl-copy\n' >> "`+logPath+`"`)
	t.Setenv("PATH", dir)

	read, err := ReadPrimarySelection()
	if err != nil || read != "wayland-primary" {
		t.Fatalf("ReadPrimarySelection = %q, %v", read, err)
	}
	if err := WritePrimarySelection("copied text"); err != nil {
		t.Fatalf("WritePrimarySelection() error = %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "copied text") || !strings.Contains(string(data), "wl-copy") {
		t.Fatalf("helper log = %q", data)
	}
	if err := ReleasePrimarySelection(); err != nil {
		t.Fatalf("ReleasePrimarySelection() error = %v", err)
	}
	data, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "clear") {
		t.Fatalf("ownership release was not recorded: %q", data)
	}
}

// TestPrimarySelectionFallsBackWhenWaylandHelperFailsLikeRust covers helper
// failure fallback rather than treating a broken Wayland session as success.
func TestPrimarySelectionFallsBackWhenWaylandHelperFailsLikeRust(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	writePrimaryHelper(t, dir, "wl-paste", `exit 1`)
	writePrimaryHelper(t, dir, "xclip", `if [ "$3" = "-o" ]; then printf 'xclip-primary'; exit 0; fi
if [ "$3" = "-in" ]; then cat > "`+logPath+`"; exit 0; fi
exit 1`)
	writePrimaryHelper(t, dir, "wl-copy", `exit 1`)
	writePrimaryHelper(t, dir, "xsel", `exit 1`)
	t.Setenv("PATH", dir)

	read, err := ReadPrimarySelection()
	if err != nil || read != "xclip-primary" {
		t.Fatalf("ReadPrimarySelection fallback = %q, %v", read, err)
	}
	if err := WritePrimarySelection("fallback text"); err != nil {
		t.Fatalf("WritePrimarySelection fallback error = %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fallback text" {
		t.Fatalf("xclip stdin = %q", data)
	}
}

// TestPrimarySelectionReportsUnavailableHelpersLikeRust covers the empty PATH
// case rather than silently claiming ownership.
func TestPrimarySelectionReportsUnavailableHelpersLikeRust(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := ReadPrimarySelection(); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("ReadPrimarySelection error = %v", err)
	}
	if err := WritePrimarySelection("text"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("WritePrimarySelection error = %v", err)
	}
	if err := ReleasePrimarySelection(); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("ReleasePrimarySelection error = %v", err)
	}
}
