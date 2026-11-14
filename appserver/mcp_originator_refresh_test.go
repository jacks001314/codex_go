package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/turn"
)

func TestMCPOriginatorChangeRefreshesRuntime(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	params := &turn.TurnStartParams{ThreadID: "parent", Originator: "desktop"}
	builds, updates := 0, 0
	create := func(*config.Config) *mcp.MCPService { builds++; return mcp.NewMCPService(nil) }
	update := func(*mcp.MCPService, *config.Config) { updates++ }
	first := coordinator.serviceForThread("parent", nil, 0, mcpEnvironmentRuntimeFingerprint(params), create, update)
	defer first.Close()
	coordinator.serviceForThread("parent", nil, 0, mcpEnvironmentRuntimeFingerprint(params), create, update)
	params.Originator = "guardian"
	coordinator.serviceForThread("parent", nil, 0, mcpEnvironmentRuntimeFingerprint(params), create, update)
	if builds != 1 || updates != 1 {
		t.Fatalf("builds=%d updates=%d want 1/1", builds, updates)
	}
	// Clearing identity must refresh too; normalization must not create a
	// fresh connection for insignificant surrounding whitespace.
	params.Originator = " guardian "
	coordinator.serviceForThread("parent", nil, 0, mcpEnvironmentRuntimeFingerprint(params), create, update)
	if updates != 1 {
		t.Fatalf("whitespace caused %d updates", updates)
	}
	params.Originator = ""
	coordinator.serviceForThread("parent", nil, 0, mcpEnvironmentRuntimeFingerprint(params), create, update)
	if updates != 2 {
		t.Fatalf("clearing originator updates=%d want 2", updates)
	}

}
