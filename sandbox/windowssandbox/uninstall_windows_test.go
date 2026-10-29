//go:build windows

package windowssandbox

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestLookupLocalUserSIDReportsMissingAccount(t *testing.T) {
	name := fmt.Sprintf("codex-missing-%d", time.Now().UnixNano())
	sid, exists, err := lookupLocalUserSID(name)
	if err != nil {
		t.Fatalf("lookupLocalUserSID(%q) error = %v", name, err)
	}
	if exists || sid != "" {
		t.Fatalf("missing account resolved as %q, %v", sid, exists)
	}
}

// TestRemoveLegacySandboxGroupKeepsRetryableStateLikeRust pins Rust #45799's
// uninstall_windows::finish: the machine-wide group survives while an earlier
// cleanup step failed, so a retry can still resolve the sandbox accounts.
func TestRemoveLegacySandboxGroupKeepsRetryableStateLikeRust(t *testing.T) {
	var lines []string
	report := func(message string) { lines = append(lines, message) }
	removed := false
	err := removeLegacySandboxGroupAfter(
		[]string{"remove hidden-user entries: failed, access denied"},
		report,
		func() error {
			removed = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("retained group returned %v", err)
	}
	if removed {
		t.Fatal("sandbox group was removed after an earlier failure")
	}
	if len(lines) != 1 || lines[0] != "retaining sandbox group until cleanup finishes" {
		t.Fatalf("report = %#v", lines)
	}
}

// TestRemoveLegacySandboxGroupReportsOutcomeLikeRust pins the success and
// failure wording of Rust #45799's sandbox-group cleanup step.
func TestRemoveLegacySandboxGroupReportsOutcomeLikeRust(t *testing.T) {
	var lines []string
	report := func(message string) { lines = append(lines, message) }
	if err := removeLegacySandboxGroupAfter(nil, report, func() error { return nil }); err != nil {
		t.Fatalf("clean group removal: %v", err)
	}
	if len(lines) != 1 || lines[0] != "remove sandbox group: completed" {
		t.Fatalf("success report = %#v", lines)
	}

	lines = nil
	err := removeLegacySandboxGroupAfter(nil, report, func() error {
		return errors.New("code 5")
	})
	if err == nil || err.Error() != "remove sandbox group: failed, code 5" {
		t.Fatalf("failure error = %v", err)
	}
	if len(lines) != 1 || lines[0] != "remove sandbox group: failed, code 5" {
		t.Fatalf("failure report = %#v", lines)
	}
}
