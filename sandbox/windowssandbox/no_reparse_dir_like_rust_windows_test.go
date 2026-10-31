//go:build windows

package windowssandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func openForACL(path string, disposition DirectoryOpenDisposition) (windows.Handle, error) {
	return OpenDirectoryNoReparse(
		path,
		windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		disposition,
	)
}

func createDirectoryJunction(t *testing.T, target string, alias string) {
	t.Helper()
	cmd := exec.Command("cmd", "/C", "mklink", "/J", alias, target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("mklink /J unavailable in this environment: %v (%s)", err, strings.TrimSpace(string(output)))
	}
}

func TestOpenDirectoryNoReparseCreatesAndOpensPlainDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), ".sandbox-bin")
	handle, err := openForACL(directory, DirectoryOpenOrCreate)
	if err != nil {
		t.Fatalf("open-or-create plain directory error = %v", err)
	}
	windows.CloseHandle(handle)
	handle, err = openForACL(directory, DirectoryOpenExisting)
	if err != nil {
		t.Fatalf("open existing plain directory error = %v", err)
	}
	windows.CloseHandle(handle)
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		t.Fatalf("directory %s stat = %v, %v", directory, info, err)
	}
}

func TestOpenDirectoryNoReparseRejectsFinalDirectoryJunction(t *testing.T) {
	temp := t.TempDir()
	target := filepath.Join(temp, "target")
	alias := filepath.Join(temp, ".sandbox-bin")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("Mkdir(target) error = %v", err)
	}
	createDirectoryJunction(t, target, alias)

	handle, err := openForACL(alias, DirectoryOpenOrCreate)
	if err == nil {
		windows.CloseHandle(handle)
		t.Fatal("final directory junction must be rejected")
	}
	if !strings.Contains(err.Error(), "reparse point") {
		t.Fatalf("final directory junction error = %v, want reparse point", err)
	}
}

func TestOpenDirectoryNoReparseRejectsAncestorDirectoryJunction(t *testing.T) {
	temp := t.TempDir()
	targetHome := filepath.Join(temp, "target-home")
	aliasHome := filepath.Join(temp, "linked-home")
	if err := os.Mkdir(targetHome, 0o700); err != nil {
		t.Fatalf("Mkdir(target-home) error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(targetHome, ".sandbox-bin"), 0o700); err != nil {
		t.Fatalf("Mkdir(.sandbox-bin) error = %v", err)
	}
	createDirectoryJunction(t, targetHome, aliasHome)

	handle, err := openForACL(filepath.Join(aliasHome, ".sandbox-bin"), DirectoryOpenOrCreate)
	if err == nil {
		windows.CloseHandle(handle)
		t.Fatal("ancestor directory junction must be rejected")
	}
}

func TestOpenDirectoryNoReparseOpenExistingDoesNotCreate(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing")
	handle, err := openForACL(directory, DirectoryOpenExisting)
	if err == nil {
		windows.CloseHandle(handle)
		t.Fatal("open-existing of a missing directory must fail")
	}
	if _, statErr := os.Stat(directory); !os.IsNotExist(statErr) {
		t.Fatalf("open-existing created %s: %v", directory, statErr)
	}
}

func TestCreateDirectoryGuardKeepsDirectoryEntryWhileHeld(t *testing.T) {
	directory := t.TempDir()
	handle, err := openForACL(directory, DirectoryOpenExisting)
	if err != nil {
		t.Fatalf("open directory error = %v", err)
	}
	defer windows.CloseHandle(handle)
	guard, err := CreateDirectoryGuard(handle)
	if err != nil {
		t.Fatalf("CreateDirectoryGuard() error = %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		windows.CloseHandle(guard)
		t.Fatalf("guard entries = %v, %v", entries, err)
	}
	guardPath := filepath.Join(directory, entries[0].Name())
	if !strings.HasPrefix(entries[0].Name(), ".codex-provisioning-") || !strings.HasSuffix(entries[0].Name(), ".guard") {
		windows.CloseHandle(guard)
		t.Fatalf("guard name = %q", entries[0].Name())
	}
	if err := os.Remove(guardPath); err == nil {
		windows.CloseHandle(guard)
		t.Fatal("held guard must not be removable")
	}
	windows.CloseHandle(guard)
	entries, err = os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("guard entries after close = %v, %v", entries, err)
	}
}

func TestValidateLocalDirectoryPathAcceptsDriveAndVerbatimDrivePaths(t *testing.T) {
	for _, path := range []string{`C:\Users\alice\.codex`, `\\?\D:\Codex Data\home`} {
		if err := ValidateLocalDirectoryPath(path); err != nil {
			t.Fatalf("ValidateLocalDirectoryPath(%q) error = %v", path, err)
		}
	}
}

func TestValidateLocalDirectoryPathRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{
		`relative\.codex`,
		`C:relative\.codex`,
		`\\server\share\.codex`,
		`\\?\UNC\server\share\.codex`,
		`\\.\C:\Users\alice`,
		`C:\Users\alice\..\other`,
		`C:\Users\alice\.codex:stream`,
	} {
		if err := ValidateLocalDirectoryPath(path); err == nil {
			t.Fatalf("ValidateLocalDirectoryPath(%q) unexpectedly accepted", path)
		}
	}
}
