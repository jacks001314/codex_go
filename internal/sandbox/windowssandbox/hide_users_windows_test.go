//go:build windows

package windowssandbox

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideDirectorySetsHiddenAndSystem(t *testing.T) {
	dir := t.TempDir()
	changed, err := HideDirectory(dir)
	if err != nil {
		t.Fatalf("HideDirectory() error = %v", err)
	}
	if !changed {
		t.Fatalf("HideDirectory() changed = false, want true")
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatalf("UTF16PtrFromString() error = %v", err)
	}
	attrs, err := windows.GetFileAttributes(name)
	if err != nil {
		t.Fatalf("GetFileAttributes() error = %v", err)
	}
	if attrs&windows.FILE_ATTRIBUTE_HIDDEN == 0 || attrs&windows.FILE_ATTRIBUTE_SYSTEM == 0 {
		t.Fatalf("attrs = %#x, want hidden|system", attrs)
	}
	changed, err = HideDirectory(dir)
	if err != nil {
		t.Fatalf("HideDirectory(second) error = %v", err)
	}
	if changed {
		t.Fatalf("HideDirectory(second) changed = true, want false")
	}
}

func TestHideCurrentUserProfileDirIgnoresMissingProfile(t *testing.T) {
	t.Setenv("USERPROFILE", `Z:\definitely\missing\codex-profile`)
	if err := HideCurrentUserProfileDir(""); err != nil {
		t.Fatalf("HideCurrentUserProfileDir() error = %v", err)
	}
}
