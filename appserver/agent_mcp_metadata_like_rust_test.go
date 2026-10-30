package appserver

import (
	"testing"

	"codex_go/mcp"
)

// Mirrors Rust #58378's thread extension data: a host selects one agent plugin
// registration per thread, and a thread without an attachment keeps the router
// default (or none at all).
func TestRuntimeRouterThreadAgentMetadataRecipientLikeRust(t *testing.T) {
	defaultRecipient := &mcp.AgentMCPMetadataRecipient{
		PluginID:   "agents@local",
		PluginRoot: "file:///plugins/agents",
		ServerName: "agents",
	}
	threadRecipient := &mcp.AgentMCPMetadataRecipient{
		PluginID:   "other@local",
		PluginRoot: "file:///plugins/other",
		ServerName: "other",
	}
	router := &RuntimeRouter{agentMCPMetadataDefault: defaultRecipient}
	if got := router.threadAgentMCPMetadataRecipient("thread-a"); got != defaultRecipient {
		t.Fatalf("unattached thread recipient = %#v, want the router default", got)
	}
	router.SetThreadAgentMCPMetadataRecipient("thread-a", threadRecipient)
	if got := router.threadAgentMCPMetadataRecipient("thread-a"); got != threadRecipient {
		t.Fatalf("thread recipient = %#v, want the installed recipient", got)
	}
	if got := router.threadAgentMCPMetadataRecipient("thread-b"); got != defaultRecipient {
		t.Fatalf("other thread recipient = %#v, want the router default", got)
	}
	router.SetThreadAgentMCPMetadataRecipient("thread-a", nil)
	if got := router.threadAgentMCPMetadataRecipient("thread-a"); got != defaultRecipient {
		t.Fatalf("recipient after removal = %#v, want the router default", got)
	}
	router.SetThreadAgentMCPMetadataRecipient("", threadRecipient)
	if got := router.threadAgentMCPMetadataRecipient(""); got != defaultRecipient {
		t.Fatalf("empty thread recipient = %#v, want the router default", got)
	}
}
