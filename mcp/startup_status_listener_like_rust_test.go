package mcp

import "testing"

// Rust #61257: startup transitions carry the registration scope so clients can
// tell same-named servers in different environments apart.
func TestMCPStartupStatusListenerCarriesEnvironmentLikeRust(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {
			Name: "docs",
			Config: ServerConfig{
				Command:       "docs-mcp",
				EnvironmentID: "env-a",
				Enabled:       true,
			},
		},
	}})
	type event struct {
		name     string
		env      string
		status   MCPServerStartupState
		hasError bool
	}
	var events []event
	remove := service.AddStartupStatusListener(func(name, environmentID string, status MCPServerStartupState, _ *string, err error) {
		events = append(events, event{name: name, env: environmentID, status: status, hasError: err != nil})
	})
	service.markServerStartupStarted("docs")
	if len(events) != 1 || events[0].status != MCPServerStarting || events[0].env != "env-a" || events[0].name != "docs" {
		t.Fatalf("starting events = %+v", events)
	}

	service.mu.Lock()
	service.servers["docs"] = MCPServerStatus{Name: "docs", State: MCPServerStarting}
	service.mu.Unlock()
	service.recordInventoryStatus("docs", MCPServerStatus{Name: "docs", State: MCPServerReady}, false, false)
	if len(events) != 2 || events[1].status != MCPServerReady || events[1].env != "env-a" {
		t.Fatalf("ready events = %+v", events)
	}

	// A refresh that keeps the same state is not a transition.
	service.recordInventoryStatus("docs", MCPServerStatus{Name: "docs", State: MCPServerReady}, false, false)
	if len(events) != 2 {
		t.Fatalf("unchanged state emitted another event: %+v", events)
	}

	remove()
	service.markServerStartupStarted("docs")
	if len(events) != 2 {
		t.Fatalf("removed listener still fired: %+v", events)
	}
}
