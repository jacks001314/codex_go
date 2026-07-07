//go:build windows

package win

import (
	"os"
	"testing"
)

func TestCreateCWDJunction(t *testing.T) {
	userprofile := t.TempDir()
	target := t.TempDir()
	t.Setenv("USERPROFILE", userprofile)
	got, err := CreateCWDJunction(target)
	if err != nil {
		t.Fatalf("CreateCWDJunction() error = %v", err)
	}
	if got == "" {
		t.Skip("mklink /J unavailable in this environment")
	}
	if _, err := os.Lstat(got); err != nil {
		t.Fatalf("junction missing: %v", err)
	}
	reparse, err := IsReparsePoint(got)
	if err != nil {
		t.Fatalf("IsReparsePoint() error = %v", err)
	}
	if !reparse {
		t.Fatalf("junction is not a reparse point: %s", got)
	}
	reused, err := CreateCWDJunction(target)
	if err != nil {
		t.Fatalf("CreateCWDJunction(reuse) error = %v", err)
	}
	if reused != got {
		t.Fatalf("reused junction = %q, want %q", reused, got)
	}
}
