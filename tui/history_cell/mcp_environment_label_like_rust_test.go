package historycell

import (
	"strings"
	"testing"
)

// Rust #61257 renders the registration scope next to the server name so two
// same-named environment registrations stay distinguishable.
func TestMCPEnvironmentScopeRendersInInventoryAndToolCallLikeRust(t *testing.T) {
	inventory := NewMCPToolsOutputFromStatuses([]McpServerStatus{
		{Name: "notes", EnvironmentID: "env-b", Auth: "Bearer token", Tools: []string{"lookup"}},
		{Name: "notes", EnvironmentID: "env-a", Auth: "Unsupported", Tools: []string{"lookup"}},
	}, false)
	lines := strings.Join(inventory.DisplayLines(120), "\n")
	firstA := strings.Index(lines, "notes (environment env-a)")
	firstB := strings.Index(lines, "notes (environment env-b)")
	if firstA < 0 || firstB < 0 {
		t.Fatalf("inventory lines = %q", lines)
	}
	if firstA > firstB {
		t.Fatalf("inventory order does not follow name then environment: %q", lines)
	}

	cell := NewMcpToolCall("call-1", McpInvocation{
		Server:        "notes",
		Tool:          "lookup",
		Arguments:     `{"q":"x"}`,
		EnvironmentID: "env-b",
	}, McpToolResult{Content: []string{"ok"}})
	rendered := strings.Join(cell.DisplayLines(200), "\n")
	if !strings.Contains(rendered, "notes.lookup({\"q\":\"x\"}) (environment env-b)") {
		t.Fatalf("tool call lines = %q", rendered)
	}

	local := NewMcpToolCall("call-2", McpInvocation{Server: "notes", Tool: "lookup"}, McpToolResult{Content: []string{"ok"}})
	if rendered := strings.Join(local.DisplayLines(200), "\n"); strings.Contains(rendered, "(environment") {
		t.Fatalf("local tool call gained an environment label: %q", rendered)
	}
}

