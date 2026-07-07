//go:build !windows

package win

import (
	"strings"
	"testing"

	"codex_go/internal/sandbox/windowssandbox"
)

func TestReadACLMutexReportsExplicitUnsupportedOffWindows(t *testing.T) {
	if _, err := ReadACLMutexExists(); !windowssandbox.IsUnsupported(err) || !strings.Contains(err.Error(), "read_acl_mutex.exists") {
		t.Fatalf("ReadACLMutexExists() error = %v, want explicit unsupported", err)
	}
	if _, _, err := AcquireReadACLMutex(); !windowssandbox.IsUnsupported(err) || !strings.Contains(err.Error(), "read_acl_mutex.acquire") {
		t.Fatalf("AcquireReadACLMutex() error = %v, want explicit unsupported", err)
	}
}
