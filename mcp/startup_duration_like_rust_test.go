package mcp

import (
	"testing"
	"time"
)

// Rust #63613: mcpServer/startupStatus/updated carries durationMs for the
// ready and failed transitions of a tracked startup attempt, and leaves it
// null for starting and for a transition without a tracked attempt.
func TestMCPStartupDurationReportedForReadyAndFailedLikeRust(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Name: "docs", Config: ServerConfig{Command: "docs-mcp", Enabled: true}},
	}})
	type event struct {
		status   MCPServerStartupState
		duration *uint64
	}
	var events []event
	remove := service.AddStartupStatusListener(func(_ string, _ string, status MCPServerStartupState, _ *string, _ error, durationMs *uint64) {
		events = append(events, event{status: status, duration: durationMs})
	})
	defer remove()

	service.markServerStartupStarted("docs")
	if len(events) != 1 || events[0].status != MCPServerStarting {
		t.Fatalf("starting events = %+v", events)
	}
	if events[0].duration != nil {
		t.Fatalf("starting duration = %d, want null", *events[0].duration)
	}

	// Backdate the attempt so the ready transition reports a measured value
	// without a real sleep. The inventory starts in the ready state, so move
	// it back to starting to model the connection attempt in flight.
	service.mu.Lock()
	service.startupStartedAt["docs"] = time.Now().Add(-150 * time.Millisecond)
	service.servers["docs"] = MCPServerStatus{Name: "docs", State: MCPServerStarting}
	service.mu.Unlock()
	service.recordInventoryStatus("docs", MCPServerStatus{Name: "docs", State: MCPServerReady}, false, false)
	if len(events) != 2 || events[1].status != MCPServerReady {
		t.Fatalf("ready events = %+v", events)
	}
	if events[1].duration == nil {
		t.Fatal("ready duration = nil, want a measured value")
	}
	if got := *events[1].duration; got < 150 || got > 5000 {
		t.Fatalf("ready duration = %d ms, want about 150", got)
	}
	service.mu.Lock()
	if _, tracked := service.startupStartedAt["docs"]; tracked {
		service.mu.Unlock()
		t.Fatal("startup attempt mark survived the ready transition")
	}
	service.mu.Unlock()

	// A same-state refresh is not a transition: no second event, no duration.
	service.recordInventoryStatus("docs", MCPServerStatus{Name: "docs", State: MCPServerReady}, false, false)
	if len(events) != 2 {
		t.Fatalf("same-state refresh emitted %d events, want 2", len(events))
	}

	// A fresh attempt that fails reports its own duration exactly once.
	service.markServerStartupStarted("docs")
	service.mu.Lock()
	service.startupStartedAt["docs"] = time.Now().Add(-40 * time.Millisecond)
	service.servers["docs"] = MCPServerStatus{Name: "docs", State: MCPServerStarting}
	service.mu.Unlock()
	reason := "handshake"
	service.recordInventoryStatus("docs", MCPServerStatus{Name: "docs", State: MCPServerFailed, FailureReason: &reason}, false, false)
	if len(events) != 4 || events[3].status != MCPServerFailed || events[3].duration == nil {
		t.Fatalf("failed events = %+v, want a measured duration", events)
	}
	if got := *events[3].duration; got < 40 {
		t.Fatalf("failed duration = %d ms, want at least 40", got)
	}

	// Without a tracked attempt (a reused connection or an externally
	// reported result) the ready transition carries a null duration.
	service.recordInventoryStatus("docs", MCPServerStatus{Name: "docs", State: MCPServerStarting}, false, false)
	if len(events) != 5 || events[4].duration != nil {
		t.Fatalf("untracked starting events = %+v", events)
	}
	service.recordInventoryStatus("docs", MCPServerStatus{Name: "docs", State: MCPServerReady}, false, false)
	if len(events) != 6 || events[5].status != MCPServerReady {
		t.Fatalf("untracked ready events = %+v", events)
	}
	if events[5].duration != nil {
		t.Fatalf("untracked ready duration = %d, want null", *events[5].duration)
	}
}
