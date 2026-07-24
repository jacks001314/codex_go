//go:build !windows

package win

import (
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox"
)

func TestSandboxUsersReportsExplicitUnsupportedOffWindows(t *testing.T) {
	if err := EnsureLocalGroup("group", "comment", nil); !windowssandbox.IsUnsupported(err) || !strings.Contains(err.Error(), "ensure_local_group") {
		t.Fatalf("EnsureLocalGroup() error = %v, want explicit unsupported", err)
	}
	if _, err := ResolveSID("Everyone"); !windowssandbox.IsUnsupported(err) || !strings.Contains(err.Error(), "resolve_sid") {
		t.Fatalf("ResolveSID() error = %v, want explicit unsupported", err)
	}
}
