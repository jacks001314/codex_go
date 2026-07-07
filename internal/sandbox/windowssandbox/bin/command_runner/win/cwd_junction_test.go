package win

import (
	"path/filepath"
	"testing"

	"codex_go/internal/sandbox/windowssandbox"
)

func TestJunctionHelpers(t *testing.T) {
	if JunctionNameForPath(`C:\repo`) == JunctionNameForPath(`C:\other`) {
		t.Fatalf("junction names should differ")
	}
	got := JunctionRootForUserProfile(`C:\Users\codex`)
	want := filepath.Join(`C:\Users\codex`, ".codex", ".sandbox", "cwd")
	if got != want {
		t.Fatalf("JunctionRootForUserProfile() = %q, want %q", got, want)
	}
	if _, err := CreateCWDJunction(""); err != windowssandbox.ErrInvalidRequest {
		t.Fatalf("CreateCWDJunction(empty) error = %v", err)
	}
}
