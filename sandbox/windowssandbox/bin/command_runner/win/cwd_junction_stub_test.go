//go:build !windows

package win

import (
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox"
)

func TestCWDJunctionReportsExplicitUnsupportedOffWindows(t *testing.T) {
	if _, err := CreateCWDJunction(t.TempDir()); !windowssandbox.IsUnsupported(err) || !strings.Contains(err.Error(), "cwd_junction") {
		t.Fatalf("CreateCWDJunction() error = %v, want explicit unsupported", err)
	}
	if _, err := IsReparsePoint(t.TempDir()); !windowssandbox.IsUnsupported(err) || !strings.Contains(err.Error(), "cwd_junction.reparse") {
		t.Fatalf("IsReparsePoint() error = %v, want explicit unsupported", err)
	}
}
