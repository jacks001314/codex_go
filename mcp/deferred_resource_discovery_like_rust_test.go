package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeferredResourceDiscoveryLeavesDormantServersUnstartedLikeRust pins Rust
// #60423: aggregate discovery reports a deferred server instead of launching it,
// while an explicit server read still starts and queries it.
func TestDeferredResourceDiscoveryLeavesDormantServersUnstartedLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "deferred-discovery")
	service := newLazyStdioService(executable, logPath, true, false)
	defer func() { _ = service.Close() }()

	response, err := service.ListStatusChecked(&MCPListServerStatusParams{SkipDeferredStartup: true})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 0 {
		t.Fatalf("helper starts = %d, discovery must leave a deferred server unstarted", starts)
	}
	status := statusForServer(t, response, "shared")
	if status.RuntimeStatus == nil || *status.RuntimeStatus != MCPConnectionNotStarted {
		t.Fatalf("runtime status = %#v, want notStarted so the listing can name it", status.RuntimeStatus)
	}

	// The explicit query is what starts it.
	name := "shared"
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{ServerName: &name}); err != nil {
		t.Fatalf("ListStatusChecked(shared) error = %v", err)
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts == 0 {
		t.Fatal("an explicit server read must start the deferred helper")
	}
}
