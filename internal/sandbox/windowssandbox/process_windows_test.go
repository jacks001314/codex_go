//go:build windows

package windowssandbox

import "testing"

func TestStartupInfoWithExplicitStdioRejectsEmptyHandles(t *testing.T) {
	desktop, err := PrepareLaunchDesktop(false)
	if err != nil {
		t.Fatalf("PrepareLaunchDesktop() error = %v", err)
	}
	if _, _, err := startupInfoWithExplicitStdio(&ProcessStdio{}, desktop); err == nil {
		t.Fatalf("startupInfoWithExplicitStdio() error = nil, want failure")
	}
}
