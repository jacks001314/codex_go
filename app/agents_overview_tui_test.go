package app

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"codex_go/appserverdaemon"
)

func TestInteractiveStartAgentsDaemonPlatformGate(t *testing.T) {
	err := interactiveStartAgentsDaemon()
	if runtime.GOOS == "windows" {
		if err == nil || !strings.Contains(err.Error(), "unsupported on Windows") {
			t.Fatalf("Windows start-daemon error = %v, want unsupported message", err)
		}
	} else if err != nil {
		t.Fatalf("non-Windows start-daemon error = %v", err)
	}
}

func TestInteractiveRemoteAgentsOverviewHandlersWireRemoteSource(t *testing.T) {
	// The handlers must be non-nil and accept the remote endpoint; the row
	// building itself is covered by TestAgentsOverviewRowsFromThreadsLikeRust.
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws://127.0.0.1:1", nil)
	ctx := context.Background()
	refresh := interactiveRemoteAgentsOverviewRefresh(ctx, endpoint)
	dispatch := interactiveRemoteAgentsOverviewDispatch(ctx, endpoint)
	stop := interactiveRemoteAgentsOverviewStop(ctx, endpoint)
	rename := interactiveRemoteAgentsOverviewRename(ctx, endpoint)
	if refresh == nil || dispatch == nil || stop == nil || rename == nil {
		t.Fatal("a remote agents-overview handler is nil")
	}
	// Calling them against an unreachable endpoint must return a connection
	// error, not panic (the dashboard surfaces it as a notice).
	if _, err := refresh(""); err == nil {
		t.Fatal("refresh against unreachable endpoint succeeded, want error")
	}
	if _, err := dispatch("prompt", ""); err == nil {
		t.Fatal("dispatch against unreachable endpoint succeeded, want error")
	}
	if err := stop("thread-1"); err == nil {
		t.Fatal("stop against unreachable endpoint succeeded, want error")
	}
	if err := rename("thread-1", "name"); err == nil {
		t.Fatal("rename against unreachable endpoint succeeded, want error")
	}
}
