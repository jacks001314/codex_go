//go:build windows

package windowsandboxservice

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// TestOnlyTheSavedJunctionNeedsMigrationLikeRust mirrors Rust #60708: a saved
// home that is a directory junction resolves to its physical target, while an
// already canonical home reports no migration.
func TestOnlyTheSavedJunctionNeedsMigrationLikeRust(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	alias := filepath.Join(dir, "alias")
	if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/j", alias, target).CombinedOutput(); err != nil {
		t.Fatalf("create test junction: %v: %s", err, output)
	}
	physical, err := canonicalRestoredHome(target)
	if err != nil {
		t.Fatalf("canonicalRestoredHome() error = %v", err)
	}
	var pins []windows.Handle
	defer func() { closeHandles(pins) }()
	home, migrated, err := resolveRestoredHome(alias, nil, &pins)
	if err != nil {
		t.Fatalf("resolveRestoredHome(alias) error = %v", err)
	}
	if !migrated || filepath.Clean(home) != physical {
		t.Fatalf("resolveRestoredHome(alias) = %q, %v; want %q, true", home, migrated, physical)
	}
	if home, migrated, err := resolveRestoredHome(physical, nil, &pins); err != nil || migrated {
		t.Fatalf("resolveRestoredHome(physical) = %q, %v, %v; want no migration", home, migrated, err)
	}
	retiring := "pending"
	runtime := &windowssandbox.RuntimeRegistration{Retiring: &retiring}
	if home, migrated, err := resolveRestoredHome(alias, runtime, &pins); err != nil || migrated {
		t.Fatalf("resolveRestoredHome(alias, retiring) = %q, %v, %v; want no migration", home, migrated, err)
	}
}
